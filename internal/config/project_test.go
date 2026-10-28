package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestFindProjectRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(deep); got != canonical(root) {
		t.Fatalf("root = %q, want %q", got, canonical(root))
	}
	// a worktree/submodule has a .git *file*
	wt := t.TempDir()
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: elsewhere"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectRoot(wt); got != canonical(wt) {
		t.Fatalf("worktree root = %q", got)
	}
	// no repository: the directory itself, never a parent
	plain := t.TempDir()
	if got := FindProjectRoot(plain); got != canonical(plain) {
		t.Fatalf("plain dir = %q, want itself", got)
	}
}

func canonical(p string) string {
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

func TestProjectAllowRulesPersistPerProject(t *testing.T) {
	isolate(t)
	a, b := t.TempDir(), t.TempDir()

	if err := AddProjectAllow(a, "Bash(go test:*)"); err != nil {
		t.Fatal(err)
	}
	if err := AddProjectAllow(a, "Bash(go test:*)"); err != nil { // duplicate
		t.Fatal(err)
	}
	if err := AddProjectAllow(a, "Edit"); err != nil {
		t.Fatal(err)
	}
	if err := AddProjectAllow(b, "Bash(make:*)"); err != nil {
		t.Fatal(err)
	}
	if got, want := LoadPrefs().ProjectAllow(a), []string{"Bash(go test:*)", "Edit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a = %v, want %v", got, want)
	}
	if got, want := LoadPrefs().ProjectAllow(b), []string{"Bash(make:*)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("b = %v, want %v", got, want)
	}
	if got := LoadPrefs().ProjectAllow(t.TempDir()); len(got) != 0 {
		t.Fatalf("an unrelated project inherited rules: %v", got)
	}
}

func TestProjectAllowKeepsDefaultAgent(t *testing.T) {
	isolate(t)
	if err := SavePrefs(Prefs{DefaultAgent: "sigma"}); err != nil {
		t.Fatal(err)
	}
	if err := AddProjectAllow(t.TempDir(), "Edit"); err != nil {
		t.Fatal(err)
	}
	if LoadPrefs().DefaultAgent != "sigma" {
		t.Fatal("saving a rule dropped the default agent")
	}
}

func TestProjectAllowKeyIsStableThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	isolate(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := AddProjectAllow(link, "Edit"); err != nil {
		t.Fatal(err)
	}
	if got := LoadPrefs().ProjectAllow(real); len(got) != 1 {
		t.Fatalf("rule saved via the link is invisible via the real path: %v", got)
	}
}

func TestLoadProjectDenyHonorsOnlyDeny(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".tlc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"permissions":{"deny":["Bash(git push:*)","Edit(secrets/**)"],"allow":["Bash","Bash(curl:*)"]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	deny, ignored, err := LoadProjectDeny(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Bash(git push:*)", "Edit(secrets/**)"}; !reflect.DeepEqual(deny, want) {
		t.Fatalf("deny = %v", deny)
	}
	if ignored != 2 {
		t.Fatalf("ignored allow rules = %d, want 2 (a repo must not grant itself permissions)", ignored)
	}
}

func TestLoadProjectDenyMissingAndBroken(t *testing.T) {
	root := t.TempDir()
	if deny, ignored, err := LoadProjectDeny(root); err != nil || deny != nil || ignored != 0 {
		t.Fatalf("missing file: %v %d %v", deny, ignored, err)
	}
	dir := filepath.Join(root, ".tlc")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{not json"), 0o644)
	if _, _, err := LoadProjectDeny(root); err == nil {
		t.Fatal("invalid json should be reported")
	}
}
