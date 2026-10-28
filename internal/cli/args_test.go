package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestEveryFlagIsDocumentedInHelp(t *testing.T) {
	var names []string
	for name := range boolFlags {
		names = append(names, name)
	}
	for name := range valueFlags {
		names = append(names, name)
	}
	for _, name := range names {
		if strings.HasPrefix(name, "--") && !strings.Contains(helpText, name) {
			t.Errorf("flag %s is not mentioned in the help text", name)
		}
	}
}

func TestParseArgsPermissionAndPrintFlags(t *testing.T) {
	opts, err := parseArgs([]string{
		"-p", "--permission-mode", "auto",
		"--allowed-tools", "Bash(go test:*),Edit(docs/**)",
		"--allowed-tools=Write",
		"--disallowed-tools", "Bash(git push:*)",
		"--max-turns", "5", "--output-format=json",
		"do", "the", "thing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.print || opts.permissionMode != "auto" || opts.maxTurns != 5 || opts.outputFormat != "json" {
		t.Fatalf("opts = %+v", opts)
	}
	if want := []string{"Bash(go test:*)", "Edit(docs/**)", "Write"}; !reflect.DeepEqual(opts.allowedTools, want) {
		t.Fatalf("allowed = %v, want %v", opts.allowedTools, want)
	}
	if want := []string{"Bash(git push:*)"}; !reflect.DeepEqual(opts.disallowedTools, want) {
		t.Fatalf("disallowed = %v", opts.disallowedTools)
	}
	if opts.prompt != "do the thing" {
		t.Fatalf("prompt = %q", opts.prompt)
	}
}

func TestParseArgsRejectsBadValues(t *testing.T) {
	for _, argv := range [][]string{
		{"--max-turns", "0"},
		{"--max-turns", "many"},
		{"--output-format", "xml"},
		{"--agent"},
		{"--output-format"},
		{"--bogus"},
		{"--print=yes"}, // a switch takes no value
	} {
		if _, err := parseArgs(argv); err == nil {
			t.Errorf("%v should be rejected", argv)
		}
	}
}

func TestSplitRulesIgnoresCommasInsideParens(t *testing.T) {
	got := splitRules("Bash(echo a,b),Edit, ,Write(*.md)")
	want := []string{"Bash(echo a,b)", "Edit", "Write(*.md)"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseArgsKeepsPositionalBehavior(t *testing.T) {
	opts, err := parseArgs([]string{"agents", "create", "--name=bug-hunter", "--model", "openai:gpt-6-sol"})
	if err != nil || opts.command != "agents-create" || opts.name != "bug-hunter" || opts.model != "openai:gpt-6-sol" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	opts, err = parseArgs([]string{"install", "opencode", "--project", "--use-env"})
	if err != nil || opts.command != "install" || opts.harness != "opencode" || !opts.project || !opts.useEnv {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	opts, err = parseArgs([]string{"--", "--not-a-flag", "word"})
	if err != nil || opts.prompt != "--not-a-flag word" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}
