package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func providerBlock(agents []AgentModel, useEnv bool) ProviderBlock {
	if agents == nil {
		agents = []AgentModel{
			{Codename: "deploy-helper", Label: "deploy"},
			{Codename: "writer"},
		}
	}
	return BuildProviderBlock(ProviderInput{
		BaseURL: "http://localhost:8080",
		APIKey:  "sk-test-key",
		UseEnv:  useEnv,
		Agents:  agents,
	})
}

func mcpBlock(useEnv bool) McpBlock {
	return BuildMcpBlock(MCPInput{
		BaseURL: "http://localhost:8080",
		APIKey:  "sk-test-key",
		UseEnv:  useEnv,
	})
}

// parseJSONC standardizes JSONC into plain JSON and unmarshals it, so tests
// can assert semantics on documents that keep comments.
func parseJSONC(t *testing.T, text []byte) map[string]any {
	t.Helper()
	std, err := hujson.Standardize(text)
	if err != nil {
		t.Fatalf("config does not parse: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(std, &doc); err != nil {
		t.Fatalf("standardized config does not unmarshal: %v", err)
	}
	return doc
}

// blockAt digs out doc[section][key] as re-marshaled JSON, for deep-equality
// against the Go block (json.Marshal sorts map keys, so both sides order the
// same).
func blockAt(t *testing.T, doc map[string]any, section, key string) string {
	t.Helper()
	sec, ok := doc[section].(map[string]any)
	if !ok {
		t.Fatalf("no %q object in %v", section, doc)
	}
	got, err := json.Marshal(sec[key])
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func wantJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	// round-trip so struct field order and map order normalize the same way
	var generic any
	if err := json.Unmarshal(b, &generic); err != nil {
		t.Fatal(err)
	}
	norm, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	return string(norm)
}

func TestUpsertProviderFreshConfig(t *testing.T) {
	text, err := UpsertProviderText(nil, "tlith", providerBlock(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	// a fresh file is plain JSON (no comments) with the schema first
	var doc map[string]any
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatalf("fresh config is not valid JSON: %v\n%s", err, text)
	}
	if doc["$schema"] != opencodeSchema {
		t.Errorf("$schema = %v, want %q", doc["$schema"], opencodeSchema)
	}
	if got, want := blockAt(t, doc, "provider", "tlith"), wantJSON(t, providerBlock(nil, false)); got != want {
		t.Errorf("provider.tlith = %s, want %s", got, want)
	}
}

func TestProviderBlockModelNames(t *testing.T) {
	doc := parseJSONC(t, mustUpsert(t, nil, "tlith"))
	models := doc["provider"].(map[string]any)["tlith"].(map[string]any)["models"].(map[string]any)
	if name := models["deploy-helper"].(map[string]any)["name"]; name != "deploy" {
		t.Errorf("deploy-helper name = %v, want the label %q", name, "deploy")
	}
	if name := models["writer"].(map[string]any)["name"]; name != "writer" {
		t.Errorf("writer name = %v, want the codename fallback %q", name, "writer")
	}
}

const existingWithComments = `{
  // my providers
  "provider": {
    "openai": { "npm": "@ai-sdk/openai", "models": { "gpt-4o": { "name": "GPT-4o" } } },
    "tlith": { "npm": "stale", "name": "Old block", "options": {}, "models": {} }
  },
  "model": "openai/gpt-4o"
}`

func TestUpsertProviderPreserves(t *testing.T) {
	merged := mustUpsert(t, []byte(existingWithComments), "tlith")
	if !strings.Contains(string(merged), "// my providers") {
		t.Error("the user's comment did not survive")
	}
	if !strings.Contains(string(merged), `"gpt-4o"`) {
		t.Error("the other provider's models did not survive")
	}
	if !bytes.Contains(merged, []byte(`"openai": { "npm": "@ai-sdk/openai", "models": { "gpt-4o": { "name": "GPT-4o" } } }`)) {
		t.Error("the other provider's block was not byte-preserved")
	}
	doc := parseJSONC(t, merged)
	provider := doc["provider"].(map[string]any)
	if got := provider["openai"].(map[string]any)["npm"]; got != "@ai-sdk/openai" {
		t.Errorf("openai.npm = %v", got)
	}
	ours := provider["tlith"].(map[string]any)
	if baseURL := ours["options"].(map[string]any)["baseURL"]; baseURL != "http://localhost:8080/v1" {
		t.Errorf("tlith.options.baseURL = %v", baseURL)
	}
	if name := ours["models"].(map[string]any)["deploy-helper"].(map[string]any)["name"]; name != "deploy" {
		t.Errorf("tlith.models.deploy-helper.name = %v", name)
	}
	if doc["model"] != "openai/gpt-4o" {
		t.Errorf("model = %v", doc["model"])
	}
}

func TestUpsertProviderIdempotent(t *testing.T) {
	existing, _ := json.MarshalIndent(map[string]any{
		"provider": map[string]any{
			"openai": map[string]any{"npm": "@ai-sdk/openai"},
			"tlith":  map[string]any{"npm": "stale"},
		},
	}, "", "  ")
	once := mustUpsert(t, existing, "tlith")
	twice := mustUpsert(t, once, "tlith")
	if !bytes.Equal(once, twice) {
		t.Errorf("re-merge not idempotent:\n%s\n---\n%s", once, twice)
	}
	// same on a commented config
	onceC := mustUpsert(t, []byte(existingWithComments), "tlith")
	if !bytes.Equal(onceC, mustUpsert(t, onceC, "tlith")) {
		t.Error("re-merge on a commented config not idempotent")
	}
}

func TestUpsertProviderUseEnv(t *testing.T) {
	text, err := UpsertProviderText(nil, "tlith", providerBlock(nil, true))
	if err != nil {
		t.Fatal(err)
	}
	doc := parseJSONC(t, text)
	got := doc["provider"].(map[string]any)["tlith"].(map[string]any)["options"].(map[string]any)["apiKey"]
	if got != "{env:TLITH_API_KEY}" {
		t.Errorf("options.apiKey = %v, want the env reference", got)
	}
}

func TestUpsertRefusesBrokenConfig(t *testing.T) {
	for _, up := range []struct {
		name string
		fn   func() error
	}{
		{"provider", func() error {
			_, err := UpsertProviderText([]byte("{ not json"), "tlith", providerBlock(nil, false))
			return err
		}},
		{"mcp", func() error {
			_, err := UpsertMcpText([]byte("{ not json"), DefaultMCPServerID, mcpBlock(false))
			return err
		}},
	} {
		err := up.fn()
		if err == nil || !strings.Contains(err.Error(), "JSON syntax errors") {
			t.Errorf("%s: error = %v, want a JSON syntax errors refusal", up.name, err)
		}
	}
}

func TestRemoveProvider(t *testing.T) {
	existing, _ := json.MarshalIndent(map[string]any{
		"provider": map[string]any{
			"openai": map[string]any{"npm": "@ai-sdk/openai"},
			"tlith":  map[string]any{"npm": "@ai-sdk/openai-compatible"},
		},
		"model": "openai/gpt-4o",
	}, "", "  ")
	removed, err := RemoveProviderText(existing, "tlith")
	if err != nil {
		t.Fatal(err)
	}
	if HasProviderBlock(removed, "tlith") {
		t.Error("tlith block still present")
	}
	if !HasProviderBlock(removed, "openai") {
		t.Error("openai block was removed too")
	}
	if doc := parseJSONC(t, removed); doc["model"] != "openai/gpt-4o" {
		t.Errorf("model = %v", doc["model"])
	}
}

func TestRemoveProviderDropsEmptiedParent(t *testing.T) {
	solo, _ := json.MarshalIndent(map[string]any{
		"provider": map[string]any{"tlith": map[string]any{"npm": "x"}},
		"theme":    "dark",
	}, "", "  ")
	removed, err := RemoveProviderText(solo, "tlith")
	if err != nil {
		t.Fatal(err)
	}
	if doc := parseJSONC(t, removed); !reflect.DeepEqual(doc, map[string]any{"theme": "dark"}) {
		t.Errorf("doc = %v, want just the theme", doc)
	}
}

func TestRemoveAbsentIsNoop(t *testing.T) {
	for _, existing := range [][]byte{
		[]byte(`{"theme": "dark"}`),
		[]byte(`{"provider": {"openai": {"npm": "x"}}}`),
	} {
		removed, err := RemoveProviderText(existing, "tlith")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(removed, existing) {
			t.Errorf("removing an absent block changed the text:\n%s", removed)
		}
	}
}

func TestOpencodeConfigPath(t *testing.T) {
	if got := OpencodeConfigPath(true); got != "opencode.json" {
		t.Errorf("project path = %q", got)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := OpencodeConfigPath(false), filepath.Join(home, ".config", "opencode", "opencode.json"); got != want {
		t.Errorf("home fallback = %q, want %q", got, want)
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := OpencodeConfigPath(false), filepath.Join(xdg, "opencode", "opencode.json"); got != want {
		t.Errorf("xdg path = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	if got, want := OpencodeConfigPath(false), filepath.Join(home, ".config", "opencode", "opencode.json"); got != want {
		t.Errorf("relative XDG_CONFIG_HOME must be ignored: %q, want %q", got, want)
	}
}

func TestBuildMcpBlock(t *testing.T) {
	block := mcpBlock(false)
	want := McpBlock{
		Type:    "remote",
		URL:     "http://localhost:8080/v1/mcp",
		Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer sk-test-key"},
		OAuth:   false,
	}
	if !reflect.DeepEqual(block, want) {
		t.Errorf("block = %+v, want %+v", block, want)
	}
	if auth := mcpBlock(true).Headers["Authorization"]; auth != "Bearer {env:TLITH_API_KEY}" {
		t.Errorf("env Authorization = %q", auth)
	}
}

func TestUpsertMcpFreshAndBesideProvider(t *testing.T) {
	fresh, err := UpsertMcpText(nil, DefaultMCPServerID, mcpBlock(false))
	if err != nil {
		t.Fatal(err)
	}
	doc := parseJSONC(t, fresh)
	if got, want := blockAt(t, doc, "mcp", DefaultMCPServerID), wantJSON(t, mcpBlock(false)); got != want {
		t.Errorf("fresh mcp block = %s, want %s", got, want)
	}

	withMcp, err := UpsertMcpText(mustUpsert(t, nil, "tlith"), DefaultMCPServerID, mcpBlock(false))
	if err != nil {
		t.Fatal(err)
	}
	// a third-party server merges beside ours without either being touched
	withOther, err := UpsertMcpText(withMcp, "context7", McpBlock{Type: "remote", URL: "https://mcp.context7.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	doc = parseJSONC(t, withOther)
	if got, want := blockAt(t, doc, "provider", "tlith"), wantJSON(t, providerBlock(nil, false)); got != want {
		t.Errorf("provider drifted after mcp merges: %s", got)
	}
	if got, want := blockAt(t, doc, "mcp", DefaultMCPServerID), wantJSON(t, mcpBlock(false)); got != want {
		t.Errorf("mcp block drifted after the context7 merge: %s", got)
	}
	if url := doc["mcp"].(map[string]any)["context7"].(map[string]any)["url"]; url != "https://mcp.context7.com/mcp" {
		t.Errorf("context7.url = %v", url)
	}
}

func TestUpsertMcpIdempotent(t *testing.T) {
	once, err := UpsertMcpText(nil, DefaultMCPServerID, mcpBlock(false))
	if err != nil {
		t.Fatal(err)
	}
	twice, err := UpsertMcpText(once, DefaultMCPServerID, mcpBlock(false))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("re-merge not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestRemoveMcp(t *testing.T) {
	withTwo, err := UpsertMcpText(nil, DefaultMCPServerID, mcpBlock(false))
	if err != nil {
		t.Fatal(err)
	}
	withTwo, err = UpsertMcpText(withTwo, "context7", McpBlock{Type: "remote", URL: "https://mcp.context7.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveMcpText(withTwo, DefaultMCPServerID)
	if err != nil {
		t.Fatal(err)
	}
	if HasMcpBlock(removed, DefaultMCPServerID) {
		t.Error("our server block still present")
	}
	if !HasMcpBlock(removed, "context7") {
		t.Error("the other server was removed too")
	}

	solo, err := UpsertMcpText(nil, DefaultMCPServerID, mcpBlock(false))
	if err != nil {
		t.Fatal(err)
	}
	removed, err = RemoveMcpText(solo, DefaultMCPServerID)
	if err != nil {
		t.Fatal(err)
	}
	if doc := parseJSONC(t, removed); !reflect.DeepEqual(doc, map[string]any{"$schema": opencodeSchema}) {
		t.Errorf("doc = %v, want just the schema", doc)
	}
}

// ------------------------------------------------------ install / uninstall

// projectScope chdirs into a fresh temp dir, where ./opencode.json lives.
func projectScope(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func installOpts() InstallOptions {
	return InstallOptions{
		Project:    true,
		ProviderID: DefaultProviderID,
		Block:      providerBlock([]AgentModel{{Codename: "deploy-helper", Label: "deploy"}}, false),
	}
}

func TestInstallLifecycle(t *testing.T) {
	projectScope(t)

	first, err := InstallOpencode(installOpts())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Path != "opencode.json" || first.AgentCount != 1 {
		t.Errorf("first install = %+v", first)
	}
	afterFirst, err := os.ReadFile("opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat("opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("created config mode = %o, want 600", perm)
	}

	second, err := InstallOpencode(installOpts())
	if err != nil {
		t.Fatal(err)
	}
	if second.Created {
		t.Error("second install reported create")
	}
	if _, err := InstallOpencode(installOpts()); err != nil {
		t.Fatal(err)
	}
	afterThird, err := os.ReadFile("opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirst, afterThird) {
		t.Error("re-install is not byte-stable")
	}

	if outcome, err := UninstallOpencode(UninstallOptions{Project: true, ProviderID: DefaultProviderID}); err != nil || outcome != OutcomeRemoved {
		t.Errorf("uninstall = %v, %v", outcome, err)
	}
	if outcome, err := UninstallOpencode(UninstallOptions{Project: true, ProviderID: DefaultProviderID}); err != nil || outcome != OutcomeNotInstalled {
		t.Errorf("second uninstall = %v, %v", outcome, err)
	}
}

func TestInstallMergesExistingConfig(t *testing.T) {
	projectScope(t)
	existing, _ := json.MarshalIndent(map[string]any{
		"provider": map[string]any{"openai": map[string]any{"npm": "@ai-sdk/openai"}},
	}, "", "  ")
	if err := os.WriteFile("opencode.json", append(existing, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallOpencode(installOpts()); err != nil {
		t.Fatal(err)
	}
	doc := parseJSONC(t, readFile(t, "opencode.json"))
	provider := doc["provider"].(map[string]any)
	if provider["openai"].(map[string]any)["npm"] != "@ai-sdk/openai" {
		t.Error("the other provider was clobbered")
	}
	if provider["tlith"].(map[string]any)["npm"] != "@ai-sdk/openai-compatible" {
		t.Error("our provider block missing")
	}
	// the merged file now holds the api key: others lose read access
	if runtime.GOOS != "windows" {
		if info, err := os.Stat("opencode.json"); err == nil && info.Mode().Perm() != 0o600 {
			t.Errorf("existing config mode = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestInstallPrefersExistingJsonc(t *testing.T) {
	projectScope(t)
	existing, _ := json.MarshalIndent(map[string]any{"theme": "dark"}, "", "  ")
	if err := os.WriteFile("opencode.jsonc", append(existing, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallOpencode(installOpts())
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "opencode.jsonc" {
		t.Errorf("target = %q, want the existing opencode.jsonc", result.Path)
	}
	if _, err := os.Stat("opencode.json"); !os.IsNotExist(err) {
		t.Error("a competing opencode.json was created")
	}
	doc := parseJSONC(t, readFile(t, "opencode.jsonc"))
	if doc["theme"] != "dark" {
		t.Errorf("theme = %v", doc["theme"])
	}
	if _, ok := doc["provider"].(map[string]any)["tlith"]; !ok {
		t.Error("our provider block missing")
	}
}

func TestUninstallNoConfig(t *testing.T) {
	projectScope(t)
	outcome, err := UninstallOpencode(UninstallOptions{Project: true, ProviderID: DefaultProviderID})
	if err != nil || outcome != OutcomeNoConfig {
		t.Errorf("uninstall = %v, %v, want no-config", outcome, err)
	}
}

func mustUpsert(t *testing.T, existing []byte, providerID string) []byte {
	t.Helper()
	text, err := UpsertProviderText(existing, providerID, providerBlock(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
