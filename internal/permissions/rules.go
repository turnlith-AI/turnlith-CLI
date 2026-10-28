package permissions

import (
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Rule grants or denies a family of tool calls, written like
//
//	Bash(go test:*)   Bash(git status)   Edit(docs/**)   Write   Bash
//
// A Bash pattern ending in ":*" matches any command starting with that prefix
// at a word boundary; without it the whole command must match. File tools
// match their path (relative to the workspace) with a glob. No pattern means
// every call of the tool.
type Rule struct {
	Tool    string
	Pattern string
	// External scopes the rule to calls that reach outside the workspace
	// (and only those); it is set by "always" answers to outside prompts and
	// never persisted.
	External bool
}

// ruleTools are the tools a rule may name.
var ruleTools = map[string]bool{
	"Bash": true, "Read": true, "Write": true, "Edit": true,
	"Glob": true, "Grep": true, "SearchMemory": true, "TodoWrite": true, "WebFetch": true,
}

// ParseRule reads "Tool" or "Tool(pattern)".
func ParseRule(s string) (Rule, error) {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	if open < 0 {
		if !ruleTools[s] {
			return Rule{}, fmt.Errorf("invalid permission rule %q: unknown tool (known: Bash, Read, Write, Edit, Glob, Grep, SearchMemory, TodoWrite, WebFetch)", s)
		}
		return Rule{Tool: s}, nil
	}
	tool := s[:open]
	if !ruleTools[tool] {
		return Rule{}, fmt.Errorf("invalid permission rule %q: unknown tool %q", s, tool)
	}
	if !strings.HasSuffix(s, ")") {
		return Rule{}, fmt.Errorf("invalid permission rule %q: missing closing parenthesis", s)
	}
	pattern := strings.TrimSpace(s[open+1 : len(s)-1])
	if pattern == "" {
		return Rule{}, fmt.Errorf("invalid permission rule %q: empty pattern (write just %q to cover every call)", s, tool)
	}
	return Rule{Tool: tool, Pattern: pattern}, nil
}

func (r Rule) String() string {
	s := r.Tool
	if r.Pattern != "" {
		s += "(" + r.Pattern + ")"
	}
	if r.External {
		s += " outside the workspace"
	}
	return s
}

// matchesSegment reports whether one simple command (no operators) falls
// under a Bash rule.
func (r Rule) matchesSegment(seg string) bool {
	if r.Pattern == "" {
		return true
	}
	if prefix, ok := strings.CutSuffix(r.Pattern, ":*"); ok {
		return seg == prefix || strings.HasPrefix(seg, prefix+" ")
	}
	return seg == r.Pattern
}

// matchesSubject reports whether a non-Bash call (its path) falls under a
// rule.
func (r Rule) matchesSubject(subject string) bool {
	if r.Pattern == "" {
		return true
	}
	ok, err := doublestar.Match(r.Pattern, subject)
	return err == nil && ok
}

// splitCommand cuts a shell command line into its simple commands at the
// unquoted operators ; & && | || and newlines. safe is false when the line
// contains something a prefix rule cannot reason about: command substitution
// ($(…), backticks, <(…)), input redirection, or output redirection to
// anything but /dev/null or a stream (2>&1). An allow rule must never cover
// such a line; deny rules still use the best-effort segments.
func splitCommand(cmd string) (segs []string, safe bool) {
	safe = true
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	rs := []rune(cmd)
	inSingle, inDouble := false, false
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
			cur.WriteRune(c)
		case c == '\\' && i+1 < len(rs):
			cur.WriteRune(c)
			i++
			cur.WriteRune(rs[i])
		case inDouble:
			switch {
			case c == '"':
				inDouble = false
			case c == '`', c == '$' && i+1 < len(rs) && rs[i+1] == '(':
				safe = false
			}
			cur.WriteRune(c)
		case c == '\'':
			inSingle = true
			cur.WriteRune(c)
		case c == '"':
			inDouble = true
			cur.WriteRune(c)
		case c == '`', c == '$' && i+1 < len(rs) && rs[i+1] == '(':
			safe = false
			cur.WriteRune(c)
		case c == '<':
			safe = false
			cur.WriteRune(c)
		case c == '>':
			rest := strings.TrimLeft(string(rs[i+1:]), " \t")
			switch {
			case strings.HasPrefix(rest, "&1"), strings.HasPrefix(rest, "&2"):
				cur.WriteString(">")
				// keep the "&1" with its redirect instead of treating & as a separator
				skip := len(rs[i+1:]) - len([]rune(rest))
				cur.WriteString(string(rs[i+1 : i+1+skip]))
				cur.WriteString(string(rs[i+1+skip : i+1+skip+2]))
				i += skip + 2
			case strings.HasPrefix(rest, "/dev/null"):
				cur.WriteRune(c)
			default:
				safe = false
				cur.WriteRune(c)
			}
		case c == ';' || c == '\n':
			flush()
		case c == '&' || c == '|':
			if i+1 < len(rs) && rs[i+1] == c {
				i++
			}
			flush()
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return segs, safe
}

// readOnlyBuiltins are commands that never change anything, so asking about
// them is noise. They are deliberately few: no cat/head/grep (those would read
// credential files past the file tools' boundary) and no find (-exec, -delete).
var readOnlyBuiltins = []string{
	"git status", "git diff", "git log", "git show", "git rev-parse", "git ls-files",
	"git branch --show-current", "ls", "pwd", "which", "cd",
}

// builtinUnsafeFlags turn an otherwise read-only builtin into something that
// writes files or reads outside the repository.
var builtinUnsafeFlags = []string{"--output", "--no-index", "--ext-diff", "--textconv"}

func builtinReadOnly(seg string) bool {
	for _, flag := range builtinUnsafeFlags {
		if strings.Contains(seg, flag) {
			return false
		}
	}
	for _, b := range readOnlyBuiltins {
		if seg == b || strings.HasPrefix(seg, b+" ") {
			return true
		}
	}
	return false
}

// twoWordTools take a subcommand, so "go test" and "go build" are different
// permissions while "make" and "pytest" are one.
var twoWordTools = map[string]bool{
	"git": true, "npm": true, "pnpm": true, "yarn": true, "bun": true, "deno": true,
	"go": true, "cargo": true, "docker": true, "kubectl": true, "helm": true,
	"pip": true, "pip3": true, "uv": true, "poetry": true, "gh": true, "terraform": true,
}

// SuggestRule proposes the rule an "always" answer should remember: for Bash
// the command's prefix ("go test:*", never "every command"), for file tools
// the tool itself. nil means no honest rule exists (a compound command, an
// outside-the-workspace call), and the answer applies to this call only.
func SuggestRule(req Request) *Rule {
	if req.External {
		return nil
	}
	if req.Tool != "Bash" {
		return &Rule{Tool: req.Tool}
	}
	segs, safe := splitCommand(req.Subject)
	if !safe || len(segs) != 1 {
		return nil
	}
	words := strings.Fields(segs[0])
	if len(words) == 0 {
		return nil
	}
	prefix := words[0]
	if twoWordTools[words[0]] {
		if len(words) < 2 || strings.HasPrefix(words[1], "-") {
			return nil
		}
		prefix = words[0] + " " + words[1]
	}
	return &Rule{Tool: "Bash", Pattern: prefix + ":*"}
}
