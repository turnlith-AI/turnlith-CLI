package ui

import (
	"strings"
	"testing"
)

func TestHelpTextCoversEveryCommand(t *testing.T) {
	help := HelpText(&Theme{})
	for _, c := range Commands {
		if !strings.Contains(help, c.Name) {
			t.Errorf("help text is missing %s", c.Name)
		}
		if !strings.Contains(help, c.Help) {
			t.Errorf("help text is missing the description of %s", c.Name)
		}
	}
}

func TestSuggestTextListsCommandsAndTab(t *testing.T) {
	text := SuggestText(&Theme{})
	for _, c := range Commands {
		if !strings.Contains(text, c.Name) {
			t.Errorf("suggestion table is missing %s", c.Name)
		}
		if !strings.Contains(text, c.Help) {
			t.Errorf("suggestion table is missing the description of %s", c.Name)
		}
	}
	if !strings.Contains(text, "TAB") {
		t.Errorf("suggestion table does not mention TAB: %q", text)
	}
}

func TestCompletionWordsCommands(t *testing.T) {
	all := CompletionWords("/", nil)
	if len(all) != len(Commands) {
		t.Fatalf("got %d commands, want %d: %v", len(all), len(Commands), all)
	}
	if all[0] != "/help" {
		t.Fatalf("first completion = %q", all[0])
	}

	narrowed := CompletionWords("/mo", nil)
	if len(narrowed) != 1 || narrowed[0] != "/model" {
		t.Fatalf("narrowed completions = %v", narrowed)
	}

	if got := CompletionWords("/zzz", nil); len(got) != 0 {
		t.Fatalf("no command should match /zzz: %v", got)
	}
}

func TestCompletionWordsArguments(t *testing.T) {
	var seenBefore, seenPrefix string
	extra := func(before, prefix string) []string {
		seenBefore, seenPrefix = before, prefix
		return []string{"sigma", "sigma-two"}
	}

	got := CompletionWords("/agent sig", extra)
	if seenBefore != "/agent" || seenPrefix != "sig" {
		t.Fatalf("extra called with (%q, %q)", seenBefore, seenPrefix)
	}
	if len(got) != 2 || got[0] != "sigma" {
		t.Fatalf("argument completions = %v", got)
	}

	// a trailing space starts a fresh argument token
	got = CompletionWords("/agent ", extra)
	if seenPrefix != "" || len(got) != 2 {
		t.Fatalf("empty-prefix completion = %v (prefix %q)", got, seenPrefix)
	}

	// without a provider, only command names complete
	if got := CompletionWords("/agent sig", nil); got != nil {
		t.Fatalf("nil extra should return nothing, got %v", got)
	}
}

func TestCompletionWordsIgnoresPlainInput(t *testing.T) {
	if got := CompletionWords("fix the bug", func(string, string) []string {
		t.Fatal("extra must not be consulted for plain input")
		return nil
	}); got != nil {
		t.Fatalf("completions for plain input = %v", got)
	}
}

func TestCompletionWordsMentions(t *testing.T) {
	var seenBefore, seenPrefix string
	extra := func(before, prefix string) []string {
		seenBefore, seenPrefix = before, prefix
		return []string{"@internal/cli/"}
	}

	got := CompletionWords("look at @int", extra)
	if seenBefore != "look at" || seenPrefix != "@int" {
		t.Fatalf("extra called with (%q, %q)", seenBefore, seenPrefix)
	}
	if len(got) != 1 || got[0] != "@internal/cli/" {
		t.Fatalf("mention completions = %v", got)
	}

	// a token without "@" is prose, not a mention
	seenPrefix = ""
	if got := CompletionWords("look at int", extra); got != nil || seenPrefix != "" {
		t.Fatalf("plain tokens must not complete: %v", got)
	}
}

func TestCompleterReturnsSuffixes(t *testing.T) {
	c := NewCompleter(nil)
	rest, length := c.Do([]rune("/mo"), 3)
	if length != 3 {
		t.Fatalf("length = %d, want 3", length)
	}
	if len(rest) != 1 || string(rest[0]) != "del" {
		t.Fatalf("suffixes = %v", rest)
	}

	// nothing to complete for plain prose
	rest, length = c.Do([]rune("hello"), 5)
	if len(rest) != 0 || length != 0 {
		t.Fatalf("plain input should not complete: %v %d", rest, length)
	}
}
