package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo makes a git project with a nested working directory.
func repo(t *testing.T) (root, work string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	work = filepath.Join(root, "pkg", "sub")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, work
}

func TestInstructionsFromRepoRootWhenWorkingInASubdirectory(t *testing.T) {
	root, work := repo(t)
	put(t, filepath.Join(root, "AGENTS.md"), "Run make test.\n")
	got := LoadInstructions(work, 4096)
	if len(got) != 1 || got[0].Name != "AGENTS.md" || got[0].Text != "Run make test." {
		t.Fatalf("got %+v", got)
	}
}

func TestClosestDirectoryWinsAndNamesHavePrecedence(t *testing.T) {
	root, work := repo(t)
	put(t, filepath.Join(root, "TLC.md"), "root rules")
	put(t, filepath.Join(root, "pkg", "CLAUDE.md"), "pkg rules")
	got := LoadInstructions(work, 4096)
	if len(got) != 1 || got[0].Text != "pkg rules" {
		t.Fatalf("the nearest file should win: %+v", got)
	}

	put(t, filepath.Join(root, "pkg", "AGENTS.md"), "pkg agents")
	put(t, filepath.Join(root, "pkg", "TLC.md"), "pkg tlc")
	got = LoadInstructions(work, 4096)
	if len(got) != 1 || got[0].Name != "TLC.md" {
		t.Fatalf("TLC.md > AGENTS.md > CLAUDE.md in one directory: %+v", got)
	}
}

func TestSearchStopsAtTheRepositoryRoot(t *testing.T) {
	outer := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	put(t, filepath.Join(outer, "AGENTS.md"), "from a parent that is not this project")
	root := filepath.Join(outer, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LoadInstructions(root, 4096); len(got) != 0 {
		t.Fatalf("instructions above the repo root leaked in: %+v", got)
	}
}

func TestWithoutARepositoryOnlyTheWorkingDirectoryCounts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	parent := t.TempDir()
	put(t, filepath.Join(parent, "AGENTS.md"), "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LoadInstructions(child, 4096); len(got) != 0 {
		t.Fatalf("no repo: must not wander upward: %+v", got)
	}
	put(t, filepath.Join(child, "CLAUDE.md"), "child")
	if got := LoadInstructions(child, 4096); len(got) != 1 || got[0].Text != "child" {
		t.Fatalf("got %+v", got)
	}
}

func TestUserInstructionsComeFirst(t *testing.T) {
	root, work := repo(t)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	put(t, filepath.Join(cfg, "tlith", "TLC.md"), "always answer in haiku")
	put(t, filepath.Join(root, "AGENTS.md"), "project rule")
	got := LoadInstructions(work, 4096)
	if len(got) != 2 || got[0].Scope != "user" || got[1].Scope != "project" {
		t.Fatalf("got %+v", got)
	}
}

func TestInstructionsAreCappedAtALineBoundary(t *testing.T) {
	root, work := repo(t)
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("rule number " + strings.Repeat("x", 40) + "\n")
	}
	put(t, filepath.Join(root, "AGENTS.md"), b.String())
	got := LoadInstructions(work, 500)
	if len(got) != 1 || !got[0].Truncated {
		t.Fatalf("got %+v", got)
	}
	if len(got[0].Text) > 600 {
		t.Fatalf("not capped: %d bytes", len(got[0].Text))
	}
	body := strings.TrimSuffix(got[0].Text, "\n… (truncated)")
	if !strings.HasSuffix(body, strings.Repeat("x", 40)) {
		t.Fatalf("cut mid-line: %q", got[0].Text[len(got[0].Text)-80:])
	}
}

func TestEmptyBinaryAndDisabledInstructionsAreSkipped(t *testing.T) {
	root, work := repo(t)
	put(t, filepath.Join(root, "TLC.md"), "  \n\n")
	put(t, filepath.Join(root, "AGENTS.md"), "bin\x00ary")
	put(t, filepath.Join(root, "CLAUDE.md"), "the real one")
	got := LoadInstructions(work, 4096)
	if len(got) != 1 || got[0].Text != "the real one" {
		t.Fatalf("got %+v", got)
	}
	if got := LoadInstructions(work, 0); len(got) != 0 {
		t.Fatalf("max 0 disables loading: %+v", got)
	}
}

func TestSystemPromptCarriesInstructionsBelowTheSafetyRules(t *testing.T) {
	prompt := SystemPrompt(PromptEnv{
		Cwd: "/work",
		Instructions: []Instructions{
			{Scope: "user", Name: "TLC.md", Path: "/home/u/.config/tlith/TLC.md", Text: "be brief"},
			{Scope: "project", Name: "AGENTS.md", Path: "/work/AGENTS.md", Text: "use tabs"},
		},
	})
	for _, want := range []string{"be brief", "use tabs", "AGENTS.md", "never override"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
	if strings.Index(prompt, "Safety:") > strings.Index(prompt, "use tabs") {
		t.Error("repository instructions must come after the safety rules so they cannot be read as overriding them")
	}
	plain := SystemPrompt(PromptEnv{Cwd: "/work"})
	if strings.Contains(plain, "instructions") && strings.Contains(plain, "Project instructions") {
		t.Error("no instruction section without instruction files")
	}
}
