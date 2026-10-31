package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox points every config location at fresh temp dirs: a home (HOME,
// XDG_CONFIG_HOME, APPDATA all inside it) and, as the working directory, a
// separate project dir. It returns the home.
func sandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	projectScope(t)
	return home
}

func testRequest() Request {
	return Request{
		ProviderID:  DefaultProviderID,
		MCPServerID: DefaultMCPServerID,
		BaseURL:     "http://localhost:8080",
		APIKey:      "sk-test-key",
		WithMCP:     true,
		Agents: []AgentModel{
			{Codename: "deploy-helper", Label: "deploy"},
			{Codename: "writer"},
		},
	}
}

func mustClient(t *testing.T, name string) Client {
	t.Helper()
	c, ok := Lookup(name)
	if !ok {
		t.Fatalf("no client %q", name)
	}
	return c
}

func mustInstall(t *testing.T, c Client, req Request) Result {
	t.Helper()
	res, err := c.Install(req)
	if err != nil {
		t.Fatalf("%s install: %v", c.ID, err)
	}
	return res
}

func uninstallReq(req Request) UninstallRequest {
	return UninstallRequest{Project: req.Project, ProviderID: req.ProviderID, MCPServerID: req.MCPServerID}
}

func TestRegistryLookup(t *testing.T) {
	if got, want := strings.Join(Names(), ","), "opencode,pi,continue,cursor,cline"; got != want {
		t.Errorf("Names() = %s, want %s", got, want)
	}
	for in, want := range map[string]string{
		"opencode": "opencode", "OpenCode": "opencode", "pi": "pi", "pi-agent": "pi",
		"continue": "continue", "continue.dev": "continue", "cursor": "cursor", " cline ": "cline",
	} {
		c, ok := Lookup(in)
		if !ok || c.ID != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", in, c.ID, ok, want)
		}
	}
	if _, ok := Lookup("zed"); ok {
		t.Error("Lookup(zed) should fail")
	}
}

func TestRegistryCapabilitiesAreConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Registry() {
		if seen[c.ID] {
			t.Errorf("duplicate client id %q", c.ID)
		}
		seen[c.ID] = true
		if !c.Caps.Provider && !c.Caps.MCP {
			t.Errorf("%s writes nothing", c.ID)
		}
		if c.Caps.UseEnv != (c.EnvRef != "") {
			t.Errorf("%s: UseEnv=%v but EnvRef=%q", c.ID, c.Caps.UseEnv, c.EnvRef)
		}
		if c.Caps.ProviderID && !c.Caps.Provider {
			t.Errorf("%s: --provider-id without a provider", c.ID)
		}
		if !c.Caps.Provider && c.ManualSteps == nil {
			t.Errorf("%s has no provider, so it needs manual steps", c.ID)
		}
		if c.Detect == nil || c.Install == nil || c.Uninstall == nil || c.Inspect == nil || c.Hint == "" {
			t.Errorf("%s: incomplete registry entry", c.ID)
		}
	}
}

// opencode's output through the registry must equal the direct functions.
func TestOpencodeThroughRegistryMatchesDirectInstall(t *testing.T) {
	sandbox(t)
	req := testRequest()
	req.Project = true
	mustInstall(t, mustClient(t, "opencode"), req)
	viaRegistry := readFile(t, "opencode.json")

	if err := os.Remove("opencode.json"); err != nil {
		t.Fatal(err)
	}
	mcp := BuildMcpBlock(MCPInput{BaseURL: req.BaseURL, APIKey: req.APIKey})
	if _, err := InstallOpencode(InstallOptions{
		Project:     true,
		ProviderID:  req.ProviderID,
		Block:       BuildProviderBlock(ProviderInput{BaseURL: req.BaseURL, APIKey: req.APIKey, Agents: req.Agents}),
		MCPServerID: req.MCPServerID,
		MCPBlock:    &mcp,
	}); err != nil {
		t.Fatal(err)
	}
	if direct := readFile(t, "opencode.json"); string(direct) != string(viaRegistry) {
		t.Errorf("registry install differs from InstallOpencode:\n%s\nvs\n%s", viaRegistry, direct)
	}
}

// ---------------------------------------------------------------- Cursor

func TestCursorInstallLifecycle(t *testing.T) {
	home := sandbox(t)
	c := mustClient(t, "cursor")
	req := testRequest()

	res := mustInstall(t, c, req)
	path := filepath.Join(home, ".cursor", "mcp.json")
	if len(res.Files) != 1 || res.Files[0].Path != path || !res.Files[0].Created {
		t.Fatalf("result = %+v, want one created file at %s", res.Files, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	doc := parseJSONC(t, readFile(t, path))
	if _, hasSchema := doc["$schema"]; hasSchema {
		t.Error("Cursor's mcp.json has no $schema")
	}
	want := wantJSON(t, CursorMcpBlock{
		URL:     "http://localhost:8080/v1/mcp",
		Headers: map[string]string{"Authorization": "Bearer sk-test-key"},
	})
	if got := blockAt(t, doc, "mcpServers", DefaultMCPServerID); got != want {
		t.Errorf("mcpServers.turnlith-controller = %s, want %s", got, want)
	}

	first := readFile(t, path)
	mustInstall(t, c, req)
	if again := readFile(t, path); string(again) != string(first) {
		t.Error("reinstall changed the file")
	}

	state := c.Inspect(false, req.ProviderID, req.MCPServerID)
	if len(state) != 1 || !state[0].MCP || !state[0].Exists {
		t.Errorf("Inspect = %+v, want the MCP entry present", state)
	}
	if out, err := c.Uninstall(uninstallReq(req)); err != nil || out != OutcomeRemoved {
		t.Fatalf("uninstall = %v, %v", out, err)
	}
	if doc := parseJSONC(t, readFile(t, path)); doc["mcpServers"] != nil {
		t.Errorf("uninstall left the emptied mcpServers behind: %v", doc)
	}
	if out, _ := c.Uninstall(uninstallReq(req)); out != OutcomeNotInstalled {
		t.Errorf("second uninstall = %v, want not-installed", out)
	}
}

func TestCursorMergesAndKeepsOtherServers(t *testing.T) {
	home := sandbox(t)
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "{\n    // my servers\n    \"mcpServers\": {\n        \"other\": {\"url\": \"https://example.com/mcp\"}\n    }\n}\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	c := mustClient(t, "cursor")
	req := testRequest()
	if res := mustInstall(t, c, req); res.Files[0].Created {
		t.Error("an existing file should be reported as updated")
	}
	got := string(readFile(t, path))
	if !strings.Contains(got, "// my servers") || !strings.Contains(got, `"other"`) {
		t.Errorf("install dropped the user's comment or server:\n%s", got)
	}
	if !strings.Contains(got, `"turnlith-controller"`) {
		t.Errorf("install did not add the server:\n%s", got)
	}
	if _, err := c.Uninstall(uninstallReq(req)); err != nil {
		t.Fatal(err)
	}
	after := string(readFile(t, path))
	if strings.Contains(after, "turnlith-controller") || !strings.Contains(after, `"other"`) || !strings.Contains(after, "// my servers") {
		t.Errorf("uninstall should remove only our server:\n%s", after)
	}
}

func TestCursorProjectAndEnv(t *testing.T) {
	sandbox(t)
	c := mustClient(t, "cursor")
	req := testRequest()
	req.Project, req.UseEnv = true, true
	mustInstall(t, c, req)
	data := string(readFile(t, filepath.Join(".cursor", "mcp.json")))
	if strings.Contains(data, "sk-test-key") || !strings.Contains(data, "Bearer ${env:TLITH_API_KEY}") {
		t.Errorf("--use-env should write the env reference, not the key:\n%s", data)
	}
}

func TestCursorRefusesBrokenConfigAndNoMCP(t *testing.T) {
	home := sandbox(t)
	c := mustClient(t, "cursor")
	req := testRequest()
	req.WithMCP = false
	if _, err := c.Install(req); err == nil {
		t.Error("Cursor with --no-mcp has nothing to install")
	}
	path := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	req.WithMCP = true
	_, err := c.Install(req)
	if err == nil || !strings.Contains(err.Error(), "Cursor") || !strings.Contains(err.Error(), "JSON syntax errors") {
		t.Errorf("error = %v, want a Cursor JSON syntax refusal", err)
	}
	if got := string(readFile(t, path)); got != `{"mcpServers": {` {
		t.Errorf("a broken file must be left untouched, got %q", got)
	}
}

// ----------------------------------------------------------------- Cline

// fakeClineInstall creates the extension's global-storage dir for an editor.
func fakeClineInstall(t *testing.T, editor string) string {
	t.Helper()
	dir := filepath.Join(userDataDir(), editor, "User", "globalStorage", clineExtensionDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "settings", "cline_mcp_settings.json")
}

func TestClineWritesEveryInstall(t *testing.T) {
	sandbox(t)
	code := fakeClineInstall(t, "Code")
	vscodium := fakeClineInstall(t, "VSCodium")
	c := mustClient(t, "cline")
	if !c.Detect() {
		t.Fatal("Cline should be detected once its storage dir exists")
	}
	req := testRequest()
	res := mustInstall(t, c, req)
	if len(res.Files) != 2 {
		t.Fatalf("files = %+v, want both editors", res.Files)
	}
	want := wantJSON(t, ClineMcpBlock{
		Type:    "streamableHttp",
		URL:     "http://localhost:8080/v1/mcp",
		Headers: map[string]string{"Authorization": "Bearer sk-test-key"},
	})
	for _, path := range []string{code, vscodium} {
		if got := blockAt(t, parseJSONC(t, readFile(t, path)), "mcpServers", DefaultMCPServerID); got != want {
			t.Errorf("%s: entry = %s, want %s", path, got, want)
		}
	}
	if out, err := c.Uninstall(uninstallReq(req)); err != nil || out != OutcomeRemoved {
		t.Fatalf("uninstall = %v, %v", out, err)
	}
	for _, path := range []string{code, vscodium} {
		if strings.Contains(string(readFile(t, path)), "turnlith-controller") {
			t.Errorf("%s still has our server", path)
		}
	}
}

func TestClineNotInstalled(t *testing.T) {
	sandbox(t)
	c := mustClient(t, "cline")
	if c.Detect() {
		t.Error("Cline should not be detected in an empty home")
	}
	_, err := c.Install(testRequest())
	if err == nil || !strings.Contains(err.Error(), "Cline") {
		t.Errorf("error = %v, want a hint to open Cline once", err)
	}
	if out, err := c.Uninstall(uninstallReq(testRequest())); err != nil || out != OutcomeNoConfig {
		t.Errorf("uninstall = %v, %v; want no-config", out, err)
	}
	if c.Caps.UseEnv || c.Caps.Project || c.Caps.Provider {
		t.Errorf("Cline capabilities = %+v; it has no env interpolation, project scope or provider file", c.Caps)
	}
}

// ------------------------------------------------------------------- Pi

func TestPiInstallLifecycle(t *testing.T) {
	home := sandbox(t)
	c := mustClient(t, "pi")
	req := testRequest()

	res := mustInstall(t, c, req)
	path := filepath.Join(home, ".pi", "agent", "models.json")
	if len(res.Files) != 1 || res.Files[0].Path != path || res.AgentCount != 2 {
		t.Fatalf("result = %+v, want one file at %s with 2 agents", res, path)
	}
	doc := parseJSONC(t, readFile(t, path))
	want := wantJSON(t, PiProviderBlock{
		BaseURL: "http://localhost:8080/v1",
		API:     "openai-completions",
		APIKey:  "sk-test-key",
		Models: []PiModel{
			{ID: "deploy-helper", Name: "deploy", ContextWindow: 12000, MaxTokens: 8192},
			{ID: "writer", Name: "writer", ContextWindow: 12000, MaxTokens: 8192},
		},
	})
	if got := blockAt(t, doc, "providers", "tlith"); got != want {
		t.Errorf("providers.tlith = %s, want %s", got, want)
	}

	first := readFile(t, path)
	mustInstall(t, c, req)
	if again := readFile(t, path); string(again) != string(first) {
		t.Error("reinstall changed the file")
	}

	// a refreshed agent list replaces the models
	req.Agents = req.Agents[:1]
	mustInstall(t, c, req)
	models := parseJSONC(t, readFile(t, path))["providers"].(map[string]any)["tlith"].(map[string]any)["models"].([]any)
	if len(models) != 1 {
		t.Errorf("models = %v, want the refreshed single agent", models)
	}

	if out, err := c.Uninstall(uninstallReq(req)); err != nil || out != OutcomeRemoved {
		t.Fatalf("uninstall = %v, %v", out, err)
	}
	if doc := parseJSONC(t, readFile(t, path)); doc["providers"] != nil {
		t.Errorf("uninstall left providers behind: %v", doc)
	}
}

func TestPiKeepsOtherProvidersAndHonoursAgentDir(t *testing.T) {
	sandbox(t)
	dir := filepath.Join(t.TempDir(), "pi-agent")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	path := filepath.Join(dir, "models.json")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "{\n  \"providers\": {\n    \"ollama\": {\"baseUrl\": \"http://localhost:11434/v1\"}\n  }\n}\n"
	if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	c := mustClient(t, "pi")
	if !c.Detect() {
		t.Error("Pi should be detected when its agent dir exists")
	}
	req := testRequest()
	req.UseEnv = true
	req.ProviderID = "turnlith"
	mustInstall(t, c, req)
	data := string(readFile(t, path))
	if !strings.Contains(data, `"ollama"`) || !strings.Contains(data, `"turnlith"`) {
		t.Errorf("both providers should be present:\n%s", data)
	}
	if strings.Contains(data, "sk-test-key") || !strings.Contains(data, `"$TLITH_API_KEY"`) {
		t.Errorf("--use-env should write the env reference:\n%s", data)
	}
	if _, err := c.Uninstall(uninstallReq(req)); err != nil {
		t.Fatal(err)
	}
	if after := string(readFile(t, path)); !strings.Contains(after, `"ollama"`) || strings.Contains(after, `"turnlith"`) {
		t.Errorf("uninstall should remove only our provider:\n%s", after)
	}
}

func TestPiHasNoMCP(t *testing.T) {
	c := mustClient(t, "pi")
	if c.Caps.MCP || c.Caps.Project {
		t.Errorf("Pi capabilities = %+v; it has no MCP and only a user-level config", c.Caps)
	}
	if lines := c.ManualSteps("http://x", nil); len(lines) == 0 {
		t.Error("Pi should explain that MCP tools are unavailable")
	}
}

// -------------------------------------------------------------- Continue

func TestContinueInstallLifecycle(t *testing.T) {
	home := sandbox(t)
	c := mustClient(t, "continue")
	req := testRequest()

	res := mustInstall(t, c, req)
	models := filepath.Join(home, ".continue", "models", "tlith.yaml")
	mcp := filepath.Join(home, ".continue", "mcpServers", "turnlith-controller.yaml")
	if len(res.Files) != 2 || res.Files[0].Path != models || res.Files[1].Path != mcp || res.AgentCount != 2 {
		t.Fatalf("result = %+v, want models + mcp files", res)
	}
	wantModels := continueMarker + `
name: "turnlith"
version: 0.0.1
schema: v1
models:
  - name: "deploy (turnlith)"
    provider: openai
    model: "deploy-helper"
    apiBase: "http://localhost:8080/v1"
    apiKey: "sk-test-key"
    roles:
      - chat
      - edit
      - apply
    capabilities:
      - tool_use
    defaultCompletionOptions:
      contextLength: 12000
      maxTokens: 8192
  - name: "writer (turnlith)"
    provider: openai
    model: "writer"
    apiBase: "http://localhost:8080/v1"
    apiKey: "sk-test-key"
    roles:
      - chat
      - edit
      - apply
    capabilities:
      - tool_use
    defaultCompletionOptions:
      contextLength: 12000
      maxTokens: 8192
`
	if got := string(readFile(t, models)); got != wantModels {
		t.Errorf("models yaml =\n%s\nwant\n%s", got, wantModels)
	}
	wantMCP := continueMarker + `
name: "turnlith-controller"
version: 0.0.1
schema: v1
mcpServers:
  - name: "turnlith-controller"
    type: streamable-http
    url: "http://localhost:8080/v1/mcp"
    requestOptions:
      headers:
        Authorization: "Bearer sk-test-key"
`
	if got := string(readFile(t, mcp)); got != wantMCP {
		t.Errorf("mcp yaml =\n%s\nwant\n%s", got, wantMCP)
	}

	if out, err := c.Uninstall(uninstallReq(req)); err != nil || out != OutcomeRemoved {
		t.Fatalf("uninstall = %v, %v", out, err)
	}
	for _, p := range []string{models, mcp} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be gone after uninstall", p)
		}
	}
	if out, _ := c.Uninstall(uninstallReq(req)); out != OutcomeNoConfig {
		t.Errorf("second uninstall = %v, want no-config", out)
	}
}

func TestContinueNeverTouchesFilesItDidNotWrite(t *testing.T) {
	home := sandbox(t)
	models := filepath.Join(home, ".continue", "models", "tlith.yaml")
	if err := os.MkdirAll(filepath.Dir(models), 0o755); err != nil {
		t.Fatal(err)
	}
	mine := "name: my own models\nschema: v1\n"
	if err := os.WriteFile(models, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	c := mustClient(t, "continue")
	req := testRequest()
	if _, err := c.Install(req); err == nil || !strings.Contains(err.Error(), "not written by tlc") {
		t.Errorf("error = %v, want a refusal to overwrite", err)
	}
	if out, err := c.Uninstall(uninstallReq(req)); err != nil || out != OutcomeNotInstalled {
		t.Errorf("uninstall = %v, %v; want not-installed", out, err)
	}
	if got := string(readFile(t, models)); got != mine {
		t.Errorf("the user's file was modified: %q", got)
	}
}

func TestContinueProjectEnvAndEscaping(t *testing.T) {
	sandbox(t)
	c := mustClient(t, "continue")
	req := testRequest()
	req.Project, req.UseEnv = true, true
	req.Agents = []AgentModel{{Codename: "a", Label: `say "hi" <b> & more`}}
	mustInstall(t, c, req)
	models := string(readFile(t, filepath.Join(".continue", "models", "tlith.yaml")))
	if strings.Contains(models, "sk-test-key") || !strings.Contains(models, `apiKey: "${{ secrets.TLITH_API_KEY }}"`) {
		t.Errorf("--use-env should write the secrets reference:\n%s", models)
	}
	if !strings.Contains(models, `name: "say \"hi\" <b> & more (turnlith)"`) {
		t.Errorf("labels must be quoted without HTML escaping:\n%s", models)
	}
}

func TestContinueHonoursGlobalDirAndSkipsModelsWithoutAgents(t *testing.T) {
	sandbox(t)
	dir := filepath.Join(t.TempDir(), "continue-home")
	t.Setenv("CONTINUE_GLOBAL_DIR", dir)
	c := mustClient(t, "continue")
	req := testRequest()
	req.Agents = nil
	res := mustInstall(t, c, req)
	if len(res.Files) != 1 || !strings.HasPrefix(res.Files[0].Path, dir) {
		t.Errorf("files = %+v, want only the MCP file under %s", res.Files, dir)
	}
}
