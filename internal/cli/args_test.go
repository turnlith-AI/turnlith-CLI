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
	if err != nil || opts.command != "install" || strings.Join(opts.clients, ",") != "opencode" || !opts.project || !opts.useEnv {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	opts, err = parseArgs([]string{"--", "--not-a-flag", "word"})
	if err != nil || opts.prompt != "--not-a-flag word" {
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
