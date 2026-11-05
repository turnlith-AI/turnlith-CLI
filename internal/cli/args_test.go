package cli

import (
	"errors"
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

func TestEveryRemovedFlagIsNotAlsoLive(t *testing.T) {
	for name := range removedFlags {
		if _, ok := boolFlags[name]; ok {
			t.Errorf("%s is both removed and live", name)
		}
		if _, ok := valueFlags[name]; ok {
			t.Errorf("%s is both removed and live", name)
		}
	}
}

func TestParseArgsRejectsBadInvocations(t *testing.T) {
	for _, argv := range [][]string{
		{"configure", "--base-url"}, // value missing
		{"--bogus"},
		{"doctor", "--list=yes"},            // a switch takes no value
		{"configure", "extra"},              // takes no argument
		{"doctor", "now"},                   // takes no argument
		{"version", "-p"},                   // removed flags win over -v
		{"completion", "bash", "zsh"},       // one shell only
		{"help", "nonsense"},                // no such topic
		{"agents", "create", "a", "prompt"}, // stray words
		{"-p", "hello"},
		{"hello"},
	} {
		_, err := parseArgs(argv)
		var usage *usageError
		if err == nil || !errors.As(err, &usage) {
			t.Errorf("%v should be a usage error, got %v", argv, err)
		}
	}
}

func TestParseArgsCommandsAndHelp(t *testing.T) {
	cases := []struct {
		argv    []string
		command string
		topic   string
	}{
		{nil, "help", ""},
		{[]string{"help"}, "help", ""},
		{[]string{"help", "install"}, "help", "install"},
		{[]string{"-h"}, "help", ""},
		{[]string{"agents", "--help"}, "help", "agents"},
		{[]string{"agents", "create", "-h"}, "help", "agents"},
		{[]string{"install", "cursor", "--help"}, "help", "install"},
		{[]string{"-v"}, "version", ""},
		{[]string{"version"}, "version", ""},
		{[]string{"--version", "doctor"}, "version", ""},
		{[]string{"configure"}, "configure", ""},
		{[]string{"doctor"}, "doctor", ""},
		{[]string{"--base-url", "https://x", "configure"}, "configure", ""}, // flags may come first
		{[]string{"completion", "zsh"}, "completion", ""},
	}
	for _, c := range cases {
		opts, err := parseArgs(c.argv)
		if err != nil {
			t.Errorf("%v: %v", c.argv, err)
			continue
		}
		if opts.command != c.command || opts.helpTopic != c.topic {
			t.Errorf("%v: command=%q topic=%q, want %q %q", c.argv, opts.command, opts.helpTopic, c.command, c.topic)
		}
	}
	if opts, _ := parseArgs([]string{"completion", "fish"}); opts.shell != "fish" {
		t.Errorf("shell = %q", opts.shell)
	}
}

func TestUnknownCommandSuggestions(t *testing.T) {
	for typo, want := range map[string]string{
		"agnts": "agents", "instal": "install", "unistall": "uninstall", "docter": "doctor",
		"configur": "configure", "completon": "completion",
	} {
		_, err := parseArgs([]string{typo})
		if err == nil || !strings.Contains(err.Error(), "`tlc "+want+"`") {
			t.Errorf("%q: err = %v, want a suggestion for %q", typo, err, want)
		}
	}
	if _, err := parseArgs([]string{"zzzzzz"}); err == nil || strings.Contains(err.Error(), "did you mean") {
		t.Errorf("a word nothing resembles must not get a suggestion: %v", err)
	}
	if got := editDistance("kitten", "sitting"); got != 3 {
		t.Errorf("editDistance = %d", got)
	}
}

func TestParseArgsDoubleDashEndsFlags(t *testing.T) {
	// after "--" nothing is a flag, so "--list" is just a (bad) client name
	if _, err := parseArgs([]string{"install", "--", "--list"}); err == nil {
		t.Fatal("--list after -- must be treated as a word")
	}
}

func TestParseArgsKeepsPositionalBehavior(t *testing.T) {
	opts, err := parseArgs([]string{"agents", "create", "--name=bug-hunter", "--model", "openai:gpt-6-sol"})
	if err != nil || opts.command != "agents-create" || opts.name != "bug-hunter" || opts.model != "openai:gpt-6-sol" {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	opts, err = parseArgs([]string{"install", "opencode", "--project", "--use-env"})
	if err != nil || opts.command != "install" || strings.Join(opts.clients, ",") != "opencode" || !opts.project || !opts.useEnv {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
}

func TestParseArgsInstallClients(t *testing.T) {
	ok := map[string]string{
		"install":                        "",
		"install cursor pi":              "cursor,pi",
		"uninstall Pi-Agent cursor":      "pi,cursor",
		"install cursor cursor":          "cursor",
		"install all":                    "opencode,pi,continue,cursor,cline",
		"install opencode all":           "opencode,pi,continue,cursor,cline",
		"install continue.dev --project": "continue",
		"install --list":                 "",
		"install --if-detected":          "",
		"install cline --no-mcp":         "cline",
		"install --use-env cursor cline": "cursor,cline",
		"uninstall --provider-id x pi":   "pi",
	}
	for argv, want := range ok {
		opts, err := parseArgs(strings.Fields(argv))
		if err != nil || strings.Join(opts.clients, ",") != want {
			t.Errorf("%q: clients=%v err=%v, want %q", argv, opts.clients, err, want)
		}
	}
	opts, err := parseArgs([]string{"install", "--list", "--if-detected"})
	if err != nil || !opts.list || !opts.ifDetected {
		t.Errorf("flags not parsed: %+v err=%v", opts, err)
	}
	_, err = parseArgs([]string{"install", "zed"})
	if err == nil || !strings.Contains(err.Error(), `unknown client "zed"`) || !strings.Contains(err.Error(), "opencode, pi, continue, cursor, cline") {
		t.Errorf("unknown client error = %v, want the supported list", err)
	}
	if _, err := parseArgs([]string{"install", "cursor", "zed"}); err == nil {
		t.Error("an unknown name after a valid one should still fail")
	}
}
