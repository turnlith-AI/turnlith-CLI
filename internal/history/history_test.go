package history

import (
	"os"
	"path/filepath"
	"strings"
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

func newSession(t *testing.T, cwd, agent string, msgs ...string) string {
	t.Helper()
	s, err := Start(cwd, agent)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		s.Append(role, m)
	}
	path := s.Path
	_ = s.Close()
	return path
}

func TestSessionsStartedInTheSameInstantDoNotShareAFile(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		p := newSession(t, cwd, "a", "hi")
		if seen[p] {
			t.Fatalf("two sessions got the same file: %s", p)
		}
		seen[p] = true
	}
}

func TestListIsNewestFirstAndScopedToTheDirectory(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd, other := t.TempDir(), t.TempDir()
	first := newSession(t, cwd, "sigma", "fix the failing login test please", "ok")
	newSession(t, other, "sigma", "elsewhere")
	second := newSession(t, cwd, "lil-coder", "second\nline", "reply", "more")

	infos, err := List(cwd, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].Path != second || infos[1].Path != first {
		t.Fatalf("infos = %+v", infos)
	}
	if infos[0].Agent != "lil-coder" || infos[0].Messages != 3 || infos[0].Preview != "second line" {
		t.Fatalf("info = %+v", infos[0])
	}
	if infos[1].Preview != "fix the failing login test please" || infos[1].Started.IsZero() {
		t.Fatalf("info = %+v", infos[1])
	}
	if limited, _ := List(cwd, 1); len(limited) != 1 || limited[0].Path != second {
		t.Fatalf("limit not applied: %+v", limited)
	}
	if none, _ := List(t.TempDir(), 5); len(none) != 0 {
		t.Fatalf("unrelated directory: %+v", none)
	}
}

func TestPreviewIsShortened(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	newSession(t, cwd, "a", strings.Repeat("word ", 60))
	infos, _ := List(cwd, 1)
	if r := []rune(infos[0].Preview); len(r) > 80 || !strings.HasSuffix(infos[0].Preview, "…") {
		t.Fatalf("preview = %q", infos[0].Preview)
	}
}

func TestOpenReopensASpecificSessionForAppending(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	older := newSession(t, cwd, "sigma", "old question", "old answer")
	newSession(t, cwd, "sigma", "newer question")

	s, entries, err := Open(older)
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "sigma" || len(entries) != 2 || entries[0].Text != "old question" {
		t.Fatalf("agent=%q entries=%+v", s.Agent, entries)
	}
	s.Append("user", "follow-up")
	_ = s.Close()
	again, _ := List(cwd, 5)
	for _, in := range again {
		if in.Path == older && in.Messages != 3 {
			t.Fatalf("append went to the wrong file: %+v", in)
		}
	}
}

func TestContinueForPrefersTheCurrentAgent(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cwd := t.TempDir()
	sigma := newSession(t, cwd, "sigma", "with sigma")
	newSession(t, cwd, "lil-coder", "with lil-coder, newer")

	s, entries, err := ContinueFor(cwd, "sigma")
	if err != nil || s == nil || s.Path != sigma || len(entries) != 1 {
		t.Fatalf("s=%v entries=%v err=%v", s, entries, err)
	}
	_ = s.Close()
	// no session with that agent: fall back to the newest overall
	s, _, err = ContinueFor(cwd, "nobody")
	if err != nil || s == nil || s.Agent != "lil-coder" {
		t.Fatalf("fallback: %v %v", s, err)
	}
	_ = s.Close()
}
