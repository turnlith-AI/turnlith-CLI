package cli

import (
	"sort"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// The menu, /help and TAB completion all read ui.Commands; the handlers live
// here. This test is what keeps the two from drifting apart.
func TestEveryMenuCommandHasAHandlerAndViceVersa(t *testing.T) {
	menu := map[string]bool{}
	for _, c := range ui.Commands {
		menu[c.Name] = true
		if _, ok := slashHandlers[c.Name]; !ok {
			t.Errorf("%s is in the menu but has no handler", c.Name)
		}
	}
	var hidden []string
	for name := range slashHandlers {
		if !menu[name] {
			hidden = append(hidden, name)
		}
	}
	sort.Strings(hidden)
	// aliases may exist without a menu entry, but only deliberately
	if len(hidden) != 1 || hidden[0] != "/quit" {
		t.Errorf("handlers missing from the menu: %v (only the /quit alias is allowed)", hidden)
	}
}

func TestSuggestCommand(t *testing.T) {
	cases := map[string]string{
		"/hlep":       "/help",
		"/hepl":       "/help",
		"/mod":        "/model",
		"/perm":       "/permissions",
		"/permisions": "/permissions",
		"/exti":       "/exit",
		"/zzzzzz":     "",
		"/":           "",
	}
	for in, want := range cases {
		if got := suggestCommand(in); got != want {
			t.Errorf("suggestCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUnknownSlashCommandSuggestsTheNearestOne(t *testing.T) {
	a, out := testApp(t, "http://127.0.0.1:1", testAgents())
	if quit := a.handleSlash("/hlep"); quit {
		t.Fatal("an unknown command must not quit")
	}
	mustContain(t, "output", out.String(), "unknown command /hlep")
	mustContain(t, "output", out.String(), "did you mean /help")

	out.Reset()
	a.handleSlash("/zzzzzz")
	mustContain(t, "output", out.String(), "try /help")
}

func TestExitAndQuitBothLeave(t *testing.T) {
	a, _ := testApp(t, "http://127.0.0.1:1", testAgents())
	for _, c := range []string{"/exit", "/quit", "/EXIT"} {
		if !a.handleSlash(c) {
			t.Errorf("%s should quit", c)
		}
	}
}

func TestMaskKeyNeverRevealsAUsableKey(t *testing.T) {
	cases := map[string]string{
		"sk-abcdefghijklmnopqrstuvwxyz1234": "sk-abc…1234",
		"sk-test-key":                       "sk-…",
		"abcd":                              "…",
		"":                                  "…",
	}
	for in, want := range cases {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q, want %q", in, got, want)
		}
	}
}
