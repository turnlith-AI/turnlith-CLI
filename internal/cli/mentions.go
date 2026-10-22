package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// @file mentions: on the wire, a mention becomes the user's text plus one
// <file path="…"> block per mentioned file, so the model sees the content
// without a Read round-trip — and the file counts as read, so it can be
// Edited immediately. Mentions that do not resolve to a file (emails,
// handles) stay literal.

var mentionRE = regexp.MustCompile(`@([A-Za-z0-9_.~/+-]+)`)

const (
	mentionMaxBytes = 96 * 1024
	mentionMaxLines = 2500
)

// attachment is one resolved mention.
type attachment struct {
	Path    string // workspace-relative, as typed/displayed
	Content string // file body, possibly truncated
	Bytes   int    // size of the attachment as sent
}

// expandMentions resolves the @file mentions in prompt against root and
// returns the wire prompt, the attachments, and human-readable problems for
// mentions that existed but could not be attached.
func expandMentions(root, prompt string, tracker *tools.Tracker) (string, []attachment, []string) {
	matches := mentionRE.FindAllStringSubmatch(prompt, -1)
	if len(matches) == 0 {
		return prompt, nil, nil
	}

	seen := map[string]bool{}
	var attached []attachment
	var problems []string
	var b strings.Builder
	b.WriteString(prompt)
	for _, m := range matches {
		token := m[1]
		if seen[token] {
			continue
		}
		seen[token] = true

		path := strings.TrimRight(token, ".,;:!?") // sentence punctuation
		if path == "" {
			continue
		}
		if path == "~" || strings.HasPrefix(path, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, strings.TrimPrefix(path[1:], "/"))
			}
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)

		info, err := os.Stat(path)
		if err != nil {
			continue // not a file — leave the mention literal
		}
		if info.IsDir() {
			problems = append(problems, fmt.Sprintf("%s is a directory — @-mention a file, or TAB-complete into it", m[0]))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("could not read %s: %v", m[0], err))
			continue
		}
		if tools.IsBinary(data) {
			problems = append(problems, fmt.Sprintf("%s is binary and was not attached", m[0]))
			continue
		}
		content, truncated := capMention(string(data))
		rel := path
		if r, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		if truncated {
			content += fmt.Sprintf("\n… (truncated — %s is larger than the mention cap)", rel)
		}
		attached = append(attached, attachment{Path: rel, Content: content, Bytes: len(content)})
		if tracker != nil {
			tracker.MarkRead(path)
		}
	}
	if len(attached) == 0 {
		return prompt, nil, problems
	}
	for _, at := range attached {
		fmt.Fprintf(&b, "\n\n<file path=%q>\n%s\n</file>", at.Path, strings.TrimRight(at.Content, "\n"))
	}
	return b.String(), attached, problems
}

// capMention bounds a file body by both lines and bytes.
func capMention(s string) (string, bool) {
	truncated := false
	if n := strings.Count(s, "\n") + 1; n > mentionMaxLines {
		lines := strings.Split(s, "\n")
		s = strings.Join(lines[:mentionMaxLines], "\n")
		truncated = true
	}
	if len(s) > mentionMaxBytes {
		cut := mentionMaxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
		truncated = true
	}
	return s, truncated
}

// humanSize renders a byte count for notices.
func humanSize(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// attachMentions expands a prompt's @file mentions for the wire and reports
// what happened on the given renderer (stdout interactively, stderr in print
// mode, where stdout must stay machine-readable).
func (a *app) attachMentions(prompt string, notices *ui.Renderer) string {
	if !strings.Contains(prompt, "@") {
		return prompt
	}
	expanded, attached, problems := expandMentions(a.cwd, prompt, a.tracker)
	for _, at := range attached {
		notices.Notice(fmt.Sprintf("attached %s (%s)", at.Path, humanSize(at.Bytes)))
	}
	for _, p := range problems {
		notices.Notice(p)
	}
	return expanded
}
