package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func longFlags() []string {
	var out []string
	for name := range boolFlags {
		if strings.HasPrefix(name, "--") {
			out = append(out, name)
		}
	}
	for name := range valueFlags {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func TestEveryFlagHasACompletionDescription(t *testing.T) {
	for _, f := range longFlags() {
		if flagHelp[f] == "" {
			t.Errorf("%s has no entry in flagHelp (used by zsh/fish completion)", f)
		}
	}
	for f := range flagHelp {
		if _, b := boolFlags[f]; !b {
			if _, v := valueFlags[f]; !v {
				t.Errorf("flagHelp documents %s, which is not a flag", f)
			}
		}
	}
}

func TestCompletionScriptsListEveryFlagAndCommand(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		script, err := completionScript(shell)
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, f := range longFlags() {
			bare := strings.TrimPrefix(f, "--")
			if !strings.Contains(script, f) && !(shell == "fish" && strings.Contains(script, "-l "+bare)) {
				t.Errorf("%s completion lacks %s", shell, f)
			}
		}
		for _, c := range []string{"agents", "configure", "doctor", "install", "uninstall", "completion"} {
			if !strings.Contains(script, c) {
				t.Errorf("%s completion lacks command %s", shell, c)
			}
		}
		for _, v := range []string{"stream-json", "plan", "xhigh"} {
			if !strings.Contains(script, v) {
				t.Errorf("%s completion lacks the value %s", shell, v)
			}
		}
	}
	if _, err := completionScript("powershell"); err == nil {
		t.Fatal("unknown shell should be an error")
	}
}

func TestBashCompletionIsValidShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}
	script, _ := completionScript("bash")
	path := filepath.Join(t.TempDir(), "tlc.bash")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bash, "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}
	// and it really completes: source it and ask for "--perm"
	probe := `source ` + path + `; COMP_WORDS=(tlc --perm); COMP_CWORD=1; _tlc; printf '%s\n' "${COMPREPLY[@]}"`
	out, err := exec.Command(bash, "-c", probe).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--permission-mode") {
		t.Fatalf("completing --perm gave %q (%v)", out, err)
	}
	probe = `source ` + path + `; COMP_WORDS=(tlc --permission-mode ""); COMP_CWORD=2; _tlc; printf '%s\n' "${COMPREPLY[@]}"`
	out, _ = exec.Command(bash, "-c", probe).CombinedOutput()
	if !strings.Contains(string(out), "plan") || !strings.Contains(string(out), "yolo") {
		t.Fatalf("mode values: %q", out)
	}
}

func TestCompletionCommand(t *testing.T) {
	res := runTLC(t, t.TempDir(), "completion", "bash")
	if res.code != 0 || !strings.Contains(res.stdout, "complete -F _tlc tlc") {
		t.Fatalf("code=%d stdout=%.80q", res.code, res.stdout)
	}
	if res = runTLC(t, t.TempDir(), "completion", "tcsh"); res.code != 2 || !strings.Contains(res.stderr, "bash, zsh, fish") {
		t.Fatalf("unknown shell: code=%d stderr=%q", res.code, res.stderr)
	}
	if res = runTLC(t, t.TempDir(), "completion"); res.code != 2 || !strings.Contains(res.stderr, "usage") {
		t.Fatalf("no shell: code=%d stderr=%q", res.code, res.stderr)
	}
}

func TestPerCommandHelp(t *testing.T) {
	cases := map[string][]string{
		"agents":     {"tlc agents create", "--effort"},
		"configure":  {"--api-key-stdin", "https://"},
		"install":    {"opencode", "--use-env"},
		"doctor":     {"connection", "Exit status"},
		"completion": {"bash", "zsh", "fish"},
	}
	for topic, wants := range cases {
		for _, argv := range [][]string{{topic, "--help"}, {topic, "-h"}, {"help", topic}} {
			res := runTLC(t, t.TempDir(), argv...)
			if res.code != 0 {
				t.Errorf("%v: code=%d stderr=%q", argv, res.code, res.stderr)
				continue
			}
			for _, w := range wants {
				if !strings.Contains(res.stdout, w) {
					t.Errorf("%v: help lacks %q:\n%s", argv, w, res.stdout)
				}
			}
			if strings.Contains(res.stdout, "Options for `tlc install opencode`") && topic != "install" {
				t.Errorf("%v printed the global help instead of the %s topic", argv, topic)
			}
		}
	}
	res := runTLC(t, t.TempDir(), "--help")
	if res.code != 0 || !strings.Contains(res.stdout, "Usage:") {
		t.Fatalf("global help: %d %q", res.code, res.stdout)
	}
}
