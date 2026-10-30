package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The harness package covers the JSONC merge semantics; these tests pin the
// command surface end to end: flags, connection, file writes, idempotence,
// uninstall.
func TestInstallAndUninstallOpencode(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	setupEnv(t, srv.URL)

	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if code := Main([]string{"install", "opencode", "--project"}); code != 0 {
		t.Fatalf("`tlc install opencode` exit = %d", code)
	}
	data := readOC(t)
	for _, want := range []string{
		`"tlith"`,
		`"@ai-sdk/openai-compatible"`,
		`"` + srv.URL + `/v1"`,
		`"sigma"`,
		`"Sigma"`,
		`"turnlith-controller"`,
		`"` + srv.URL + `/v1/mcp"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("opencode config missing %s:\n%s", want, data)
		}
	}
	if !strings.Contains(string(data), "sk-test-key") {
		t.Error("the api key was not embedded — by default install writes it into the config")
	}

	// re-running is byte-stable
	if code := Main([]string{"install", "opencode", "--project"}); code != 0 {
		t.Fatalf("second install exit = %d", code)
	}
	if again := readOC(t); !bytes.Equal(data, again) {
		t.Error("re-install changed the config")
	}

	if code := Main([]string{"uninstall", "opencode", "--project"}); code != 0 {
		t.Fatalf("`tlc uninstall opencode` exit = %d", code)
	}
	after := readOC(t)
	if strings.Contains(string(after), `"tlith"`) || strings.Contains(string(after), "turnlith-controller") {
		t.Errorf("uninstall left turnlith blocks behind:\n%s", after)
	}
	if code := Main([]string{"uninstall", "opencode", "--project"}); code != 0 {
		t.Errorf("uninstall with nothing installed should still exit 0, got %d", code)
	}
}

func TestInstallUseEnvOmitsKey(t *testing.T) {
	srv := newMockServer(t, testAgents(), nil)
	setupEnv(t, srv.URL)

	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	if code := Main([]string{"install", "opencode", "--project", "--use-env"}); code != 0 {
		t.Fatalf("install exit = %d", code)
	}
	data := string(readOC(t))
	if strings.Contains(data, "sk-test-key") {
		t.Error("--use-env still embedded the api key")
	}
	if !strings.Contains(data, `"Bearer {env:TLITH_API_KEY}"`) {
		t.Error("--use-env did not write the env reference into the MCP header")
	}
}

func TestInstallRejectsUnknownHarness(t *testing.T) {
	if code := Main([]string{"install", "zed"}); code == 0 {
		t.Error("installing an unknown client should fail")
	}
	if code := Main([]string{"install", "opencode", "extra-words"}); code == 0 {
		t.Error("a prompt after the harness name should fail")
	}
}

func readOC(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("opencode.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ------------------------------------------------------------ other clients

// clientEnv isolates every client's config location in a fresh home and
// hides any real client binaries from PATH, so detection depends only on what
// the test creates.
func clientEnv(t *testing.T) (srv *mockServer, home string) {
	t.Helper()
	srv = newMockServer(t, testAgents(), nil)
	setupEnv(t, srv.URL)
	home = os.Getenv("HOME")
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("PI_CODING_AGENT_DIR", "")
	t.Setenv("CONTINUE_GLOBAL_DIR", "")
	t.Setenv("PATH", t.TempDir())
	return srv, home
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestInstallSeveralClientsAtOnce(t *testing.T) {
	srv, home := clientEnv(t)
	res := runTLC(t, t.TempDir(), "install", "pi", "cursor", "continue")
	if res.code != 0 {
		t.Fatalf("exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	pi := mustRead(t, filepath.Join(home, ".pi", "agent", "models.json"))
	mustContain(t, "pi models.json", pi, `"baseUrl": "`+srv.URL+`/v1"`)
	mustContain(t, "pi models.json", pi, `"id": "sigma"`)
	mustContain(t, "cursor mcp.json", mustRead(t, filepath.Join(home, ".cursor", "mcp.json")), `"`+srv.URL+`/v1/mcp"`)
	mustContain(t, "continue models", mustRead(t, filepath.Join(home, ".continue", "models", "tlith.yaml")), `model: "sigma"`)
	mustContain(t, "continue mcp", mustRead(t, filepath.Join(home, ".continue", "mcpServers", "turnlith-controller.yaml")), "streamable-http")
	// several clients: each gets a header
	for _, name := range []string{"Pi", "Cursor", "Continue"} {
		mustContain(t, "stdout", res.stdout, name)
	}
}

func TestInstallAutoDetectsClients(t *testing.T) {
	_, home := clientEnv(t)
	for _, dir := range []string{filepath.Join(home, ".pi", "agent"), filepath.Join(home, ".continue")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	res := runTLC(t, t.TempDir(), "install")
	if res.code != 0 {
		t.Fatalf("exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	if !exists(filepath.Join(home, ".pi", "agent", "models.json")) || !exists(filepath.Join(home, ".continue", "models", "tlith.yaml")) {
		t.Error("detected clients were not wired")
	}
	if exists(filepath.Join(home, ".cursor", "mcp.json")) {
		t.Error("Cursor is not installed here and must be left alone")
	}
}

func TestInstallNothingDetected(t *testing.T) {
	srv, _ := clientEnv(t)
	res := runTLC(t, t.TempDir(), "install")
	if res.code != 2 {
		t.Errorf("exit = %d, want 2 when nothing is detected", res.code)
	}
	mustContain(t, "output", res.stdout+res.stderr, "tlc install <opencode|pi|continue|cursor|cline>")

	res = runTLC(t, t.TempDir(), "install", "--if-detected")
	if res.code != 0 || strings.TrimSpace(res.stdout+res.stderr) != "" {
		t.Errorf("--if-detected: exit=%d output=%q, want a silent success", res.code, res.stdout+res.stderr)
	}
	if srv.modelsHits != 0 {
		t.Error("nothing to install, so the server must not be contacted")
	}
}

func TestInstallMCPOnlyClientsSkipTheAgentList(t *testing.T) {
	srv, home := clientEnv(t)
	res := runTLC(t, t.TempDir(), "install", "cursor")
	if res.code != 0 {
		t.Fatalf("exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	if srv.modelsHits != 0 {
		t.Errorf("an MCP-only install listed the agents (%d requests)", srv.modelsHits)
	}
	mustContain(t, "stdout", res.stdout, "Settings → Models")
	mustContain(t, "stdout", res.stdout, srv.URL+"/v1")
	if !exists(filepath.Join(home, ".cursor", "mcp.json")) {
		t.Error("mcp.json was not written")
	}
}

func TestInstallFlagsAgainstClientCapabilities(t *testing.T) {
	_, home := clientEnv(t)
	for _, argv := range [][]string{
		{"install", "pi", "--project"},
		{"install", "cline", "--use-env"},
		{"install", "cursor", "--no-mcp"},
		{"uninstall", "pi", "--project"},
	} {
		if res := runTLC(t, t.TempDir(), argv...); res.code != 2 {
			t.Errorf("%v: exit = %d, want 2\n%s%s", argv, res.code, res.stdout, res.stderr)
		}
	}
	if exists(filepath.Join(home, ".pi")) || exists(filepath.Join(home, ".cursor")) {
		t.Error("a rejected install must not write anything")
	}

	// with several clients the unsupported one is skipped, the rest go ahead
	dir := t.TempDir()
	res := runTLC(t, dir, "install", "cursor", "pi", "--project")
	if res.code != 0 {
		t.Fatalf("mixed install exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	mustContain(t, "stdout", res.stdout, "skipping Pi")
	if !exists(filepath.Join(dir, ".cursor", "mcp.json")) {
		t.Error("Cursor's project config was not written")
	}
	if exists(filepath.Join(home, ".pi", "agent", "models.json")) {
		t.Error("--project must not fall back to Pi's global config")
	}
}

func TestInstallUseEnvPerClient(t *testing.T) {
	_, home := clientEnv(t)
	if res := runTLC(t, t.TempDir(), "install", "pi", "cursor", "continue", "--use-env"); res.code != 0 {
		t.Fatalf("exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	for path, want := range map[string]string{
		filepath.Join(home, ".pi", "agent", "models.json"):                         `"$TLITH_API_KEY"`,
		filepath.Join(home, ".cursor", "mcp.json"):                                 "Bearer ${env:TLITH_API_KEY}",
		filepath.Join(home, ".continue", "models", "tlith.yaml"):                   "${{ secrets.TLITH_API_KEY }}",
		filepath.Join(home, ".continue", "mcpServers", "turnlith-controller.yaml"): "${{ secrets.TLITH_API_KEY }}",
	} {
		data := mustRead(t, path)
		mustContain(t, path, data, want)
		if strings.Contains(data, "sk-test-key") {
			t.Errorf("%s embeds the key despite --use-env", path)
		}
	}
}

func TestInstallOneClientFailingDoesNotBlockTheRest(t *testing.T) {
	_, home := clientEnv(t)
	cursor := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(cursor), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cursor, []byte(`{"mcpServers": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runTLC(t, t.TempDir(), "install", "cursor", "pi")
	if res.code != 1 {
		t.Errorf("exit = %d, want 1 when one client fails", res.code)
	}
	mustContain(t, "output", res.stdout+res.stderr, "JSON syntax errors")
	if !exists(filepath.Join(home, ".pi", "agent", "models.json")) {
		t.Error("Pi should still be installed after Cursor failed")
	}
	if got := mustRead(t, cursor); got != `{"mcpServers": {` {
		t.Errorf("the broken file was modified: %q", got)
	}
}

func TestInstallList(t *testing.T) {
	srv, home := clientEnv(t)
	if res := runTLC(t, t.TempDir(), "install", "cursor"); res.code != 0 {
		t.Fatalf("setup install failed: %s%s", res.stdout, res.stderr)
	}
	hits := srv.modelsHits
	res := runTLC(t, t.TempDir(), "install", "--list")
	if res.code != 0 {
		t.Fatalf("exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	for _, want := range []string{"opencode", "pi", "continue", "cursor", "cline",
		filepath.Join(home, ".cursor", "mcp.json") + " — turnlith installed: mcp",
		filepath.Join(home, ".pi", "agent", "models.json") + " — missing",
		"no Cline install found"} {
		mustContain(t, "--list", res.stdout, want)
	}
	if srv.modelsHits != hits {
		t.Error("--list must work offline")
	}
	res = runTLC(t, t.TempDir(), "install", "--list", "--project")
	mustContain(t, "--list --project", res.stdout, "no project-level config")
}

func TestUninstallWithoutNameRemovesEveryInstalledClient(t *testing.T) {
	_, home := clientEnv(t)
	if res := runTLC(t, t.TempDir(), "install", "pi", "cursor", "continue"); res.code != 0 {
		t.Fatalf("setup install failed: %s%s", res.stdout, res.stderr)
	}
	res := runTLC(t, t.TempDir(), "uninstall")
	if res.code != 0 {
		t.Fatalf("exit = %d\n%s%s", res.code, res.stdout, res.stderr)
	}
	for _, path := range []string{
		filepath.Join(home, ".continue", "models", "tlith.yaml"),
		filepath.Join(home, ".continue", "mcpServers", "turnlith-controller.yaml"),
	} {
		if exists(path) {
			t.Errorf("%s survived uninstall", path)
		}
	}
	for _, path := range []string{filepath.Join(home, ".pi", "agent", "models.json"), filepath.Join(home, ".cursor", "mcp.json")} {
		if strings.Contains(mustRead(t, path), "tlith") || strings.Contains(mustRead(t, path), "turnlith-controller") {
			t.Errorf("%s still has turnlith entries", path)
		}
	}
	res = runTLC(t, t.TempDir(), "uninstall")
	if res.code != 0 {
		t.Errorf("second uninstall exit = %d", res.code)
	}
	mustContain(t, "second uninstall", res.stdout, "nothing to remove")
}
