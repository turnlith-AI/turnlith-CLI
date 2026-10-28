package cli

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

// withStdin replaces os.Stdin with a regular file holding content, the way a
// shell redirect or `cmd | tlc` presents piped input.
func withStdin(t *testing.T, content string) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdin-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; f.Close() })
}

func TestPrintModeExitsNonZeroOnServerError(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string { return nil })
	srv.chatFail = func(turn int) (int, string) {
		return 400, `{"error":{"message":"context too large","code":"bad_request"}}`
	}
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d, want 1; stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "context too large")
	if strings.Contains(res.stdout, "context too large") {
		t.Fatalf("error leaked into stdout: %q", res.stdout)
	}
}

func TestPrintModeAppendsPipedStdinToPrompt(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{finalChunk(t, "reviewed", 10, 1)}
	})
	setupEnv(t, srv.URL)
	withStdin(t, "diff --git a/x b/x\n+added line\n")

	res := runTLC(t, t.TempDir(), "-p", "review this diff")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}
	got, _ := lastMessage(t, reqs[0]).Content.(string)
	mustContain(t, "prompt", got, "review this diff")
	mustContain(t, "prompt", got, "+added line")
}

func TestPrintModeStdinAloneIsThePrompt(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{finalChunk(t, "ok", 10, 1)}
	})
	setupEnv(t, srv.URL)
	withStdin(t, "explain main.go\n")

	res := runTLC(t, t.TempDir(), "-p")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	got, _ := lastMessage(t, srv.requests()[0]).Content.(string)
	if got != "explain main.go" {
		t.Fatalf("prompt = %q", got)
	}
}

func fastRetries(t *testing.T) {
	t.Helper()
	clientRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	t.Cleanup(func() { clientRetryDelays = nil })
}

func TestPrintModeRetriesRateLimitAndSucceeds(t *testing.T) {
	fastRetries(t)
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{finalChunk(t, "made it", 10, 2)}
	})
	srv.chatFail = func(turn int) (int, string) {
		if turn < 2 {
			return 429, `{"error":{"message":"slow down","code":"rate_limited"}}`
		}
		return 0, ""
	}
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "hi")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "made it")
	mustContain(t, "stderr", res.stderr, "rate limited by the server")
	mustContain(t, "stderr", res.stderr, "retrying in")
	if strings.Contains(res.stdout, "retrying") {
		t.Fatalf("retry notice leaked into stdout: %q", res.stdout)
	}
}

func TestPrintModeTruncatedStreamIsReportedAndFails(t *testing.T) {
	fastRetries(t)
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{textChunk(t, "half an ans")}
	})
	srv.omitDone = true
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d, want 1; stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "ended before the answer was complete")
	mustContain(t, "stdout", res.stdout, "half an ans")
	if n := len(srv.requests()); n != 1 {
		t.Fatalf("a stream that produced output must not be re-sent (requests=%d)", n)
	}
}

func TestFriendlyErrMessages(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"429", &api.APIError{Status: 429, Message: "slow down"}, "rate limiting"},
		{"503", &api.APIError{Status: 503, Message: "overloaded"}, "turnlith server had a problem (HTTP 503)"},
		{"truncated", api.ErrTruncatedStream, "ended before the answer was complete"},
		{"stalled", api.ErrStreamStalled, api.EnvStreamIdleTimeout},
		{"refused", &url.Error{Op: "Post", URL: "http://localhost:1/v1/chat/completions?x=secret", Err: errors.New("dial tcp: connection refused")}, "cannot reach http://localhost:1"},
	}
	for _, c := range cases {
		got := friendlyErr(c.err)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, got, c.want)
		}
		if strings.Contains(got, "secret") {
			t.Errorf("%s: query string leaked: %q", c.name, got)
		}
	}
}

func TestPlainHTTPToARemoteHostIsRefused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", "http://turnlith.example.com")
	t.Setenv("TLITH_API_KEY", "sk-test-key")
	t.Setenv("TLC_ALLOW_INSECURE", "")

	res := runTLC(t, t.TempDir(), "-p", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "plain http")
	mustContain(t, "stderr", res.stderr, "TLC_ALLOW_INSECURE")
}

func TestApiKeyFlagWarnsAboutProcessList(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{finalChunk(t, "ok", 1, 1)}
	})
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir(), "-p", "--api-key", "sk-from-flag", "hi")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "process list")
	if strings.Contains(res.stderr, "sk-from-flag") || strings.Contains(res.stdout, "sk-from-flag") {
		t.Fatal("the key must never be echoed")
	}
}

func TestConfigureReadsKeyFromStdin(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string { return nil })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", "")
	t.Setenv("TLITH_API_KEY", "")
	withStdin(t, "sk-piped-key\n")

	res := runTLC(t, t.TempDir(), "configure", "--base-url", srv.URL, "--api-key-stdin")
	if res.code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	conn, err := config.LoadConnection()
	if err != nil || conn.APIKey != "sk-piped-key" {
		t.Fatalf("saved connection = %+v err=%v", conn, err)
	}
}

func TestConfigureRefusesPlainHTTPRemote(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", "")
	t.Setenv("TLITH_API_KEY", "")
	t.Setenv("TLC_ALLOW_INSECURE", "")
	res := runTLC(t, t.TempDir(), "configure", "--base-url", "http://turnlith.example.com", "--api-key", "sk-x")
	if res.code == 0 {
		t.Fatal("configure must refuse plain http to a remote host")
	}
	mustContain(t, "stdout", res.stdout, "plain http")
}
