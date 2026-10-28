package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
)

// GrepTool searches file contents. It prefers ripgrep when installed (which
// respects ignore files) and falls back to a pure-Go walker.
type GrepTool struct {
	Root string
}

func (t *GrepTool) Name() string { return "Grep" }

func (t *GrepTool) Description() string {
	return "Search file contents with a regular expression. " +
		"output_mode: \"files_with_matches\" (default) lists files, \"content\" shows matching lines, " +
		"\"count\" counts matches per file. Narrow the search with path and glob."
}

func (t *GrepTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"pattern":     prop("string", "Regular expression to search for (ripgrep syntax)."),
		"path":        prop("string", "File or directory to search. Defaults to the working directory."),
		"glob":        prop("string", "Filter files by glob, e.g. \"*.go\" or \"src/**/*.ts\"."),
		"ignore_case": prop("boolean", "Case-insensitive search."),
		"output_mode": enumProp([]string{"files_with_matches", "content", "count"}, "What to return (default files_with_matches)."),
		"head_limit":  prop("integer", "Maximum results to return (default 100)."),
	}, "pattern")
}

func (t *GrepTool) Gated() bool { return false }

// NeedsPermission asks before searching outside the working directory.
func (t *GrepTool) NeedsPermission(raw json.RawMessage) (string, bool) {
	var args grepArgs
	if decodeArgs(raw, &args) != nil {
		return "", false
	}
	return outsideWorkspace(t.Root, args.Path)
}

// ConcurrencySafe: read-only and stateless, so calls may overlap.
func (t *GrepTool) ConcurrencySafe() bool { return true }

func (t *GrepTool) Category() string { return permissions.CategoryRead }

type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	IgnoreCase bool   `json:"ignore_case"`
	OutputMode string `json:"output_mode"`
	HeadLimit  int    `json:"head_limit"`
}

const grepDefaultLimit = 100

func (t *GrepTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args grepArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(args.Pattern) == "" {
		return "", fmt.Errorf("pattern is empty")
	}
	switch args.OutputMode {
	case "", "files_with_matches", "content", "count":
	default:
		return "", fmt.Errorf("invalid output_mode %q", args.OutputMode)
	}
	if args.OutputMode == "" {
		args.OutputMode = "files_with_matches"
	}
	if args.HeadLimit <= 0 {
		args.HeadLimit = grepDefaultLimit
	}

	if rg, err := exec.LookPath("rg"); err == nil {
		return t.runRipgrep(ctx, rg, args)
	}
	return t.runGo(ctx, args)
}

func (t *GrepTool) runRipgrep(ctx context.Context, rg string, args grepArgs) (string, error) {
	dir := "."
	if args.Path != "" {
		resolved, err := resolvePath(t.Root, args.Path)
		if err != nil {
			return "", err
		}
		dir = resolved
	}
	cmdArgs := []string{"--no-heading", "--color", "never", "--with-filename", "--max-columns", "400", "--max-columns-preview"}
	switch args.OutputMode {
	case "content":
		cmdArgs = append(cmdArgs, "-n")
	case "files_with_matches":
		cmdArgs = append(cmdArgs, "-l")
	case "count":
		cmdArgs = append(cmdArgs, "-c")
	}
	if args.IgnoreCase {
		cmdArgs = append(cmdArgs, "-i")
	}
	if args.Glob != "" {
		cmdArgs = append(cmdArgs, "-g", args.Glob)
	}
	cmdArgs = append(cmdArgs, "--", args.Pattern, dir)

	cmd := exec.CommandContext(ctx, rg, cmdArgs...)
	cmd.Dir = t.Root
	out := newLimitedBuffer(4 * 1024 * 1024)
	errOut := newLimitedBuffer(64 * 1024)
	cmd.Stdout = out
	cmd.Stderr = errOut
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		switch exitErr.ExitCode() {
		case 1: // no matches
			return "No matches found.", nil
		case 2: // real error
			return "", fmt.Errorf("ripgrep failed: %s", strings.TrimSpace(errOut.String()))
		}
	} else if err != nil {
		return "", err
	}
	return t.formatResults(out.String(), args.HeadLimit, true)
}

func (t *GrepTool) runGo(ctx context.Context, args grepArgs) (string, error) {
	pattern := args.Pattern
	if args.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regular expression: %w", err)
	}
	base := t.Root
	if args.Path != "" {
		if base, err = resolvePath(t.Root, args.Path); err != nil {
			return "", err
		}
	}
	if _, err := os.Stat(base); err != nil {
		return "", fmt.Errorf("cannot access %s: %w", base, err)
	}

	var results []string
	var scanned int
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
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
		if len(results) >= args.HeadLimit || scanned > 20000 {
			return filepath.SkipAll
		}
		scanned++
		rel := relativePath(t.Root, path)
		if args.Glob != "" && !globMatches(args.Glob, rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2*1024*1024 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || IsBinary(data) {
			return nil
		}
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		fileMatches := 0
		for i, line := range lines {
			if !re.MatchString(line) {
				continue
			}
			fileMatches++
			switch args.OutputMode {
			case "content":
				if len(line) > 400 {
					line = truncateText(line, 400)
				}
				results = append(results, fmt.Sprintf("%s:%d:%s", rel, i+1, line))
			case "count":
				continue
			default:
				results = append(results, rel)
			}
			if len(results) >= args.HeadLimit {
				break
			}
			if args.OutputMode != "content" {
				break // one hit per file for files_with_matches
			}
		}
		if args.OutputMode == "count" && fileMatches > 0 {
			results = append(results, fmt.Sprintf("%s:%d", rel, fileMatches))
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll && walkErr != context.Canceled {
		return "", walkErr
	}
	if len(results) == 0 {
		return "No matches found.", nil
	}
	return strings.Join(results, "\n"), nil
}

// globMatches applies a ripgrep-style glob: it can match the whole relative
// path or just the basename (so "*.go" matches at any depth).
func globMatches(pattern, rel string) bool {
	slash := filepath.ToSlash(rel)
	if ok, err := doublestar.Match(pattern, slash); err == nil && ok {
		return true
	}
	if ok, err := doublestar.Match(pattern, filepath.Base(slash)); err == nil && ok {
		return true
	}
	return false
}

// formatResults trims raw finder output to head_limit entries.
func (t *GrepTool) formatResults(raw string, limit int, truncateLines bool) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var lines []string
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			continue
		}
		if truncateLines && len(line) > 500 {
			line = truncateText(line, 500)
		}
		lines = append(lines, line)
		if len(lines) > limit {
			break
		}
	}
	if len(lines) == 0 {
		return "No matches found.", nil
	}
	truncated := len(lines) > limit
	if truncated {
		lines = lines[:limit]
	}
	out := strings.Join(lines, "\n")
	if truncated {
		out += fmt.Sprintf("\n… (results limited to %d)", limit)
	}
	return out, nil
}
