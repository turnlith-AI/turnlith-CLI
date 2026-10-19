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
	{Name: "/verbose", Help: "toggle token usage display"},
	{Name: "/yolo", Help: "toggle permission prompts off/on"},
	{Name: "/exit", Help: "quit"},
}

// HelpText renders the /help body from the command table.
func HelpText() string {
	var b strings.Builder
	for _, c := range Commands {
		line := c.Name
		if c.Args != "" {
			line += " " + c.Args
		}
		b.WriteString(line)
		if pad := 22 - len(line); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		} else {
			b.WriteString(" ")
		}
		b.WriteString(c.Help)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// SuggestText is printed once when "/" opens a fresh command line: the whole
// menu, so the commands are discoverable before anything is typed. TAB then
// opens readline's own completion menu, filtered as the line narrows.
func SuggestText(t *Theme) string {
	names := make([]string, len(Commands))
	for i, c := range Commands {
		names[i] = t.Accent(c.Name)
	}
	return "  " + strings.Join(names, " ") + "\n" +
		"  " + t.Faint("TAB completes · /help describes each command") + "\n"
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
// expand to. Command names complete at the start of the line; arguments are
// delegated to extra (agents, models, effort levels) which receives the
// already-typed command part and the current token. Pure, so it is unit
// tested without a terminal.
func CompletionWords(line string, extra func(before, prefix string) []string) []string {
	if !strings.HasPrefix(line, "/") {
		return nil
	}
	before, prefix := currentToken(line)
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
