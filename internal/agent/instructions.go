package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

// Instructions is one instruction file loaded into the system prompt.
type Instructions struct {
	Scope     string // "user" or "project"
	Name      string // file name, e.g. AGENTS.md
	Path      string
	Text      string
	Truncated bool
}

// instructionNames are searched in this order within one directory: tlc's own
// file first, then the conventions other agent tools already write.
var instructionNames = []string{"TLC.md", "AGENTS.md", "CLAUDE.md"}

const (
	// DefaultInstructionsMax caps each instruction file. The system prompt
	// shares the server's context budget with everything else, so this is
	// small on purpose; keep instruction files terse.
	DefaultInstructionsMax = 4096
	// EnvInstructionsMax overrides the cap in bytes; 0 turns loading off.
	EnvInstructionsMax = "TLC_INSTRUCTIONS_MAX"
)

// InstructionsMaxFromEnv reads the per-file cap.
func InstructionsMaxFromEnv() int {
	if v := strings.TrimSpace(os.Getenv(EnvInstructionsMax)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return DefaultInstructionsMax
}

// LoadInstructions finds the instruction files that apply to cwd: the user's
// own (~/.config/tlith/TLC.md), then the nearest TLC.md / AGENTS.md /
// CLAUDE.md walking up from cwd to the project root (the git root; without a
// repository only cwd itself). The nearest directory wins and within it the
// order above does. maxBytes <= 0 loads nothing.
func LoadInstructions(cwd string, maxBytes int) []Instructions {
	if maxBytes <= 0 {
		return nil
	}
	var out []Instructions
	userPath := filepath.Join(filepath.Dir(config.ConnectionPath()), "TLC.md")
	if in, ok := readInstructions("user", userPath, maxBytes); ok {
		out = append(out, in)
	}

	root := config.FindProjectRoot(cwd)
	dir := cwd
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		dir = real
	}
	for {
		for _, name := range instructionNames {
			if in, ok := readInstructions("project", filepath.Join(dir, name), maxBytes); ok {
				return append(out, in)
			}
		}
		if dir == root {
			return out
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return out
		}
		dir = parent
	}
}

// readInstructions loads one file if it is a non-empty regular text file.
func readInstructions(scope, path string, maxBytes int) (Instructions, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return Instructions{}, false
	}
	// read a little past the cap so truncation can be reported
	f, err := os.Open(path)
	if err != nil {
		return Instructions{}, false
	}
	defer f.Close()
	buf := make([]byte, maxBytes+1)
	n, _ := f.Read(buf)
	data := buf[:n]
	if strings.IndexByte(string(data), 0) >= 0 {
		return Instructions{}, false
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if text == "" {
		return Instructions{}, false
	}
	in := Instructions{Scope: scope, Name: filepath.Base(path), Path: path, Text: text}
	if n > maxBytes || info.Size() > int64(maxBytes) {
		in.Truncated = true
		cut := text
		if len(cut) > maxBytes {
			cut = cut[:maxBytes]
		}
		if i := strings.LastIndexByte(cut, '\n'); i > 0 {
			cut = cut[:i]
		}
		in.Text = strings.TrimRight(cut, "\n") + "\n… (truncated)"
	}
	return in, true
}
