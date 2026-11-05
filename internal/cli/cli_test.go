package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

// ------------------------------------------------------------------ fixtures

func strPtr(s string) *string { return &s }

func testAgents() []api.Agent {
	return []api.Agent{{
		ID:       "agent-1",
		Codename: "sigma",
		Label:    strPtr("Sigma"),
		Model:    "gemini:gemini-3.8-flash",
	}}
}

// -------------------------------------------------------------- mock server

// mockServer stands in for a turnlith server: /health, /v1/models and agent
// creation.
type mockServer struct {
	*httptest.Server

	agents    []api.Agent
	providers []api.ProviderStatus
	catalog   []api.CatalogModel

	mu sync.Mutex
	// modelsHits counts GET /v1/models — `tlc install` lists the agents there.
	modelsHits int
	nextID     int
	creates    []api.CreateAgentInput
}

// testProviders is the per-provider capability fixture: gemini and openai
// expose reasoning levels, groq has no reasoning control.
func testProviders() []api.ProviderStatus {
	return []api.ProviderStatus{
		{Provider: "gemini", Available: true, Source: "env", ReasoningEfforts: []string{"minimal", "low", "medium", "high"}},
		{Provider: "openai", Available: true, Source: "env", ReasoningEfforts: []string{"none", "minimal", "low", "medium", "high", "xhigh"}},
		{Provider: "groq", Available: true, Source: "env"},
	}
}

// testCatalog is the pickable-model fixture, including one unavailable entry.
func testCatalog() []api.CatalogModel {
	return []api.CatalogModel{
		{ID: "gemini:gemini-3.8-flash", Label: "Gemini 3.8 Flash", Provider: "gemini", Available: true, Source: "catalog"},
		{ID: "openai:gpt-6-sol", Label: "GPT-6 Sol", Provider: "openai", Available: true, Source: "catalog"},
		{ID: "groq:llama-4-scout", Label: "Llama 4 Scout", Provider: "groq", Available: true, Source: "catalog"},
		{ID: "openai:gpt-6-luna", Label: "GPT-6 Luna", Provider: "openai", Available: false, Source: "catalog"},
	}
}

func newMockServer(t *testing.T, agents []api.Agent) *mockServer {
	t.Helper()
	m := &mockServer{agents: agents, providers: testProviders(), catalog: testCatalog()}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			writeJSON(w, map[string]any{"status": "ok"})
		case "/v1/models":
			m.mu.Lock()
			m.modelsHits++
			agents := append([]api.Agent(nil), m.agents...)
			m.mu.Unlock()
			data := make([]map[string]string, 0, len(agents))
			for _, a := range agents {
				data = append(data, map[string]string{"id": a.Codename, "object": "model"})
			}
			writeJSON(w, map[string]any{
				"data":      data,
				"agents":    agents,
				"providers": m.providers,
				"models":    m.catalog,
			})
		case "/v1/agents":
			m.handleAgentCreate(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

// handleAgentCreate answers POST /v1/agents: it records the body and returns
// the created agent, generating a codename when none was given.
func (m *mockServer) handleAgentCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var input api.CreateAgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	agent := api.Agent{Codename: input.Codename, Model: input.Model}
	if input.Label != "" {
		agent.Label = &input.Label
	}
	if input.ReasoningEffort != "" {
		effort := input.ReasoningEffort
		agent.ReasoningEffort = &effort
	}
	m.mu.Lock()
	m.nextID++
	m.creates = append(m.creates, input)
	if agent.Codename == "" {
		agent.Codename = fmt.Sprintf("auto-%d", m.nextID)
	}
	agent.ID = fmt.Sprintf("agent-%d", 100+m.nextID)
	m.agents = append(m.agents, agent)
	m.mu.Unlock()

	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(agent)
}

func (m *mockServer) createdAgents() []api.CreateAgentInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]api.CreateAgentInput(nil), m.creates...)
}

func (m *mockServer) hits() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.modelsHits
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// -------------------------------------------------------------- run helpers

type runResult struct {
	code   int
	stdout string
	stderr string
}

// runTLC runs Main with stdout/stderr captured to temp files and the process
// working directory moved into dir. Colors stay off because the outputs are
// regular files, not terminals.
func runTLC(t *testing.T, dir string, args ...string) runResult {
	t.Helper()
	outFile, err := os.CreateTemp(t.TempDir(), "stdout-*")
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	t.Cleanup(func() { os.Stdout, os.Stderr = oldOut, oldErr })
	t.Chdir(dir)

	code := Main(args)

	os.Stdout, os.Stderr = oldOut, oldErr
	return runResult{code: code, stdout: readFile(t, outFile), stderr: readFile(t, errFile)}
}

func readFile(t *testing.T, f *os.File) string {
	t.Helper()
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// withStdin makes the next run read content from a pipe, the way
// `echo $KEY | tlc configure --api-key-stdin` does.
func withStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; r.Close() })
}

// isolate points HOME and the config/data directories at fresh temp dirs and
// clears the connection variables. HOME is redirected too, because macOS
// resolves ~/.config from it directly.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", "")
	t.Setenv("TLITH_API_KEY", "")
	t.Setenv("TLC_ALLOW_INSECURE", "")
}

// setupEnv makes the run hermetic with the connection given via environment.
func setupEnv(t *testing.T, baseURL string) {
	t.Helper()
	isolate(t)
	t.Setenv("TLITH_BASE_URL", baseURL)
	t.Setenv("TLITH_API_KEY", "sk-test-key")
}

// fastRetries sets the API retry schedule for the duration of the test; with
// no arguments it disables retries (a non-nil empty slice, since nil means
// "use the defaults").
func fastRetries(t *testing.T, delays ...time.Duration) {
	t.Helper()
	old := clientRetryDelays
	clientRetryDelays = append([]time.Duration{}, delays...)
	t.Cleanup(func() { clientRetryDelays = old })
}

func mustContain(t *testing.T, name, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s does not contain %q\n--- got ---\n%s", name, want, got)
	}
}

func mustNotContain(t *testing.T, name, got, bad string) {
	t.Helper()
	if strings.Contains(got, bad) {
		t.Fatalf("%s must not contain %q\n--- got ---\n%s", name, bad, got)
	}
}

// ------------------------------------------------------------------ the tests

func TestHelpAndVersion(t *testing.T) {
	for _, arg := range []string{"version", "-v", "--version"} {
		res := runTLC(t, t.TempDir(), arg)
		if res.code != 0 || strings.TrimSpace(res.stdout) != version.Version {
			t.Fatalf("%s: code=%d stdout=%q", arg, res.code, res.stdout)
		}
	}
	for _, arg := range []string{"help", "-h", "--help"} {
		res := runTLC(t, t.TempDir(), arg)
		if res.code != 0 || !strings.Contains(res.stdout, "Usage:") || res.stderr != "" {
			t.Fatalf("%s: code=%d stdout=%q stderr=%q", arg, res.code, res.stdout, res.stderr)
		}
	}
}

func TestNoArgumentsShowsHelp(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir())
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "Usage:")
	mustContain(t, "stdout", res.stdout, "tlc install")
	if srv.hits() != 0 {
		t.Fatal("showing help must not contact the server")
	}
}

func TestUnknownFlagExits2(t *testing.T) {
	res := runTLC(t, t.TempDir(), "--bogus")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "unknown flag")
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)

	// a typo gets a suggestion
	res := runTLC(t, t.TempDir(), "agnts")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, `unknown command "agnts"`)
	mustContain(t, "stderr", res.stderr, "did you mean `tlc agents`?")

	// an old-style prompt is no longer sent to a model — and says so
	res = runTLC(t, t.TempDir(), "fix the failing test")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "no longer includes a coding agent")

	if srv.hits() != 0 {
		t.Fatal("a usage error must not contact the server")
	}
}

func TestRemovedFlagsExplainThemselves(t *testing.T) {
	for _, argv := range [][]string{
		{"-p", "hello"}, {"--print"}, {"-c"}, {"--continue"}, {"--yolo"}, {"--verbose"},
		{"--agent", "sigma"}, {"--permission-mode=auto"}, {"--allowed-tools", "Bash"},
		{"--disallowed-tools=Write"}, {"--max-turns", "3"}, {"--output-format", "json"},
		{"agents", "-p"},
	} {
		res := runTLC(t, t.TempDir(), argv...)
		if res.code != 2 {
			t.Errorf("%v: code=%d, want 2", argv, res.code)
			continue
		}
		if !strings.Contains(res.stderr, "was removed") || !strings.Contains(res.stderr, "no longer includes a coding agent") {
			t.Errorf("%v: stderr = %q", argv, res.stderr)
		}
		if res.stdout != "" {
			t.Errorf("%v: stdout = %q", argv, res.stdout)
		}
	}
}

func TestFlagsWithoutACommand(t *testing.T) {
	res := runTLC(t, t.TempDir(), "--base-url", "https://turnlith.com")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "missing command")
}

func TestNotConfigured(t *testing.T) {
	isolate(t)
	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "not connected to a turnlith server")
	mustContain(t, "stderr", res.stderr, "tlc configure")
	if res.stdout != "" {
		t.Fatalf("errors belong on stderr, stdout = %q", res.stdout)
	}
}

func TestAuthFailureIsFriendly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid API key","code":"invalid_api_key"}}`)
	}))
	t.Cleanup(srv.Close)
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "authentication failed")
	mustContain(t, "stderr", res.stderr, "tlc configure")
}

func TestUnreachableServerIsFriendly(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	setupEnv(t, url)
	fastRetries(t) // no retries: fail at once

	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "cannot reach")
}

func TestRetriesAreAnnouncedOnStderr(t *testing.T) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]any{"agents": testAgents()})
	}))
	t.Cleanup(srv.Close)
	setupEnv(t, srv.URL)
	fastRetries(t, time.Millisecond)

	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "server unavailable — retrying")
	mustNotContain(t, "stdout", res.stdout, "retrying") // stdout stays data
	mustContain(t, "stdout", res.stdout, "sigma")
}

func TestAgentsCommandListsWorkspace(t *testing.T) {
	agents := testAgents()
	agents = append(agents, api.Agent{ID: "agent-2", Codename: "lil-coder", Label: strPtr("Lil Coder"), Model: "gemini:gemini-3.8-flash", ReasoningEffort: strPtr("high")})
	srv := newMockServer(t, agents)
	setupEnv(t, srv.URL)

	for _, argv := range [][]string{{"agents"}, {"agents", "list"}} {
		res := runTLC(t, t.TempDir(), argv...)
		if res.code != 0 {
			t.Fatalf("%v: code=%d stderr=%q", argv, res.code, res.stderr)
		}
		mustContain(t, "stdout", res.stdout, "CODENAME")
		mustContain(t, "stdout", res.stdout, "sigma")
		mustContain(t, "stdout", res.stdout, "Lil Coder")
		mustContain(t, "stdout", res.stdout, "gemini:gemini-3.8-flash · effort high")
	}
}

func TestAgentsCommandNeverPrintsServerControlCharacters(t *testing.T) {
	hostile := api.Agent{ID: "a", Codename: "evil", Label: strPtr("pwn\x1b]0;owned\x07\x1b[2J ed"), Model: "gemini:x\x1b[31m"}
	srv := newMockServer(t, []api.Agent{hostile})
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if strings.ContainsRune(res.stdout, 0x1b) || strings.ContainsRune(res.stdout, 0x07) {
		t.Fatalf("terminal control characters from the server reached the screen: %q", res.stdout)
	}
	mustContain(t, "stdout", res.stdout, "evil")
}

func TestAgentsCommandWithAnEmptyWorkspacePointsToCreate(t *testing.T) {
	srv := newMockServer(t, nil)
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	mustContain(t, "stderr", res.stderr, "tlc agents create")
}

func TestParseAgentsCreateSubcommands(t *testing.T) {
	opts, err := parseArgs([]string{"agents", "create", "--name", "tlc-test", "--model", "openai:gpt-6-sol", "--effort", "high", "--label", "TLC test"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.command != "agents-create" {
		t.Fatalf("command = %q", opts.command)
	}
	if opts.name != "tlc-test" || opts.label != "TLC test" || opts.model != "openai:gpt-6-sol" || opts.effort != "high" {
		t.Fatalf("flags = %+v", opts)
	}

	opts, err = parseArgs([]string{"agents", "new", "--model=x"})
	if err != nil || opts.command != "agents-create" || opts.model != "x" {
		t.Fatalf("agents new: opts=%+v err=%v", opts, err)
	}

	opts, err = parseArgs([]string{"agents", "list"})
	if err != nil || opts.command != "agents" {
		t.Fatalf("agents list: opts=%+v err=%v", opts, err)
	}

	if _, err := parseArgs([]string{"agents", "bogus"}); err == nil {
		t.Fatal("unknown subcommand was accepted")
	}
	if _, err := parseArgs([]string{"agents", "create", "make", "it", "good"}); err == nil {
		t.Fatal("a stray word was accepted by agents create")
	}
}

func TestAgentsCreatePostsFlags(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create",
		"--name", "tlc-test", "--label", "TLC test",
		"--model", "openai:gpt-6-sol", "--effort", "high")
	if res.code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "created agent tlc-test")
	mustContain(t, "stdout", res.stdout, "openai:gpt-6-sol · effort high")
	mustContain(t, "stdout", res.stdout, "tlc install")

	created := srv.createdAgents()
	if len(created) != 1 {
		t.Fatalf("create calls = %d", len(created))
	}
	in := created[0]
	if in.Codename != "tlc-test" || in.Label != "TLC test" || in.Model != "openai:gpt-6-sol" || in.ReasoningEffort != "high" {
		t.Fatalf("create body = %+v", in)
	}
}

func TestAgentsCreateGeneratesCodename(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create", "--model", "gemini:gemini-3.8-flash")
	if res.code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "created agent auto-1")
	if created := srv.createdAgents(); len(created) != 1 || created[0].Codename != "" {
		t.Fatalf("create bodies = %+v", created)
	}
}

func TestAgentsCreateAcceptsDefaultEffortSpelling(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir(), "agents", "create", "--model", "groq:llama-4-scout", "--effort", "default")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if created := srv.createdAgents(); len(created) != 1 || created[0].ReasoningEffort != "" {
		t.Fatalf("create bodies = %+v", created)
	}
}

func TestAgentsCreateNeedsModelOffTTY(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create")
	if res.code != 1 {
		t.Fatalf("code=%d stdout=%q", res.code, res.stdout)
	}
	mustContain(t, "stderr", res.stderr, "--model is required")
	if len(srv.createdAgents()) != 0 {
		t.Fatal("a create was attempted without a model")
	}
}

func TestAgentsCreateRejectsBadInputBeforeThePost(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)

	for name, tc := range map[string]struct {
		argv []string
		want string
	}{
		"bad codename": {[]string{"--name", "Not-Allowed", "--model", "gemini:gemini-3.8-flash"}, "not usable"},
		// groq has no reasoning control: asking for a level must fail with the provider named
		"effort on a knobless provider": {[]string{"--model", "groq:llama-4-scout", "--effort", "high"}, "no reasoning control"},
		"effort the provider rejects":   {[]string{"--model", "gemini:gemini-3.8-flash", "--effort", "xhigh"}, "supported: minimal, low, medium, high"},
		"unavailable model":             {[]string{"--model", "openai:gpt-6-luna"}, "not available in this workspace"},
	} {
		res := runTLC(t, t.TempDir(), append([]string{"agents", "create"}, tc.argv...)...)
		if res.code != 1 {
			t.Errorf("%s: code=%d", name, res.code)
		}
		if !strings.Contains(res.stderr, tc.want) {
			t.Errorf("%s: stderr = %q, want %q", name, res.stderr, tc.want)
		}
	}
	if len(srv.createdAgents()) != 0 {
		t.Fatal("invalid input still reached the server")
	}
}

func TestAgentsCreateExplainsAgentRoleKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			writeJSON(w, map[string]any{"data": []any{}, "agents": testAgents(), "providers": testProviders(), "models": testCatalog()})
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"message":"admin role required","code":"forbidden"}}`)
	}))
	t.Cleanup(srv.Close)
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create", "--model", "gemini:gemini-3.8-flash")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	mustContain(t, "stderr", res.stderr, "admin-role key")
}

// ---------------------------------------------------------------- configure

func savedConnection(t *testing.T) config.Connection {
	t.Helper()
	conn, err := config.LoadConnection()
	if err != nil {
		t.Fatalf("no saved connection: %v", err)
	}
	return conn
}

func TestConfigureVerifiesThenSavesFromStdin(t *testing.T) {
	srv := newMockServer(t, testAgents())
	isolate(t)
	withStdin(t, "sk-from-stdin\n")

	res := runTLC(t, t.TempDir(), "configure", "--base-url", srv.URL, "--api-key-stdin")
	if res.code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "connected to "+srv.URL)
	mustContain(t, "stdout", res.stdout, "1 agent(s) available")
	mustContain(t, "stdout", res.stdout, "tlc install")
	mustNotContain(t, "output", res.stdout+res.stderr, "sk-from-stdin")

	if conn := savedConnection(t); conn.BaseURL != srv.URL || conn.APIKey != "sk-from-stdin" {
		t.Fatalf("saved = %+v", conn)
	}
	if info, err := os.Stat(config.ConnectionPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v %v", info, err)
	}
}

func TestConfigureSendsTheKeyItWasGiven(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			gotAuth = r.Header.Get("authorization")
			writeJSON(w, map[string]any{"agents": []any{}})
			return
		}
		writeJSON(w, map[string]any{"status": "ok"})
	}))
	t.Cleanup(srv.Close)
	isolate(t)

	res := runTLC(t, t.TempDir(), "configure", "--base-url", srv.URL, "--api-key", "sk-flag")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if gotAuth != "Bearer sk-flag" {
		t.Fatalf("auth = %q", gotAuth)
	}
	mustContain(t, "stderr", res.stderr, "--api-key is visible")
	mustContain(t, "stdout", res.stdout, "no agents exist in this workspace yet")
}

func TestConfigureDoesNotSaveARejectedKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			writeJSON(w, map[string]any{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid API key"}}`)
	}))
	t.Cleanup(srv.Close)
	isolate(t)
	withStdin(t, "sk-wrong\n")

	res := runTLC(t, t.TempDir(), "configure", "--base-url", srv.URL, "--api-key-stdin")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "key check failed")
	if _, err := os.Stat(config.ConnectionPath()); err == nil {
		t.Fatal("a rejected key was saved")
	}
}

func TestConfigureDoesNotSaveWhenTheServerIsDown(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	isolate(t)
	fastRetries(t)
	withStdin(t, "sk-x\n")

	res := runTLC(t, t.TempDir(), "configure", "--base-url", url, "--api-key-stdin")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "health check failed")
	if _, err := os.Stat(config.ConnectionPath()); err == nil {
		t.Fatal("a connection that was never verified was saved")
	}
}

func TestConfigureRefusesPlainHTTPToARemoteHost(t *testing.T) {
	isolate(t)
	withStdin(t, "sk-x\n")
	res := runTLC(t, t.TempDir(), "configure", "--base-url", "http://turnlith.example.com", "--api-key-stdin")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "plain http")
	if _, err := os.Stat(config.ConnectionPath()); err == nil {
		t.Fatal("an unsafe URL was saved")
	}
}

func TestConfigureNeedsInputWhenThereIsNoTerminal(t *testing.T) {
	isolate(t)
	// no key and nobody to ask: say so instead of failing silently
	res := runTLC(t, t.TempDir(), "configure", "--base-url", "https://turnlith.com")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "--api-key-stdin")

	withStdin(t, "\n")
	res = runTLC(t, t.TempDir(), "configure", "--base-url", "https://turnlith.com", "--api-key-stdin")
	if res.code != 2 {
		t.Fatalf("empty stdin: code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "stdin was empty")
}

// ----------------------------------------------------------------- install

func TestInstallRefusesPlainHTTPToARemoteHost(t *testing.T) {
	srv := newMockServer(t, testAgents())
	setupEnv(t, srv.URL)
	t.Setenv("PATH", t.TempDir())
	home := os.Getenv("HOME")

	// the saved connection is fine, but --base-url must be held to the same rule
	res := runTLC(t, t.TempDir(), "install", "cursor", "--base-url", "http://turnlith.example.com")
	if res.code != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "plain http")
	if exists(home + "/.cursor/mcp.json") {
		t.Fatal("the api key was written for an unsafe server URL")
	}
}

func TestInstallErrorsGoToStderr(t *testing.T) {
	isolate(t)
	t.Setenv("PATH", t.TempDir())
	res := runTLC(t, t.TempDir(), "install", "cursor")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	mustContain(t, "stderr", res.stderr, "not connected")
	if res.stdout != "" {
		t.Fatalf("stdout = %q", res.stdout)
	}
}
