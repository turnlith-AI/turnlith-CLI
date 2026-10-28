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

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

// resolvePathRaw turns a tool-provided path into an absolute path: absolute
// paths and ~ are honored, everything else is relative to root. It does not
// judge the result; resolvePath does.
func resolvePathRaw(root, p string) (string, error) {
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

// resolvePath is resolvePathRaw plus the hard rule that credential stores
// (ssh keys, cloud credentials, tlc's own api key file) are never readable or
// writable by tools, in any permission mode — a model that can read them can
// ship them to a server.
func resolvePath(root, p string) (string, error) {
	abs, err := resolvePathRaw(root, p)
	if err != nil {
		return "", err
	}
	if reason := credentialReason(abs); reason != "" {
		return "", fmt.Errorf("access to %s is blocked: %s", abs, reason)
	}
	return abs, nil
}

// canonicalAllowMissing resolves symlinks in the longest existing prefix of p,
// so paths that do not exist yet (a file about to be written) are still
// judged by where their directory really lives.
func canonicalAllowMissing(p string) string {
	p = filepath.Clean(p)
	var rest []string
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				real = filepath.Join(real, rest[i])
			}
			return real
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}

// isUnder reports whether path is dir itself or inside it (both canonical).
func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// insideRoot reports whether abs lies within the workspace root, judged after
// symlink resolution on both sides.
func insideRoot(root, abs string) bool {
	return isUnder(canonicalAllowMissing(abs), canonicalAllowMissing(root))
}

// outsideWorkspace reports, with a human reason, that a tool-provided path
// reaches outside the workspace root.
func outsideWorkspace(root, p string) (string, bool) {
	if strings.TrimSpace(p) == "" {
		return "", false
	}
	abs, err := resolvePathRaw(root, p)
	if err != nil || insideRoot(root, abs) {
		return "", false
	}
	return "outside the working directory: " + abs, true
}

// patternOutside judges a glob pattern against the workspace: its static
// directory prefix (the part before the first wildcard) is resolved against
// base, and any ".." segment counts as reaching out.
func patternOutside(root, base, pattern string) (string, bool) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", false
	}
	for _, seg := range strings.Split(filepath.ToSlash(pattern), "/") {
		if seg == ".." {
			return "pattern climbs out of the working directory: " + pattern, true
		}
	}
	prefix := pattern
	if i := strings.IndexAny(prefix, "*?[{"); i >= 0 {
		prefix = prefix[:i]
	}
	if i := strings.LastIndexAny(prefix, `/\`); i >= 0 {
		prefix = prefix[:i+1]
	} else {
		prefix = ""
	}
	dir := base
	if prefix != "" {
		if filepath.IsAbs(prefix) {
			dir = filepath.Clean(prefix)
		} else {
			dir = filepath.Join(base, prefix)
		}
	}
	if insideRoot(root, dir) {
		return "", false
	}
	return "outside the working directory: " + dir, true
}

// credentialReason names why a path is a credential store, or "" if it is not.
func credentialReason(abs string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	target := canonicalAllowMissing(abs)
	protected := []struct{ rel, why string }{
		{".ssh", "ssh keys and config"},
		{".aws", "cloud credentials"},
		{".gnupg", "gpg keys"},
		{".kube", "cluster credentials"},
		{filepath.Join(".config", "gcloud"), "cloud credentials"},
		{".azure", "cloud credentials"},
		{".netrc", "stored logins"},
		{".git-credentials", "stored logins"},
		{".npmrc", "registry tokens"},
		{".pypirc", "registry tokens"},
		{filepath.Join(".docker", "config.json"), "registry logins"},
	}
	for _, pr := range protected {
		if isUnder(target, canonicalAllowMissing(filepath.Join(home, pr.rel))) {
			return pr.why + " are off limits to tools"
		}
	}
	if isUnder(target, canonicalAllowMissing(filepath.Dir(config.ConnectionPath()))) {
		return "tlc's own api key and settings are off limits to tools"
	}
	return ""
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

// truncateMiddle caps s at head+tail bytes, keeping the start and the end and
// dropping the middle. Command output puts the cause of a failure at the end,
// so a plain prefix cut hides exactly what the model needs. Cuts land on rune
// boundaries.
func truncateMiddle(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	h := head
	for h > 0 && !utf8.RuneStart(s[h]) {
		h--
	}
	t := len(s) - tail
	for t < len(s) && !utf8.RuneStart(s[t]) {
		t++
	}
	return s[:h] + fmt.Sprintf("\n… (%d characters omitted) …\n", t-h) + s[t:]
}

// headTailBuffer captures process output keeping the first head bytes and the
// last tail bytes; anything between is counted and dropped, while Write still
// reports success so the child process keeps running.
type headTailBuffer struct {
	mu      sync.Mutex
	headMax int
	tailMax int
	head    []byte
	tail    []byte // at most 2*tailMax between trims
	dropped int
}

func newHeadTailBuffer(headMax, tailMax int) *headTailBuffer {
	return &headTailBuffer{headMax: headMax, tailMax: tailMax}
}

func (b *headTailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if room := b.headMax - len(b.head); room > 0 {
		take := min(room, len(p))
		b.head = append(b.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if len(b.tail) > 2*b.tailMax {
			cut := len(b.tail) - b.tailMax
			b.dropped += cut
			b.tail = append(b.tail[:0], b.tail[cut:]...)
		}
	}
	return n, nil
}

func (b *headTailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	tail := b.tail
	dropped := b.dropped
	if len(tail) > b.tailMax {
		dropped += len(tail) - b.tailMax
		tail = tail[len(tail)-b.tailMax:]
	}
	if dropped == 0 {
		return string(b.head) + string(tail)
	}
	return string(b.head) + fmt.Sprintf("\n… (%d bytes of output omitted) …\n", dropped) + string(tail)
}

// lineCount counts lines in s, treating a trailing newline as a terminator.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(s, "\n"), "\n"))
}
