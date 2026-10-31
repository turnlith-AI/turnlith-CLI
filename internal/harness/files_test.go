package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteConfigKeepsTheKeyPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permission bits")
	}
	dir := t.TempDir()

	created := filepath.Join(dir, "new", "client.json")
	if err := writeConfig(created, []byte(`{"apiKey":"sk-secret"}`)); err != nil {
		t.Fatal(err)
	}
	assertMode(t, created, 0o600)

	// an existing world-readable config loses group/other bits, keeps the
	// owner's (here: executable stays off, read/write stay on)
	existing := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(existing, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o664); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(existing, []byte(`{"apiKey":"sk-secret"}`)); err != nil {
		t.Fatal(err)
	}
	assertMode(t, existing, 0o600)

	// an owner-only file the user made stricter is left alone
	strict := filepath.Join(dir, "strict.json")
	if err := os.WriteFile(strict, []byte("{}"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(strict, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeConfig(strict, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	assertMode(t, strict, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s: mode = %o, want %o", filepath.Base(path), got, want)
	}
}
