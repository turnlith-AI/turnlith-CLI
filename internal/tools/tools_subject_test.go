package tools

import (
	"path/filepath"
	"testing"
)

func TestSubject(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	cases := []struct {
		tool, args, want string
	}{
		{"Bash", `{"command":"go test ./..."}`, "go test ./..."},
		{"Edit", `{"file_path":"src/a.go"}`, "src/a.go"},
		{"Write", `{"file_path":"` + filepath.ToSlash(filepath.Join(root, "docs", "a.md")) + `"}`, "docs/a.md"},
		{"Read", `{"file_path":"sub/../x.txt"}`, "x.txt"},
		{"Glob", `{"pattern":"*.go"}`, "."},
		{"Grep", `{"pattern":"x","path":"internal"}`, "internal"},
		{"Edit", `{"file_path":"` + filepath.ToSlash(filepath.Join(other, "o.txt")) + `"}`, filepath.ToSlash(filepath.Join(other, "o.txt"))},
		{"SearchMemory", `{"query":"x"}`, ""},
		{"Bash", `not json`, ""},
	}
	for _, c := range cases {
		if got := Subject(root, c.tool, []byte(c.args)); got != c.want {
			t.Errorf("Subject(%s, %s) = %q, want %q", c.tool, c.args, got, c.want)
		}
	}
}
