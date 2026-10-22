package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
)

// GlobTool finds files by pattern.
type GlobTool struct {
	Root string
}

func (t *GlobTool) Name() string { return "Glob" }

func (t *GlobTool) Description() string {
	return "Find files by glob pattern (supports **, e.g. \"src/**/*.ts\"). " +
		"Returns paths sorted by modification time, newest first."
}

func (t *GlobTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"pattern": prop("string", "Glob pattern, e.g. \"**/*.go\" or \"src/*.ts\"."),
		"path":    prop("string", "Directory to search in. Defaults to the working directory."),
	}, "pattern")
}

func (t *GlobTool) Gated() bool { return false }

func (t *GlobTool) Category() string { return permissions.CategoryRead }

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

const globMaxResults = 100

func (t *GlobTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args globArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "", fmt.Errorf("pattern is empty")
	}
	base := t.Root
	if args.Path != "" {
		var err error
		if base, err = resolvePath(t.Root, args.Path); err != nil {
			return "", err
		}
	}
	info, err := os.Stat(base)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", base, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", base)
	}

	absPattern := pattern
	if !filepath.IsAbs(pattern) {
		absPattern = filepath.Join(base, pattern)
	}
	absPattern = filepath.ToSlash(absPattern)

	type match struct {
		path    string
		modTime time.Time
	}
	var matches []match
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			if path != base && shouldSkipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		ok, matchErr := doublestar.Match(absPattern, filepath.ToSlash(path))
		if matchErr != nil || !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		matches = append(matches, match{path: path, modTime: info.ModTime()})
		return nil
	})
	if walkErr != nil && walkErr != context.Canceled {
		return "", walkErr
	}

	sort.Slice(matches, func(i, j int) bool { return matches[i].modTime.After(matches[j].modTime) })
	total := len(matches)
	if total > globMaxResults {
		matches = matches[:globMaxResults]
	}
	if total == 0 {
		return "No files matched.", nil
	}
	var b strings.Builder
	for _, m := range matches {
		b.WriteString(relativePath(t.Root, m.path))
		b.WriteByte('\n')
	}
	if total > globMaxResults {
		fmt.Fprintf(&b, "… (%d more matches)\n", total-globMaxResults)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
