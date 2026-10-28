package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

func find(checks []check, name string) *check {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

func TestDoctorHealthyServer(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(int, api.ChatRequest) []string { return nil })
	setupEnv(t, srv.URL)
	conn, _ := config.LoadConnection()

	checks := runChecks(context.Background(), doctorInput{conn: conn, agent: "sigma", cwd: t.TempDir()})
	for _, name := range []string{"connection", "server", "api key", "agent"} {
		c := find(checks, name)
		if c == nil || c.Status != checkOK {
			t.Errorf("%s: %+v", name, c)
		}
	}
	if c := find(checks, "api key"); c != nil && !strings.Contains(c.Detail, "1 agent") {
		t.Errorf("api key detail = %q", c.Detail)
	}
	if c := find(checks, "connection"); strings.Contains(c.Detail, "sk-test-key") {
		t.Errorf("the key must be masked: %q", c.Detail)
	}
}

func TestDoctorNotConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", "")
	t.Setenv("TLITH_API_KEY", "")
	_, err := config.LoadConnection()
	checks := runChecks(context.Background(), doctorInput{connErr: err, cwd: t.TempDir()})
	c := find(checks, "connection")
	if c == nil || c.Status != checkFail || !strings.Contains(c.Fix, "tlc configure") {
		t.Fatalf("connection: %+v", c)
	}
	if find(checks, "server") != nil {
		t.Fatal("network checks make no sense without a connection")
	}
}

func TestDoctorRejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key"}}`))
	}))
	t.Cleanup(srv.Close)
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: srv.URL, APIKey: "sk-bad"}, cwd: t.TempDir()})
	if c := find(checks, "server"); c.Status != checkOK {
		t.Errorf("server: %+v", c)
	}
	c := find(checks, "api key")
	if c.Status != checkFail || !strings.Contains(c.Detail, "rejected") {
		t.Fatalf("api key: %+v", c)
	}
}

func TestDoctorUnreachableServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: url, APIKey: "sk-x"}, cwd: t.TempDir()})
	c := find(checks, "server")
	if c == nil || c.Status != checkFail || !strings.Contains(c.Detail, "cannot reach") {
		t.Fatalf("server: %+v", c)
	}
	if find(checks, "api key") != nil {
		t.Fatal("no point checking the key when the server is unreachable")
	}
}

func TestDoctorFlagsPlainHTTPToARemoteHost(t *testing.T) {
	t.Setenv("TLC_ALLOW_INSECURE", "")
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: "http://turnlith.example.com", APIKey: "sk-x"}, cwd: t.TempDir()})
	c := find(checks, "connection")
	if c.Status != checkFail || !strings.Contains(c.Detail, "plain http") {
		t.Fatalf("connection: %+v", c)
	}
}

func TestDoctorWarnsWhenTheAgentsProviderHasNoKey(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(int, api.ChatRequest) []string { return nil })
	srv.providers = []api.ProviderStatus{{Provider: "gemini", Available: false, Source: ""}}
	setupEnv(t, srv.URL)
	conn, _ := config.LoadConnection()
	checks := runChecks(context.Background(), doctorInput{conn: conn, agent: "sigma", cwd: t.TempDir()})
	c := find(checks, "agent")
	if c == nil || c.Status != checkWarn || !strings.Contains(c.Detail, "gemini") {
		t.Fatalf("agent: %+v", c)
	}
}

func TestDoctorCommandPrintsAReportAndExitsNonZeroOnFailure(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(int, api.ChatRequest) []string { return nil })
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir(), "doctor")
	if res.code != 0 {
		t.Fatalf("healthy: code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "connection")
	mustContain(t, "stdout", res.stdout, "server")
	mustContain(t, "stdout", res.stdout, "✓")

	t.Setenv("TLITH_BASE_URL", "http://127.0.0.1:1")
	res = runTLC(t, t.TempDir(), "doctor")
	if res.code != 1 {
		t.Fatalf("broken: code=%d stdout=%q", res.code, res.stdout)
	}
	mustContain(t, "stdout", res.stdout, "✗")
}

func TestDoctorSlashCommandRuns(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(int, api.ChatRequest) []string { return nil })
	a, out := testApp(t, srv.URL, testAgents())
	a.cwd = t.TempDir()
	a.handleSlash("/doctor")
	mustContain(t, "output", out.String(), "server")
	mustContain(t, "output", out.String(), "api key")
}
