package cli

import (
	"bytes"
	"os"
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
	if code := Main([]string{"install", "cursor"}); code == 0 {
		t.Error("installing an unknown harness should fail")
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
