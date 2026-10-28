package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeHome points HOME (and the config dirs derived from it) at a temp dir
// holding credential-shaped files.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeFile(t, home, ".ssh/id_rsa", "PRIVATE KEY")
	writeFile(t, home, ".aws/credentials", "aws_secret_access_key=abc")
	writeFile(t, home, ".config/tlith/config.json", `{"apiKey":"sk-secret"}`)
	writeFile(t, home, "notes.txt", "harmless")
	return home
}

func TestFileToolsRefuseCredentialPaths(t *testing.T) {
	home := fakeHome(t)
	dir := t.TempDir()
	tracker := NewTracker()
	read := &ReadTool{Root: dir, Tracker: tracker}
	write := &WriteTool{Root: dir, Tracker: tracker}
	edit := &EditTool{Root: dir, Tracker: tracker}
	glob := &GlobTool{Root: dir}
	grep := &GrepTool{Root: dir}

	blocked := []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		"~/.ssh/id_rsa",
		filepath.Join(home, ".aws", "credentials"),
		filepath.Join(home, ".config", "tlith", "config.json"),
		filepath.Join(home, ".ssh", "new_key"), // not yet existing
	}
	for _, p := range blocked {
		if out := run(t, read, map[string]any{"file_path": p}); !strings.Contains(out, "blocked") {
			t.Errorf("Read %s not blocked: %s", p, out)
		}
		if out := run(t, write, map[string]any{"file_path": p, "content": "x"}); !strings.Contains(out, "blocked") {
			t.Errorf("Write %s not blocked: %s", p, out)
		}
		if out := run(t, edit, map[string]any{"file_path": p, "old_string": "a", "new_string": "b"}); !strings.Contains(out, "blocked") {
			t.Errorf("Edit %s not blocked: %s", p, out)
		}
	}
	if out := run(t, glob, map[string]any{"pattern": "*", "path": filepath.Join(home, ".ssh")}); !strings.Contains(out, "blocked") {
		t.Errorf("Glob ~/.ssh not blocked: %s", out)
	}
	if out := run(t, grep, map[string]any{"pattern": "KEY", "path": filepath.Join(home, ".ssh")}); !strings.Contains(out, "blocked") {
		t.Errorf("Grep ~/.ssh not blocked: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh", "new_key")); err == nil {
		t.Error("blocked Write created a file")
	}
	// ordinary files in the home directory are not credential stores
	if out := run(t, read, map[string]any{"file_path": filepath.Join(home, "notes.txt")}); !strings.Contains(out, "harmless") {
		t.Errorf("plain home file should be readable: %s", out)
	}
}

func TestCredentialBlockSeesThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	home := fakeHome(t)
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(home, ".ssh"), filepath.Join(dir, "innocent")); err != nil {
		t.Fatal(err)
	}
	read := &ReadTool{Root: dir, Tracker: NewTracker()}
	if out := run(t, read, map[string]any{"file_path": "innocent/id_rsa"}); !strings.Contains(out, "blocked") {
		t.Fatalf("symlink into ~/.ssh was readable: %s", out)
	}
}

func needs(t *testing.T, tool Tool, args map[string]any) (string, bool) {
	t.Helper()
	g, ok := tool.(ArgGater)
	if !ok {
		t.Fatalf("%s does not implement ArgGater", tool.Name())
	}
	raw, _ := json.Marshal(args)
	return g.NeedsPermission(raw)
}

func TestFileToolsAskOnlyOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "o.txt", "x")
	tracker := NewTracker()
	read := &ReadTool{Root: dir, Tracker: tracker}

	inside := []string{"a.txt", "sub/b.go", "./c", filepath.Join(dir, "d.txt"), "sub/../e.txt", "new/dir/f.txt"}
	for _, p := range inside {
		if reason, need := needs(t, read, map[string]any{"file_path": p}); need {
			t.Errorf("%q inside the workspace needs permission (%s)", p, reason)
		}
	}
	escapes := []string{"../x", "sub/../../x", filepath.Join(outside, "o.txt"), "/etc/hostname", "~/something"}
	for _, p := range escapes {
		reason, need := needs(t, read, map[string]any{"file_path": p})
		if !need || !strings.Contains(reason, "outside the working directory") {
			t.Errorf("%q should need permission, got need=%v reason=%q", p, need, reason)
		}
	}

	// the other path-taking tools use the same rule
	for _, tool := range []Tool{&WriteTool{Root: dir, Tracker: tracker}, &EditTool{Root: dir, Tracker: tracker}} {
		if _, need := needs(t, tool, map[string]any{"file_path": filepath.Join(outside, "o.txt")}); !need {
			t.Errorf("%s outside the workspace must ask", tool.Name())
		}
		if _, need := needs(t, tool, map[string]any{"file_path": "ok.txt"}); need {
			t.Errorf("%s inside the workspace must not need extra permission", tool.Name())
		}
	}
	glob := &GlobTool{Root: dir}
	grep := &GrepTool{Root: dir}
	if _, need := needs(t, glob, map[string]any{"pattern": "*.go"}); need {
		t.Error("Glob in the workspace asked")
	}
	if _, need := needs(t, glob, map[string]any{"pattern": "*", "path": outside}); !need {
		t.Error("Glob outside the workspace must ask")
	}
	if _, need := needs(t, glob, map[string]any{"pattern": "../**/*.go"}); !need {
		t.Error("Glob pattern climbing out must ask")
	}
	if _, need := needs(t, glob, map[string]any{"pattern": filepath.Join(outside, "*")}); !need {
		t.Error("absolute Glob pattern outside must ask")
	}
	if _, need := needs(t, grep, map[string]any{"pattern": "x"}); need {
		t.Error("Grep in the workspace asked")
	}
	if _, need := needs(t, grep, map[string]any{"pattern": "x", "path": outside}); !need {
		t.Error("Grep outside the workspace must ask")
	}
}

func TestSymlinkEscapeNeedsPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, outside, "secret.txt", "s")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	read := &ReadTool{Root: dir, Tracker: NewTracker()}
	if _, need := needs(t, read, map[string]any{"file_path": "link/secret.txt"}); !need {
		t.Fatal("a symlink pointing outside the workspace must be treated as outside")
	}
}

func TestWorkspaceRootBehindASymlinkStillCountsAsInside(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "proj")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	read := &ReadTool{Root: link, Tracker: NewTracker()}
	for _, p := range []string{"a.txt", filepath.Join(link, "a.txt"), filepath.Join(real, "a.txt")} {
		if reason, need := needs(t, read, map[string]any{"file_path": p}); need {
			t.Errorf("%q is inside the project but asked: %s", p, reason)
		}
	}
}

func TestReadOnlyToolsRejectCredentialPathsAtPreparation(t *testing.T) {
	home := fakeHome(t)
	dir := t.TempDir()
	cases := []struct {
		tool Tool
		args map[string]any
	}{
		{&ReadTool{Root: dir, Tracker: NewTracker()}, map[string]any{"file_path": filepath.Join(home, ".ssh", "id_rsa")}},
		{&GlobTool{Root: dir}, map[string]any{"pattern": "*", "path": filepath.Join(home, ".aws")}},
		{&GlobTool{Root: dir}, map[string]any{"pattern": filepath.Join(home, ".ssh") + "/*"}},
		{&GrepTool{Root: dir}, map[string]any{"pattern": "x", "path": filepath.Join(home, ".config", "tlith")}},
	}
	for _, c := range cases {
		if _, err := prepare(t, c.tool, c.args); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Errorf("%s %v: err = %v", c.tool.Name(), c.args, err)
		}
	}
	// ordinary reads prepare cleanly
	writeFile(t, dir, "a.txt", "x")
	if _, err := prepare(t, &ReadTool{Root: dir, Tracker: NewTracker()}, map[string]any{"file_path": "a.txt"}); err != nil {
		t.Errorf("plain read: %v", err)
	}
	if _, err := prepare(t, &GlobTool{Root: dir}, map[string]any{"pattern": "**/*.go"}); err != nil {
		t.Errorf("plain glob: %v", err)
	}
}
