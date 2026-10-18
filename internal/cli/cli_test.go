package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
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

type mcpCall struct {
	Name string
	Args map[string]any
}

// mockServer stands in for a turnlith server: /v1/models, /health, the
// streamed chat facade, and the stateless MCP endpoint.
type mockServer struct {
	*httptest.Server

	agents  []api.Agent
	chatFn  func(turn int, body api.ChatRequest) []string
	mcpText string

	mu       sync.Mutex
	turns    int
	chats    []api.ChatRequest
	methods  []string
	mcpCalls []mcpCall
}

func newMockServer(t *testing.T, agents []api.Agent, chatFn func(turn int, body api.ChatRequest) []string) *mockServer {
	t.Helper()
	m := &mockServer{agents: agents, chatFn: chatFn}

	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			writeJSON(w, map[string]any{"status": "ok"})
		case "/v1/models":
			data := make([]map[string]string, 0, len(m.agents))
			for _, a := range m.agents {
				data = append(data, map[string]string{"id": a.Codename, "object": "model"})
			}
			writeJSON(w, map[string]any{"data": data, "agents": m.agents})
		case "/v1/chat/completions":
			var body api.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
				return
			}
			m.mu.Lock()
			turn := m.turns
			m.turns++
			m.chats = append(m.chats, body)
			m.mu.Unlock()

			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			for _, payload := range m.chatFn(turn, body) {
				_, _ = io.WriteString(w, "data: "+payload+"\n\n")
				flusher.Flush()
			}
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
		case "/v1/mcp":
			m.handleMCP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.Close)
	return m
}

func (m *mockServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	var rpc struct {
		ID     int             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	m.methods = append(m.methods, rpc.Method)
	m.mu.Unlock()

	if rpc.Method == "initialize" {
		writeJSON(w, map[string]any{
			"jsonrpc": "2.0", "id": rpc.ID,
			"result": map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "turnlith-controller"},
			},
		})
		return
	}

	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	_ = json.Unmarshal(rpc.Params, &call)
	m.mu.Lock()
	m.mcpCalls = append(m.mcpCalls, mcpCall{Name: call.Name, Args: call.Arguments})
	text := m.mcpText
	m.mu.Unlock()

	writeJSON(w, map[string]any{
		"jsonrpc": "2.0", "id": rpc.ID,
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": false,
		},
	})
}

func (m *mockServer) requests() []api.ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]api.ChatRequest(nil), m.chats...)
}

func (m *mockServer) mcpMethods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.methods...)
}

func (m *mockServer) toolsCalled() []mcpCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]mcpCall(nil), m.mcpCalls...)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------- SSE chunks

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func textChunk(t *testing.T, text string) string {
	return mustJSON(t, map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"content": text},
		}},
	})
}

func toolChunk(t *testing.T, id, name, args string) string {
	return mustJSON(t, map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index":    0,
				"id":       id,
				"type":     "function",
				"function": map[string]any{"name": name, "arguments": args},
			}}},
		}},
	})
}

func finalChunk(t *testing.T, text string, promptTokens, completionTokens int) string {
	return mustJSON(t, map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         map[string]any{"content": text},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	})
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

// setupEnv makes the run hermetic: connection via environment, config,
// preferences, and transcripts under temp dirs, no lingering agent choice.
// HOME is redirected too, because macOS resolves ~/.config from it directly.
func setupEnv(t *testing.T, baseURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", baseURL)
	t.Setenv("TLITH_API_KEY", "sk-test-key")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("TLC_AGENT", "")
}

func mustContain(t *testing.T, name, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s does not contain %q\n--- got ---\n%s", name, want, got)
	}
}

func lastMessage(t *testing.T, req api.ChatRequest) api.Message {
	t.Helper()
	if len(req.Messages) == 0 {
		t.Fatal("request carried no messages")
	}
	return req.Messages[len(req.Messages)-1]
}

// ------------------------------------------------------------------ the tests

func TestHelpAndVersion(t *testing.T) {
	res := runTLC(t, t.TempDir(), "version")
	if res.code != 0 || strings.TrimSpace(res.stdout) != version.Version {
		t.Fatalf("version: code=%d stdout=%q", res.code, res.stdout)
	}

	res = runTLC(t, t.TempDir(), "help")
	if res.code != 0 || !strings.Contains(res.stdout, "Usage:") {
		t.Fatalf("help: code=%d stdout=%q", res.code, res.stdout)
	}
}

func TestUnknownFlagExits2(t *testing.T) {
	res := runTLC(t, t.TempDir(), "--bogus")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "unknown flag")
}

func TestNotConfigured(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TLITH_BASE_URL", "")
	t.Setenv("TLITH_API_KEY", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	res := runTLC(t, t.TempDir(), "-p", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "not connected to a turnlith server")
}

func TestAuthFailureIsFriendly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Invalid API key","code":"invalid_api_key"}}`)
	}))
	t.Cleanup(srv.Close)
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "authentication failed")
}

func TestPrintModeStreamsAssistantText(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{
			textChunk(t, "Looking at "),
			textChunk(t, "the project."),
			finalChunk(t, " All good.", 1234, 12),
		}
	})
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "hi there")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "Looking at the project. All good.")

	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 chat request, got %d", len(reqs))
	}
	req := reqs[0]
	if req.Model != "sigma" {
		t.Errorf("model = %q, want sigma", req.Model)
	}
	if !req.Stream {
		t.Error("stream flag not set")
	}
	if got := lastMessage(t, req); got.Role != "user" || got.Content != "hi there" {
		t.Errorf("last message = %+v", got)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" || req.Messages[0].Content == "" {
		t.Errorf("first message should be a non-empty system prompt: %+v", req.Messages[0])
	}
	names := map[string]bool{}
	for _, def := range req.Tools {
		names[def.Function.Name] = true
	}
	for _, want := range []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep", "SearchMemory"} {
		if !names[want] {
			t.Errorf("tool %s not offered to the model", want)
		}
	}
}

func TestMultiAgentRequiresChoice(t *testing.T) {
	agents := testAgents()
	agents = append(agents, api.Agent{ID: "agent-2", Codename: "lil-coder", Label: nil, Model: "gemini:gemini-3.8-flash"})
	srv := newMockServer(t, agents, func(turn int, body api.ChatRequest) []string {
		return []string{finalChunk(t, "hello", 10, 2)}
	})
	setupEnv(t, srv.URL)

	dir := t.TempDir()
	res := runTLC(t, dir, "-p", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "choose one with --agent")
	if strings.Contains(res.stdout, "choose one") {
		t.Fatalf("error text leaked into stdout: %q", res.stdout)
	}

	res = runTLC(t, dir, "-p", "--agent", "lil-coder", "hi")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	reqs := srv.requests()
	if len(reqs) != 1 || reqs[0].Model != "lil-coder" {
		t.Fatalf("requests = %+v", reqs)
	}
}

func TestAgentsCommandListsWorkspace(t *testing.T) {
	agents := testAgents()
	agents = append(agents, api.Agent{ID: "agent-2", Codename: "lil-coder", Label: strPtr("Lil Coder"), Model: "gemini:gemini-3.8-flash"})
	srv := newMockServer(t, agents, func(turn int, body api.ChatRequest) []string { return nil })
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "CODENAME")
	mustContain(t, "stdout", res.stdout, "sigma")
	mustContain(t, "stdout", res.stdout, "Lil Coder")
	mustContain(t, "stdout", res.stdout, "gemini:gemini-3.8-flash")
}

func TestPrintModeReadToolRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/notes.txt", []byte("the-secret-marker\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{toolChunk(t, "call_1", "Read", `{"file_path":"notes.txt"}`)}
		}
		return []string{finalChunk(t, "It says: the-secret-marker.", 900, 20)}
	})
	setupEnv(t, srv.URL)

	res := runTLC(t, dir, "-p", "what is in notes.txt?")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "⏺ Read")
	mustContain(t, "stdout", res.stdout, "It says: the-secret-marker.")
	if strings.Contains(res.stdout, "⏺") {
		t.Fatalf("tool block leaked into stdout: %q", res.stdout)
	}

	reqs := srv.requests()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 chat requests (tool round trip), got %d", len(reqs))
	}
	replay := reqs[1].Messages
	if len(replay) != 4 {
		t.Fatalf("replayed history = %d messages, want 4: %+v", len(replay), replay)
	}
	if a := replay[2]; a.Role != "assistant" || len(a.ToolCalls) != 1 || a.ToolCalls[0].Function.Name != "Read" {
		t.Errorf("assistant tool-call round not replayed: %+v", a)
	}
	if tr := replay[3]; tr.Role != "tool" || tr.ToolCallID != "call_1" {
		t.Errorf("tool result round not replayed: %+v", tr)
	} else {
		mustContain(t, "tool result", tr.Content.(string), "the-secret-marker")
	}
}

func TestPrintModeDeniesGatedTool(t *testing.T) {
	dir := t.TempDir()
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{toolChunk(t, "call_b", "Bash", `{"command":"echo ran > pwned.txt"}`)}
		}
		return []string{finalChunk(t, "Okay, I will not run that.", 500, 10)}
	})
	setupEnv(t, srv.URL)

	res := runTLC(t, dir, "-p", "run a command")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if _, err := os.Stat(dir + "/pwned.txt"); err == nil {
		t.Fatal("denied Bash command was executed")
	}
	mustContain(t, "stderr", res.stderr, "denied")
	mustContain(t, "stdout", res.stdout, "Okay, I will not run that.")

	reqs := srv.requests()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 chat requests, got %d", len(reqs))
	}
	tool := lastMessage(t, reqs[1])
	mustContain(t, "denial result", tool.Content.(string), "denied permission")
}

func TestPrintModeYoloRunsGatedTool(t *testing.T) {
	dir := t.TempDir()
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{toolChunk(t, "call_b", "Bash", `{"command":"echo hi > ran.txt"}`)}
		}
		return []string{finalChunk(t, "Done.", 500, 10)}
	})
	setupEnv(t, srv.URL)

	res := runTLC(t, dir, "-p", "run a command", "--yolo")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	data, err := os.ReadFile(dir + "/ran.txt")
	if err != nil {
		t.Fatalf("yolo Bash did not run: %v", err)
	}
	if strings.TrimSpace(string(data)) != "hi" {
		t.Fatalf("ran.txt = %q", data)
	}
	if strings.Contains(res.stderr, "denied") {
		t.Fatalf("tool was denied despite --yolo: %q", res.stderr)
	}
}

func TestSearchMemoryToolUsesMCP(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{toolChunk(t, "call_m", "SearchMemory", `{"query":"deploy steps","scope":"memory"}`)}
		}
		return []string{finalChunk(t, "From memory: make release.", 700, 15)}
	})
	srv.mcpText = "Deploy: run make release from main."
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "how do we deploy?")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "From memory: make release.")

	methods := srv.mcpMethods()
	if len(methods) < 2 || methods[0] != "initialize" || methods[1] != "tools/call" {
		t.Fatalf("MCP methods = %v, want initialize then tools/call", methods)
	}
	calls := srv.toolsCalled()
	if len(calls) != 1 {
		t.Fatalf("MCP tools/call count = %d", len(calls))
	}
	if calls[0].Name != "search_memory" {
		t.Errorf("MCP tool name = %q", calls[0].Name)
	}
	if got := calls[0].Args["agent"]; got != "sigma" {
		t.Errorf("search_memory agent = %v, want sigma", got)
	}
	if got := calls[0].Args["scope"]; got != "memory" {
		t.Errorf("search_memory scope = %v, want memory", got)
	}

	reqs := srv.requests()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 chat requests, got %d", len(reqs))
	}
	tool := lastMessage(t, reqs[1])
	mustContain(t, "memory result", tool.Content.(string), "[agent sigma]")
	mustContain(t, "memory result", tool.Content.(string), "make release")
}
