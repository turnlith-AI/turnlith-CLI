package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
)

// bashOnceServer has the model ask for one Bash command, then finish.
func bashOnceServer(t *testing.T, command string) *mockServer {
	t.Helper()
	return newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{toolChunk(t, "call_b", "Bash", mustJSON(t, map[string]string{"command": command}))}
		}
		return []string{finalChunk(t, "finished", 100, 5)}
	})
}

func toolResult(t *testing.T, srv *mockServer) string {
	t.Helper()
	reqs := srv.requests()
	if len(reqs) < 2 {
		t.Fatalf("expected a tool round trip, got %d request(s)", len(reqs))
	}
	return lastMessage(t, reqs[1]).Content.(string)
}

func TestAllowedToolsFlagRunsMatchingCommandsWithoutAsking(t *testing.T) {
	dir := t.TempDir()
	srv := bashOnceServer(t, "echo allowed > marker.txt")
	setupEnv(t, srv.URL)
	// redirection is never covered by a prefix rule...
	res := runTLC(t, dir, "-p", "--allowed-tools", "Bash(echo:*)", "go")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "marker.txt")); err == nil {
		t.Fatal("a command with redirection ran under a prefix rule")
	}
	mustContain(t, "tool result", toolResult(t, srv), "denied permission")

	// ...but a plain command is
	dir2 := t.TempDir()
	srv2 := bashOnceServer(t, "echo allowed")
	setupEnv(t, srv2.URL)
	res = runTLC(t, dir2, "-p", "--allowed-tools", "Bash(echo:*)", "go")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "tool result", toolResult(t, srv2), "allowed")
}

func TestDisallowedToolsBeatYolo(t *testing.T) {
	dir := t.TempDir()
	srv := bashOnceServer(t, "echo should-not-run > pwned.txt")
	setupEnv(t, srv.URL)
	res := runTLC(t, dir, "-p", "--yolo", "--disallowed-tools", "Bash(echo:*)", "go")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned.txt")); err == nil {
		t.Fatal("deny rule did not beat --yolo")
	}
	mustContain(t, "tool result", toolResult(t, srv), "denied permission")
}

func TestPermissionModeFlag(t *testing.T) {
	dir := t.TempDir()
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{toolChunk(t, "w", "Write", `{"file_path":"made.txt","content":"hi\n"}`)}
		}
		return []string{finalChunk(t, "done", 10, 1)}
	})
	setupEnv(t, srv.URL)
	res := runTLC(t, dir, "-p", "--permission-mode", "auto", "write it")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "made.txt")); err != nil || string(data) != "hi\n" {
		t.Fatalf("auto mode should allow Write: %v %q", err, data)
	}

	res = runTLC(t, t.TempDir(), "-p", "--permission-mode", "reckless", "x")
	if res.code != 2 {
		t.Fatalf("bad mode: code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "--permission-mode")
}

func TestInvalidRuleFlagIsAUsageError(t *testing.T) {
	srv := bashOnceServer(t, "echo hi")
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir(), "-p", "--allowed-tools", "Nonsense(x)", "go")
	if res.code != 2 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "invalid permission rule")
}

func TestProjectSettingsCanOnlyDeny(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".tlc"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"permissions":{"deny":["Bash(echo:*)"],"allow":["Bash"]}}`
	if err := os.WriteFile(filepath.Join(dir, ".tlc", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := bashOnceServer(t, "echo hello")
	setupEnv(t, srv.URL)

	res := runTLC(t, dir, "-p", "--yolo", "go")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "tool result", toolResult(t, srv), "denied permission") // the repo's deny rule holds, even in yolo
	mustContain(t, "stderr", res.stderr, "ignored 1 allow rule")           // its allow rule was refused
}

func TestSavedProjectAllowRulesApply(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := bashOnceServer(t, "echo remembered")
	setupEnv(t, srv.URL)
	if err := config.AddProjectAllow(dir, "Bash(echo:*)"); err != nil {
		t.Fatal(err)
	}
	res := runTLC(t, dir, "-p", "go")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "tool result", toolResult(t, srv), "remembered")

	// a different project does not inherit it
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv2 := bashOnceServer(t, "echo remembered")
	t.Setenv("TLITH_BASE_URL", srv2.URL)
	res = runTLC(t, other, "-p", "go")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "tool result", toolResult(t, srv2), "denied permission")
}

func TestProjectRuleStorePersistsForTheProject(t *testing.T) {
	setupEnv(t, "http://127.0.0.1:1")
	root := t.TempDir()
	store := projectRuleStore{root: root}
	rule, _ := parseRule(t, "Bash(make:*)")
	if err := store.SaveAllow(rule); err != nil {
		t.Fatal(err)
	}
	if got := config.LoadPrefs().ProjectAllow(root); len(got) != 1 || got[0] != "Bash(make:*)" {
		t.Fatalf("saved = %v", got)
	}
}

func TestMaxTurnsStopsARunawayRunWithAFailingExit(t *testing.T) {
	dir := t.TempDir()
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{toolChunk(t, "r"+string(rune('a'+turn)), "Glob", `{"pattern":"*"}`)} // never finishes
	})
	setupEnv(t, srv.URL)
	res := runTLC(t, dir, "-p", "--max-turns", "3", "loop forever")
	if res.code != 1 {
		t.Fatalf("code=%d, want 1; stderr=%q", res.code, res.stderr)
	}
	mustContain(t, "stderr", res.stderr, "3-step")
	if n := len(srv.requests()); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
}

func TestStatusShowsRules(t *testing.T) {
	a, out := testApp(t, "http://127.0.0.1:1", testAgents())
	a.cwd = t.TempDir()
	rule, _ := parseRule(t, "Bash(go test:*)")
	a.policy.AddAllow(rule)
	deny, _ := parseRule(t, "Bash(git push:*)")
	a.policy.AddDeny(deny)
	a.handleSlash("/status")
	mustContain(t, "status", out.String(), "Bash(go test:*)")
	mustContain(t, "status", out.String(), "Bash(git push:*)")
	if strings.Contains(out.String(), "sk-test-key") {
		t.Fatal("status must mask the api key")
	}
}

func parseRule(t *testing.T, s string) (permissions.Rule, error) {
	t.Helper()
	r, err := permissions.ParseRule(s)
	if err != nil {
		t.Fatal(err)
	}
	return r, nil
}
