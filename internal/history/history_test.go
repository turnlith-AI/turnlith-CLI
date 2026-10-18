package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionRoundTrip(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()

	session, err := Start(cwd, "helper")
	if err != nil {
		t.Fatal(err)
	}
	session.Append("user", "hello")
	session.Append("assistant", "hi there")
	session.Append("assistant", "") // empty entries are dropped
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}

	continued, entries, err := Continue(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if continued == nil {
		t.Fatal("no session found for the same cwd")
	}
	if continued.Agent != "helper" {
		t.Fatalf("agent = %q", continued.Agent)
	}
	if len(entries) != 2 || entries[0].Text != "hello" || entries[1].Role != "assistant" {
		t.Fatalf("entries = %+v", entries)
	}
	_ = continued.Close()
}

func TestContinueIgnoresOtherDirectories(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	first := t.TempDir()
	second := t.TempDir()

	session, err := Start(first, "helper")
	if err != nil {
		t.Fatal(err)
	}
	session.Append("user", "private")
	_ = session.Close()

	continued, _, err := Continue(second)
	if err != nil {
		t.Fatal(err)
	}
	if continued != nil {
		t.Fatal("found a transcript from a different directory")
	}
}

func TestContinueWithoutSessions(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	continued, entries, err := Continue(t.TempDir())
	if err != nil || continued != nil || entries != nil {
		t.Fatalf("continued=%v entries=%v err=%v", continued, entries, err)
	}
	// a torn line must not break the transcript
	dir := sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	path := filepath.Join(dir, "20990101T000000-1.jsonl")
	content := `{"type":"meta","cwd":"` + cwd + `","agent":"helper"}` + "\n" + `{"type":"user","text":"ok"}` + "\n" + `{torn`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, entries, err = Continue(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "ok" {
		t.Fatalf("entries = %+v", entries)
	}
}
