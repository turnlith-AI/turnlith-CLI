package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
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
