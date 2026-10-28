package tools

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
)

// resolvePath turns a tool-provided path into an absolute path: absolute
// paths and ~ are honored, everything else is relative to root.
func resolvePath(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("missing required path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p), nil
}

// relativePath renders p relative to root when it is inside root.
func relativePath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

// truncateText caps s at max bytes on a rune boundary, noting the cut.
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n… (truncated, %d more characters)", len(s)-cut)
}

// IsBinary reports whether data looks like a binary file.
func IsBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// skipDirs are heavyweight directories directory walks never descend into.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "vendor": true, "bower_components": true,
	"dist": true, "build": true, "target": true, "out": true,
	".venv": true, "venv": true, "__pycache__": true,
	".next": true, ".nuxt": true, ".cache": true, ".tox": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true,
	".idea": true, ".vscode": true,
}

func shouldSkipDir(name string) bool { return skipDirs[name] }

// IsSkipDir reports whether a directory should never be walked into (or
// listed): heavyweight caches and dependency trees.
func IsSkipDir(name string) bool { return skipDirs[name] }

// limitedBuffer captures process output up to a cap, discarding the rest
// while still reporting success so the child process keeps running.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func newLimitedBuffer(max int) *limitedBuffer { return &limitedBuffer{max: max} }

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	remaining := l.max - l.buf.Len()
	if remaining <= 0 {
		l.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		l.buf.Write(p[:remaining])
		l.truncated = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.buf.String()
	if l.truncated {
		s += "\n… (output truncated)"
	}
	return s
}

// isPureCRLF reports whether every line ending in s is "\r\n" (and there is at
// least one). Mixed files are deliberately not "CRLF files": rewriting their
// endings wholesale would change lines the edit never touched.
func isPureCRLF(s string) bool {
	n := strings.Count(s, "\n")
	return n > 0 && strings.Count(s, "\r\n") == n
}

func toLF(s string) string   { return strings.ReplaceAll(s, "\r\n", "\n") }
func toCRLF(s string) string { return strings.ReplaceAll(toLF(s), "\n", "\r\n") }

// writeFileAtomic replaces path with data without ever exposing a half-written
// file: it writes a temp file beside the target and renames it into place.
// Symlinks are followed (the link survives, the target changes), and mode is
// applied to the new file. Where a temp file cannot be created (read-only
// directory with a writable file) it falls back to an in-place write.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	target := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		target = real
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".tlc-*.tmp")
	if err != nil {
		return os.WriteFile(target, data, mode)
	}
	name := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Chmod(mode); err != nil && runtime.GOOS != "windows" {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Rename(name, target); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// lineCount counts lines in s, treating a trailing newline as a terminator.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(s, "\n"), "\n"))
}
