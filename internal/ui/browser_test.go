package ui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sliceSource feeds fixed bytes; next() returns false at the end, standing in
// for the raw-mode timeout.
type sliceSource struct {
	data []byte
	i    int
}

func (s *sliceSource) first() (byte, bool) { return s.next() }

func (s *sliceSource) next() (byte, bool) {
	if s.i >= len(s.data) {
		return 0, false
	}
	b := s.data[s.i]
	s.i++
	return b, true
}

func TestDecodeKey(t *testing.T) {
	cases := []struct {
		bytes []byte
		want  string
	}{
		{[]byte{0x1b, '[', 'A'}, "up"},
		{[]byte{0x1b, '[', 'B'}, "down"},
		{[]byte{0x1b, '[', 'C'}, "right"},
		{[]byte{0x1b, '[', 'D'}, "left"},
		{[]byte{0x1b}, "esc"},
		{[]byte{'\r'}, "enter"},
		{[]byte{'\n'}, "enter"},
		{[]byte{0x7f}, "backspace"},
		{[]byte{0x08}, "backspace"},
		{[]byte{0x03}, "ctrlC"},
		{[]byte{'k'}, "k"},
		{[]byte{0xc3, 0xa9}, "é"},
	}
	for _, tc := range cases {
		got, err := decodeKey(&sliceSource{data: tc.bytes})
		if err != nil || got != tc.want {
			t.Errorf("decodeKey(%v) = %q, %v; want %q", tc.bytes, got, err, tc.want)
		}
	}
}

func TestActionForKey(t *testing.T) {
	nav := []struct {
		key  string
		want baction
	}{
		{"up", actUp}, {"k", actUp},
		{"down", actDown}, {"j", actDown},
		{"right", actOpen}, {"l", actOpen}, {"enter", actOpen},
		{"left", actParent}, {"h", actParent}, {"u", actParent}, {"backspace", actParent},
		{"q", actClose}, {"esc", actClose}, {"ctrlC", actClose},
		{"/", actFilterStart},
		{"r", actRefresh},
		{"x", actNone},
	}
	for _, tc := range nav {
		if got, _ := actionForKey(tc.key, false); got != tc.want {
			t.Errorf("actionForKey(%q, nav) = %v, want %v", tc.key, got, tc.want)
		}
	}
	filter := []struct {
		key  string
		want baction
	}{
		{"enter", actFilterStop},
		{"esc", actFilterClear},
		{"backspace", actBackspace},
		{"up", actUp},
		{"x", actType},
	}
	for _, tc := range filter {
		got, _ := actionForKey(tc.key, true)
		if got != tc.want {
			t.Errorf("actionForKey(%q, filtering) = %v, want %v", tc.key, got, tc.want)
		}
	}
	if _, r := actionForKey("é", true); r != 'é' {
		t.Errorf("filter rune = %q", r)
	}
}

func browserDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("main.go")
	mk("internal/cli.go")
	mk("node_modules/skip.go")
	return dir
}

func TestBrowserLoadRowsAndNavigation(t *testing.T) {
	dir := browserDir(t)
	st := &browserState{
		Root:    dir,
		Touched: []FileBadge{{Path: "internal/cli.go", Read: true}},
	}
	st.loadRows()

	var labels []string
	for _, r := range st.Rows {
		labels = append(labels, r.Label)
	}
	want := []string{"internal/cli.go", "internal/", "main.go"}
	if len(labels) != len(want) {
		t.Fatalf("rows = %v", labels)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("rows = %v, want %v", labels, want)
		}
	}
	if !st.Rows[0].Session || !st.Rows[0].Read {
		t.Fatalf("session row = %+v", st.Rows[0])
	}

	// down to internal/, open → inside; ../ first
	st.apply(actDown, 0)
	picked, _ := st.apply(actOpen, 0)
	if picked != "" || st.Cwd != "internal" {
		t.Fatalf("entering a directory failed: cwd=%q picked=%q", st.Cwd, picked)
	}
	if st.Rows[0].Label != "../" || st.Rows[1].Label != "cli.go" {
		t.Fatalf("rows inside internal = %+v", st.Rows)
	}
	picked, closed := st.apply(actDown, 0) // onto cli.go
	if picked != "" || closed {
		t.Fatal("down must not pick")
	}
	picked, _ = st.apply(actOpen, 0)
	if picked != "internal/cli.go" {
		t.Fatalf("picked = %q", picked)
	}

	// parent from the root view is a no-op
	st2 := &browserState{Root: dir}
	st2.loadRows()
	st2.apply(actParent, 0)
	if st2.Cwd != "" {
		t.Fatalf("parent at root moved to %q", st2.Cwd)
	}
}

func TestBrowserFilter(t *testing.T) {
	dir := browserDir(t)
	st := &browserState{Root: dir, Touched: []FileBadge{{Path: "internal/cli.go", Read: true}}}
	st.loadRows()

	st.apply(actFilterStart, 0)
	st.apply(actType, 'c')
	st.apply(actType, 'l')
	if st.Filter != "cl" || !st.Filtering {
		t.Fatalf("filter = %q", st.Filter)
	}
	var labels []string
	for _, r := range st.Rows {
		labels = append(labels, r.Label)
	}
	if len(labels) != 1 || labels[0] != "internal/cli.go" {
		t.Fatalf("filtered rows = %v", labels)
	}

	st.apply(actFilterClear, 0)
	if st.Filter != "" || len(st.Rows) != 3 {
		t.Fatalf("clearing the filter failed: %d rows", len(st.Rows))
	}
}

func TestRenderBrowserStableLineCount(t *testing.T) {
	dir := browserDir(t)
	st := &browserState{Root: dir, Touched: []FileBadge{{Path: "internal/cli.go", Written: true}}}
	st.loadRows()

	var buf bytes.Buffer
	n1 := renderBrowser(&buf, &Theme{}, st, 12)
	if got := strings.Count(buf.String(), "\n"); got != n1 {
		t.Fatalf("render wrote %d lines, reported %d", got, n1)
	}
	st.apply(actDown, 0)
	buf.Reset()
	n2 := renderBrowser(&buf, &Theme{}, st, 12)
	if n1 != n2 {
		t.Fatalf("line count changed across redraws: %d → %d", n1, n2)
	}
}
