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

	"github.com/turnlith-AI/turnlith-CLI/internal/agent"
	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
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
// streamed chat facade, the stateless MCP endpoint, and agent management.
type mockServer struct {
	*httptest.Server

	agents    []api.Agent
	providers []api.ProviderStatus
	catalog   []api.CatalogModel
	chatFn    func(turn int, body api.ChatRequest) []string
	mcpText   string
	// chatFail, when set, may answer a chat request with a plain HTTP error
	// (non-zero status plus JSON body) instead of a stream.
	chatFail func(turn int) (status int, body string)
	// omitDone ends each stream without the terminating [DONE] frame, like a
	// connection that was cut mid-turn.
	omitDone bool

	mu    sync.Mutex
	turns int
	// modelsHits counts GET /v1/models — `tlc install` lists the agents there.
	modelsHits int
	chats      []api.ChatRequest
	methods    []string
	mcpCalls   []mcpCall

	nextID  int
	creates []api.CreateAgentInput
	patches []map[string]any
}

// testProviders is the per-provider capability fixture: gemini and openai
// expose reasoning levels (minimal sets come from the server's defs.ts),
// groq has no reasoning control.
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

func newMockServer(t *testing.T, agents []api.Agent, chatFn func(turn int, body api.ChatRequest) []string) *mockServer {
	t.Helper()
	m := &mockServer{agents: agents, chatFn: chatFn, providers: testProviders(), catalog: testCatalog()}

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

			if m.chatFail != nil {
				if status, errBody := m.chatFail(turn); status != 0 {
					w.Header().Set("content-type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, errBody)
					return
				}
			}
			w.Header().Set("content-type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)
			for _, payload := range m.chatFn(turn, body) {
				_, _ = io.WriteString(w, "data: "+payload+"\n\n")
				flusher.Flush()
			}
			if !m.omitDone {
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
		case "/v1/mcp":
			m.handleMCP(w, r)
		default:
			if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/v1/agents/") {
				m.handleAgentPatch(w, r)
				return
			}
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
	agent := api.Agent{ID: "", Codename: input.Codename, Model: input.Model}
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

// handleAgentPatch answers PATCH /v1/agents/:id with the same merge semantics
// the server has: an explicit null effort clears it.
func (m *mockServer) handleAgentPatch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		http.Error(w, `{"error":{"message":"bad request"}}`, http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.patches = append(m.patches, patch)
	for i := range m.agents {
		if m.agents[i].ID != id {
			continue
		}
		if model, ok := patch["model"].(string); ok && model != "" {
			m.agents[i].Model = model
		}
		if raw, ok := patch["reasoningEffort"]; ok {
			if raw == nil {
				m.agents[i].ReasoningEffort = nil
			} else if level, ok := raw.(string); ok {
				m.agents[i].ReasoningEffort = &level
			}
		}
		updated := m.agents[i]
		writeJSON(w, updated)
		return
	}
	http.Error(w, `{"error":{"message":"agent not found","code":"model_not_found"}}`, http.StatusNotFound)
}

// createdAgents returns the POST /v1/agents bodies seen so far.
func (m *mockServer) createdAgents() []api.CreateAgentInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]api.CreateAgentInput(nil), m.creates...)
}

// agentPatches returns the PATCH /v1/agents/:id bodies seen so far.
func (m *mockServer) agentPatches() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any(nil), m.patches...)
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
	for _, want := range []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep", "SearchMemory", "TodoWrite"} {
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

// --------------------------------------------------------- agent management

// testApp wires the fields the management handlers touch, with output captured
// in a buffer instead of a terminal.
func testApp(t *testing.T, baseURL string, agents []api.Agent) (*app, *strings.Builder) {
	t.Helper()
	out := &strings.Builder{}
	theme := &ui.Theme{}
	a := &app{
		client:   api.New(baseURL, "sk-test-key"),
		agents:   agents,
		agent:    agents[0],
		theme:    theme,
		renderer: ui.NewRenderer(out, theme),
		runner:   &agent.Runner{},
	}
	a.policy = &permissions.Policy{}
	return a, out
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
		t.Fatal("positional prompt was accepted by agents create")
	}
}

func TestAgentsCreatePostsFlags(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create",
		"--name", "tlc-test", "--label", "TLC test",
		"--model", "openai:gpt-6-sol", "--effort", "high")
	if res.code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "created agent tlc-test")
	mustContain(t, "stdout", res.stdout, "openai:gpt-6-sol · effort high")
	mustContain(t, "stdout", res.stdout, "tlc --agent tlc-test")

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
	srv := newMockServer(t, testAgents(), nil)
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

func TestAgentsCreateNeedsModelOffTTY(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create")
	if res.code != 1 {
		t.Fatalf("code=%d stdout=%q", res.code, res.stdout)
	}
	mustContain(t, "stdout", res.stdout, "--model is required")
	if len(srv.createdAgents()) != 0 {
		t.Fatal("a create was attempted without a model")
	}
}

func TestAgentsCreateRejectsBadCodenameAndEffort(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "agents", "create", "--name", "Not-Allowed", "--model", "gemini:gemini-3.8-flash")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	mustContain(t, "stdout", res.stdout, "not usable")

	// groq has no reasoning control: asking for a level must fail before the
	// POST, with the provider named.
	res = runTLC(t, t.TempDir(), "agents", "create", "--model", "groq:llama-4-scout", "--effort", "high")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	mustContain(t, "stdout", res.stdout, "no reasoning control")
	if len(srv.createdAgents()) != 0 {
		t.Fatal("invalid effort still reached the server")
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
	mustContain(t, "stdout", res.stdout, "admin-role key")
}

func TestModelCommandPatchesAgentAndEffort(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	a, out := testApp(t, srv.URL, testAgents())

	if err := a.handleModel([]string{"/model", "openai:gpt-6-sol", "high"}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, "output", out.String(), "openai:gpt-6-sol · effort high")
	if a.agent.Model != "openai:gpt-6-sol" || currentEffort(a.agent) != "high" {
		t.Fatalf("local agent = %+v", a.agent)
	}
	patches := srv.agentPatches()
	if len(patches) != 1 {
		t.Fatalf("patches = %+v", patches)
	}
	if patches[0]["model"] != "openai:gpt-6-sol" || patches[0]["reasoningEffort"] != "high" {
		t.Fatalf("patch body = %+v", patches[0])
	}
}

func TestModelCommandClearsUnsupportedEffort(t *testing.T) {
	agents := testAgents()
	effort := "high"
	agents[0].ReasoningEffort = &effort
	srv := newMockServer(t, agents, nil)
	a, out := testApp(t, srv.URL, agents)

	// groq:llama-4-scout is reachable but has no reasoning knob: the stored
	// level must be dropped, not carried over.
	if err := a.handleModel([]string{"/model", "groq:llama-4-scout"}); err != nil {
		t.Fatal(err)
	}
	mustContain(t, "output", out.String(), "provider default")
	if currentEffort(a.agent) != "" {
		t.Fatalf("effort survived the switch: %+v", a.agent.ReasoningEffort)
	}
	patches := srv.agentPatches()
	if len(patches) != 1 {
		t.Fatalf("patches = %+v", patches)
	}
	raw, present := patches[0]["reasoningEffort"]
	if !present || raw != nil {
		t.Fatalf("patch should send an explicit null effort: %+v", patches[0])
	}
}

func TestModelCommandKeepsSupportedEffort(t *testing.T) {
	agents := testAgents()
	effort := "low"
	agents[0].ReasoningEffort = &effort
	srv := newMockServer(t, agents, nil)
	a, _ := testApp(t, srv.URL, agents)

	// gemini → openai keeps a level both providers accept.
	if err := a.handleModel([]string{"/model", "openai:gpt-6-sol"}); err != nil {
		t.Fatal(err)
	}
	patches := srv.agentPatches()
	if len(patches) != 1 || patches[0]["reasoningEffort"] != "low" {
		t.Fatalf("patches = %+v", patches)
	}
}

func TestModelCommandRejectsUnavailableModel(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	a, _ := testApp(t, srv.URL, testAgents())

	err := a.handleModel([]string{"/model", "openai:gpt-6-luna"})
	if err == nil {
		t.Fatal("an unavailable model was accepted")
	}
	mustContain(t, "error", err.Error(), "not available")
	if len(srv.agentPatches()) != 0 {
		t.Fatal("a patch was sent for an unavailable model")
	}
}

func TestEffortCommandSetsAndClears(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	a, _ := testApp(t, srv.URL, testAgents())

	if err := a.handleEffort([]string{"/effort", "medium"}); err != nil {
		t.Fatal(err)
	}
	if currentEffort(a.agent) != "medium" || a.agent.Model != "gemini:gemini-3.8-flash" {
		t.Fatalf("agent = %+v", a.agent)
	}

	if err := a.handleEffort([]string{"/effort", "default"}); err != nil {
		t.Fatal(err)
	}
	if currentEffort(a.agent) != "" {
		t.Fatalf("default did not clear the level: %+v", a.agent)
	}

	err := a.handleEffort([]string{"/effort", "xhigh"})
	if err == nil {
		t.Fatal("gemini accepted xhigh")
	}
	mustContain(t, "error", err.Error(), "supported: minimal, low, medium, high")

	patches := srv.agentPatches()
	if len(patches) != 2 {
		t.Fatalf("patches = %+v", patches)
	}
	if raw, present := patches[1]["reasoningEffort"]; !present || raw != nil {
		t.Fatalf("clearing should send an explicit null: %+v", patches[1])
	}
}

func TestEffortPickerDefaultIsExplicit(t *testing.T) {
	// the picker's "provider default" entry returns "" — it must be read as an
	// explicit reset, never as "no argument given" (which keeps the level)
	if got := pickEffortArg(""); got != "default" {
		t.Fatalf("picker default = %q, want %q", got, "default")
	}
	if got := pickEffortArg("high"); got != "high" {
		t.Fatalf("picker level = %q, want %q", got, "high")
	}
}

func TestEffortCommandRefusesKnoblessProvider(t *testing.T) {
	agents := testAgents()
	agents[0].Model = "groq:llama-4-scout"
	srv := newMockServer(t, agents, nil)
	a, _ := testApp(t, srv.URL, agents)

	err := a.handleEffort([]string{"/effort", "low"})
	if err == nil {
		t.Fatal("groq accepted an effort")
	}
	mustContain(t, "error", err.Error(), "no reasoning control")
}

func TestCompleteArgumentOffersWorkspaceWords(t *testing.T) {
	agents := testAgents()
	agents = append(agents, api.Agent{ID: "agent-2", Codename: "lil-coder", Model: "openai:gpt-6-sol"})
	a, _ := testApp(t, "http://127.0.0.1:1", agents)
	a.models = &api.ModelsResponse{Providers: testProviders(), Models: testCatalog()}

	got := a.completeArgument("/agent", "sig")
	if len(got) != 1 || got[0] != "sigma" {
		t.Fatalf("agent completion = %v", got)
	}
	got = a.completeArgument("/agent", "")
	if len(got) != 3 || got[0] != "new" {
		t.Fatalf("agent completion (empty) = %v", got)
	}
	// only available catalog entries complete
	got = a.completeArgument("/model", "openai:")
	if len(got) != 1 || got[0] != "openai:gpt-6-sol" {
		t.Fatalf("model completion = %v", got)
	}
	got = a.completeArgument("/model gemini:gemini-3.8-flash", "hi")
	if len(got) != 1 || got[0] != "high" {
		t.Fatalf("model effort completion = %v", got)
	}
	got = a.completeArgument("/effort", "min")
	if len(got) != 1 || got[0] != "minimal" {
		t.Fatalf("effort completion = %v", got)
	}
	got = a.completeArgument("/permissions", "")
	if len(got) != 4 || got[0] != "normal" || got[1] != "auto" || got[2] != "plan" || got[3] != "yolo" {
		t.Fatalf("permission mode completion = %v", got)
	}
	got = a.completeArgument("/permissions", "y")
	if len(got) != 1 || got[0] != "yolo" {
		t.Fatalf("permission mode completion (y) = %v", got)
	}
}

func TestPermissionsCommandSetsCyclesAndCovers(t *testing.T) {
	a, out := testApp(t, "http://127.0.0.1:1", testAgents())

	// no argument cycles normal → auto → plan → yolo → normal
	a.handleSlash("/permissions")
	if a.policy.Mode != permissions.ModeAuto {
		t.Fatalf("mode = %v, want auto", a.policy.Mode)
	}
	a.handleSlash("/permissions")
	if a.policy.Mode != permissions.ModePlan {
		t.Fatalf("mode = %v, want plan", a.policy.Mode)
	}
	a.handleSlash("/permissions")
	if a.policy.Mode != permissions.ModeYolo {
		t.Fatalf("mode = %v, want yolo", a.policy.Mode)
	}
	a.handleSlash("/permissions")
	if a.policy.Mode != permissions.ModeAsk {
		t.Fatalf("mode = %v, want normal", a.policy.Mode)
	}

	// explicit words and prefixes
	a.handleSlash("/permissions auto")
	if a.policy.Mode != permissions.ModeAuto {
		t.Fatalf("mode = %v, want auto", a.policy.Mode)
	}
	a.handleSlash("/permissions yolo")
	if a.policy.Mode != permissions.ModeYolo {
		t.Fatalf("mode = %v, want yolo", a.policy.Mode)
	}
	a.handleSlash("/permissions norm")
	if a.policy.Mode != permissions.ModeAsk {
		t.Fatalf("mode = %v, want normal", a.policy.Mode)
	}
	a.handleSlash("/permissions sideways")
	if a.policy.Mode != permissions.ModeAsk {
		t.Fatalf("an unknown mode must not change the policy")
	}
	mustContain(t, "unknown mode error", out.String(), "unknown permission mode")

	// /yolo toggles between normal and yolo
	a.handleSlash("/yolo")
	if a.policy.Mode != permissions.ModeYolo {
		t.Fatalf("mode = %v, want yolo", a.policy.Mode)
	}
	a.handleSlash("/yolo")
	if a.policy.Mode != permissions.ModeAsk {
		t.Fatalf("mode = %v, want normal", a.policy.Mode)
	}
}
