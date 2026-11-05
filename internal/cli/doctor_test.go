package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// noClients hides any real coding client from detection.
func noClients(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestDoctorHealthyServer(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)
	noClients(t)
	conn, _ := config.LoadConnection()

	checks := runChecks(context.Background(), doctorInput{conn: conn})
	for _, name := range []string{"connection", "server", "api key", "agents", "coding clients"} {
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
	for _, gone := range []string{"terminal", "shell", "git", "ripgrep", "transcripts", "instructions"} {
		if find(checks, gone) != nil {
			t.Errorf("the %q check belonged to the coding agent and should be gone", gone)
		}
	}
}

func TestDoctorNotConfigured(t *testing.T) {
	isolate(t)
	noClients(t)
	_, err := config.LoadConnection()
	checks := runChecks(context.Background(), doctorInput{connErr: err})
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
	isolate(t)
	noClients(t)
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: srv.URL, APIKey: "sk-bad"}})
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
	isolate(t)
	noClients(t)
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: url, APIKey: "sk-x"}})
	c := find(checks, "server")
	if c == nil || c.Status != checkFail || !strings.Contains(c.Detail, "cannot reach") {
		t.Fatalf("server: %+v", c)
	}
	if find(checks, "api key") != nil {
		t.Fatal("no point checking the key when the server is unreachable")
	}
}

func TestDoctorFailsFastOnAnOverloadedServer(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	isolate(t)
	noClients(t)
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: srv.URL, APIKey: "sk-x"}})
	if c := find(checks, "server"); c == nil || c.Status != checkFail {
		t.Fatalf("server: %+v", c)
	}
	if hits != 1 {
		t.Fatalf("a diagnostic must not sit in retry backoff (hits=%d)", hits)
	}
}

func TestDoctorFlagsPlainHTTPToARemoteHost(t *testing.T) {
	isolate(t)
	noClients(t)
	checks := runChecks(context.Background(), doctorInput{conn: config.Connection{BaseURL: "http://turnlith.example.com", APIKey: "sk-x"}})
	c := find(checks, "connection")
	if c.Status != checkFail || !strings.Contains(c.Detail, "plain http") {
		t.Fatalf("connection: %+v", c)
	}
}

func TestDoctorWarnsPerAgentWhenItsProviderHasNoKey(t *testing.T) {
	agents := append(testAgents(), api.Agent{Codename: "fast", Model: "groq:llama-4-scout"})
	srv := newMockServer(t, agents)
	srv.providers = []api.ProviderStatus{
		{Provider: "gemini", Available: false},
		{Provider: "groq", Available: true},
	}
	setupEnv(t, srv.URL)
	noClients(t)
	conn, _ := config.LoadConnection()
	checks := runChecks(context.Background(), doctorInput{conn: conn})
	c := find(checks, "agent sigma")
	if c == nil || c.Status != checkWarn || !strings.Contains(c.Detail, "gemini") {
		t.Fatalf("agent sigma: %+v", c)
	}
	if find(checks, "agent fast") != nil {
		t.Fatal("an agent whose provider is fine must not be flagged")
	}
	if find(checks, "agents") != nil {
		t.Fatal("the all-clear line must not appear next to a problem")
	}
}

func TestDoctorChecksTheConfigFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits")
	}
	isolate(t)
	noClients(t)
	if c := find(localChecks(), "config file"); c != nil {
		t.Fatalf("no file, no check: %+v", c)
	}
	if _, err := config.SaveConnection(config.Connection{BaseURL: "https://turnlith.com", APIKey: "sk-x"}); err != nil {
		t.Fatal(err)
	}
	if c := find(localChecks(), "config file"); c == nil || c.Status != checkOK {
		t.Fatalf("private file: %+v", c)
	}
	if err := os.Chmod(config.ConnectionPath(), 0o644); err != nil {
		t.Fatal(err)
	}
	c := find(localChecks(), "config file")
	if c == nil || c.Status != checkWarn || !strings.Contains(c.Fix, "chmod 600") {
		t.Fatalf("world-readable file: %+v", c)
	}
}

func TestDoctorReportsCodingClients(t *testing.T) {
	srv, home := clientEnv(t)
	_ = srv
	// cursor is "installed" (its config dir exists) but turnlith is not wired in
	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	checks := clientChecks()
	c := find(checks, "cursor")
	if c == nil || c.Status != checkWarn || !strings.Contains(c.Fix, "tlc install cursor") {
		t.Fatalf("cursor before install: %+v", c)
	}
	if find(checks, "coding clients") != nil {
		t.Fatal("the none-detected line must not appear when a client was found")
	}

	if res := runTLC(t, t.TempDir(), "install", "cursor"); res.code != 0 {
		t.Fatalf("install: %d %s%s", res.code, res.stdout, res.stderr)
	}
	c = find(clientChecks(), "cursor")
	if c == nil || c.Status != checkOK {
		t.Fatalf("cursor after install: %+v", c)
	}

	if res := runTLC(t, t.TempDir(), "uninstall", "cursor"); res.code != 0 {
		t.Fatalf("uninstall: %d", res.code)
	}
	if c = find(clientChecks(), "cursor"); c == nil || c.Status != checkWarn {
		t.Fatalf("cursor after uninstall: %+v", c)
	}
}

func TestDoctorCommandPrintsAReportAndExitsNonZeroOnFailure(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)
	noClients(t)
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

func TestDoctorWorksWithoutAnyConfiguration(t *testing.T) {
	isolate(t)
	noClients(t)
	res := runTLC(t, t.TempDir(), "doctor")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	mustContain(t, "stdout", res.stdout, "tlc configure")
	mustContain(t, "stdout", res.stdout, "version")
}
