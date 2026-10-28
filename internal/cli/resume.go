package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/history"
)

// slashResume lists earlier sessions in this directory and continues one.
// `/resume` shows the list (and, in a terminal, picks from it); `/resume 3`
// continues the third.
func (a *app) slashResume(args []string) bool {
	infos, err := history.List(a.cwd, 12)
	if err != nil {
		a.renderer.Errorf("could not read earlier sessions: %v", err)
		return false
	}
	if len(infos) == 0 {
		a.renderer.Notice("no earlier sessions in this directory")
		return false
	}

	idx := -1
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > len(infos) {
			a.renderer.Errorf("no session %s — /resume lists them (1-%d)", args[0], len(infos))
			return false
		}
		idx = n - 1
	} else {
		now := time.Now()
		options := make([]string, len(infos))
		for i, in := range infos {
			options[i] = fmt.Sprintf("%s  %s  %s", a.theme.Dim(fmt.Sprintf("%-8s", ago(in.Started, now))),
				a.theme.Accent(fmt.Sprintf("%-12s", orDash(in.Agent))), in.Preview)
		}
		if a.repl == nil {
			for i, o := range options {
				a.renderer.Infof("  %d. %s", i+1, o)
			}
			a.renderer.Notice("continue one with /resume <number>")
			return false
		}
		picked, err := a.repl.Pick("Resume which session?", options)
		if err != nil {
			return false
		}
		idx = picked
	}

	in := infos[idx]
	if in.Agent != "" && in.Agent != a.agent.Codename {
		// server memory is per agent: continuing the talk means talking to that agent
		if err := a.setAgentByName(in.Agent, true); err != nil {
			a.renderer.Errorf("%s", friendlyErr(err))
			return false
		}
		a.renderer.Notice("switched to " + in.Agent + " — the agent this session was with")
	}
	session, entries, err := history.Open(in.Path)
	if err != nil {
		a.renderer.Errorf("could not open that session: %v", err)
		return false
	}
	if a.transcript != nil {
		_ = a.transcript.Close()
	}
	a.transcript = session
	a.runner.Reset()
	a.replay(entries)
	return false
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ago renders how long before now t was: "just now", "5m ago", "3h ago",
// "2d ago", "5w ago", "1y ago".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return "?"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dy ago", int(d.Hours()/(24*365)))
	}
}
