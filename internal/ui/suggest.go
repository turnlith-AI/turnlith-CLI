package ui

import (
	"strings"

	"github.com/ergochat/readline"
)

// Command describes one REPL slash command. This table is the single source
// of truth for /help, the menu printed when "/" opens a command line, and TAB
// completion — the three can never drift apart.
type Command struct {
	Name string // "/model"
	Args string // "[spec] [effort]"; empty when the command takes none
	Help string // one-line description
}

// Commands lists every slash command in menu order.
var Commands = []Command{
	{Name: "/help", Help: "this help"},
	{Name: "/clear", Help: "reset the local task history (server memory is kept)"},
	{Name: "/agent", Args: "[name|new]", Help: "switch agent (memory is per agent) or create one"},
	{Name: "/agents", Help: "list the workspace's agents"},
	{Name: "/model", Args: "[spec] [effort]", Help: "switch the agent's model and reasoning effort"},
	{Name: "/effort", Args: "[level]", Help: "set reasoning effort (low · medium · high · …)"},
	{Name: "/status", Help: "show agent, server, and session details"},
	{Name: "/init", Help: "have the agent write TLC.md (project instructions) for this repo"},
	{Name: "/undo", Args: "[force]", Help: "revert the agent's last file change"},
	{Name: "/diff", Help: "show what the agent changed this session (git)"},
	{Name: "/copy", Help: "copy the last answer to the clipboard"},
	{Name: "/resume", Args: "[n]", Help: "continue an earlier session from this directory"},
	{Name: "/doctor", Help: "check the connection, key, agent and local tools"},
	{Name: "/edit", Help: "write a long prompt in $EDITOR (a trailing \\ continues a line at the prompt)"},
	{Name: "/files", Help: "browse the workspace and @-mention a file into the prompt"},
	{Name: "/permissions", Args: "[normal|auto|plan|yolo]", Help: "set the permission mode; no argument cycles (Tab at an empty prompt cycles too)"},
	{Name: "/verbose", Help: "toggle token usage display"},
	{Name: "/yolo", Help: "toggle yolo mode (alias for /permissions yolo)"},
	{Name: "/exit", Help: "quit"},
}

// commandTable renders the aligned two-column command menu: the invocation
// (accent name, dim arguments) and its description.
func commandTable(t *Theme) string {
	width := 0
	for _, c := range Commands {
		if n := len(c.Name) + len(c.Args) + 1; n > width {
			width = n
		}
	}
	var b strings.Builder
	for _, c := range Commands {
		line := t.Accent(c.Name)
		if c.Args != "" {
			line += " " + t.Dim(c.Args)
		}
		if pad := width + 2 - len(c.Name) - len(c.Args) - 1; pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		b.WriteString("  " + line + t.Dim(c.Help) + "\n")
	}
	return b.String()
}

// HelpText renders the /help body from the command table.
func HelpText(t *Theme) string {
	return strings.TrimRight(commandTable(t), "\n")
}

// SuggestText is printed once when "/" opens a fresh command line: the whole
// command menu, so the commands are discoverable before anything is typed.
// TAB then opens readline's own completion menu, filtered as the line narrows.
func SuggestText(t *Theme) string {
	return commandTable(t) +
		"  " + t.Faint("TAB completes commands, arguments, and @file mentions") + "\n"
}

// currentToken splits the line being edited into everything before the token
// under the cursor and the token itself.
func currentToken(line string) (before, token string) {
	if i := strings.LastIndexByte(line, ' '); i >= 0 {
		return strings.TrimRight(line[:i], " "), line[i+1:]
	}
	return "", line
}

// CompletionWords returns the full words the token under the cursor could
// expand to. Command names complete at the start of a slash line, arguments
// after it; a token starting with "@" completes to @file mentions. Both are
// delegated to extra, which receives the already-typed command part (or the
// text before the mention) and the current token. Pure, so it is unit tested
// without a terminal.
func CompletionWords(line string, extra func(before, prefix string) []string) []string {
	before, prefix := currentToken(line)
	if strings.HasPrefix(line, "/") {
		if before == "" {
			words := make([]string, 0, len(Commands))
			for _, c := range Commands {
				if strings.HasPrefix(c.Name, prefix) {
					words = append(words, c.Name)
				}
			}
			return words
		}
		if extra == nil {
			return nil
		}
		return extra(before, prefix)
	}
	if strings.HasPrefix(prefix, "@") && extra != nil {
		return extra(before, prefix)
	}
	return nil
}

// completer adapts CompletionWords to readline's AutoCompleter contract: Do
// returns, per candidate, the runes to append after the typed token plus the
// token length to keep.
type completer struct {
	extra func(before, prefix string) []string
}

func (c completer) Do(line []rune, pos int) ([][]rune, int) {
	text := string(line[:pos])
	words := CompletionWords(text, c.extra)
	if len(words) == 0 {
		return nil, 0
	}
	_, prefix := currentToken(text)
	prefixLen := len([]rune(prefix))
	rest := make([][]rune, 0, len(words))
	for _, w := range words {
		runes := []rune(w)
		if len(runes) < prefixLen {
			continue
		}
		rest = append(rest, runes[prefixLen:])
	}
	return rest, prefixLen
}

// NewCompleter builds the tab-completion engine; extra may be nil when only
// command names should complete.
func NewCompleter(extra func(before, prefix string) []string) readline.AutoCompleter {
	return completer{extra: extra}
}
