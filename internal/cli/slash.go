package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// slashHandler runs one REPL command; it returns true to leave the REPL.
type slashHandler func(a *app, args []string) (quit bool)

// slashHandlers maps command names to their handlers. ui.Commands is the
// menu (what /help, "/" and TAB show); TestEveryMenuCommandHasAHandler keeps
// the two in step. "/quit" is a deliberate alias that has no menu entry.
var slashHandlers = map[string]slashHandler{
	"/exit":        func(a *app, _ []string) bool { return true },
	"/quit":        func(a *app, _ []string) bool { return true },
	"/help":        (*app).slashHelp,
	"/clear":       (*app).slashClear,
	"/agent":       (*app).slashAgent,
	"/agents":      (*app).slashAgents,
	"/model":       (*app).slashModel,
	"/effort":      (*app).slashEffort,
	"/status":      (*app).slashStatus,
	"/verbose":     (*app).slashVerbose,
	"/files":       (*app).slashFiles,
	"/init":        (*app).slashInit,
	"/permissions": (*app).slashPermissions,
	"/yolo":        (*app).slashYolo,
}

// handleSlash processes a REPL command; it returns true to exit.
func (a *app) handleSlash(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	name := strings.ToLower(fields[0])
	handler, ok := slashHandlers[name]
	if !ok {
		hint := "try /help"
		if near := suggestCommand(name); near != "" {
			hint = "did you mean " + near + "?"
		}
		a.renderer.Infof("%s", a.theme.Dim("unknown command "+fields[0]+" — "+hint))
		return false
	}
	return handler(a, fields[1:])
}

// suggestCommand finds the menu command closest to a mistyped name: one it
// is a prefix of, else the nearest by edit distance (at most 2). "" if none.
func suggestCommand(name string) string {
	name = strings.ToLower(name)
	if len(name) < 2 {
		return ""
	}
	for _, c := range ui.Commands {
		if strings.HasPrefix(c.Name, name) {
			return c.Name
		}
	}
	best, bestDist := "", 3
	for _, c := range ui.Commands {
		if d := editDistance(name, c.Name); d < bestDist {
			best, bestDist = c.Name, d
		}
	}
	return best
}

// editDistance is the Damerau-Levenshtein distance (adjacent swaps count
// once), enough for typo suggestions.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

func (a *app) slashHelp(_ []string) bool {
	a.renderer.Infof("%s", ui.HelpText(a.theme))
	return false
}

func (a *app) slashClear(_ []string) bool {
	a.runner.Reset()
	a.renderer.Notice("local task history cleared — the agent's server-side memory is kept")
	return false
}

func (a *app) slashAgent(args []string) bool {
	if len(args) > 0 {
		if strings.EqualFold(args[0], "new") || strings.EqualFold(args[0], "create") {
			if err := a.createAgentInSession(); err != nil {
				a.renderer.Errorf("%s", friendlyErr(err))
			}
			return false
		}
		if err := a.setAgentByName(args[0], true); err != nil {
			a.renderer.Errorf("%s", friendlyErr(err))
		}
		return false
	}
	if err := a.pickAgent(); err != nil {
		a.renderer.Errorf("%s", friendlyErr(err))
	}
	return false
}

func (a *app) slashAgents(_ []string) bool {
	for _, ag := range a.agents {
		marker := "  "
		if ag.Codename == a.agent.Codename {
			marker = a.theme.Green("▸ ")
		}
		a.renderer.Infof("%s%-14s %s", marker, ag.Codename, a.theme.Dim(modelNote(ag)))
	}
	return false
}

// handleModel and handleEffort take the full field list ("/model", args…).
func (a *app) slashModel(args []string) bool {
	if err := a.handleModel(append([]string{"/model"}, args...)); err != nil {
		if errors.Is(err, errCancelled) {
			a.renderer.Notice("model unchanged")
			return false
		}
		a.renderer.Errorf("%s", friendlyErr(err))
	}
	return false
}

func (a *app) slashEffort(args []string) bool {
	if err := a.handleEffort(append([]string{"/effort"}, args...)); err != nil {
		if errors.Is(err, errCancelled) {
			a.renderer.Notice("effort unchanged")
			return false
		}
		a.renderer.Errorf("%s", friendlyErr(err))
	}
	return false
}

func (a *app) slashStatus(_ []string) bool {
	a.renderer.Infof("agent:      %s (%s)", a.agent.Codename, modelNote(a.agent))
	a.renderer.Infof("server:     %s", a.client.BaseURL)
	a.renderer.Infof("api key:    %s", maskKey(a.client.APIKey))
	a.renderer.Infof("directory:  %s", a.cwd)
	a.renderer.Infof("permissions: %s", a.policy.Mode.Description())
	allow, deny := a.policy.Rules()
	for _, r := range allow {
		a.renderer.Infof("  allow     %s", r)
	}
	for _, r := range deny {
		a.renderer.Infof("  deny      %s", r)
	}
	a.renderer.Infof("transcript: %s", a.transcriptPath())
	return false
}

func (a *app) slashVerbose(_ []string) bool {
	a.verbose = !a.verbose
	a.renderer.Notice(fmt.Sprintf("verbose %v", map[bool]string{true: "on", false: "off"}[a.verbose]))
	return false
}

func (a *app) slashFiles(_ []string) bool {
	a.openFileBrowser()
	return false
}

func (a *app) slashPermissions(args []string) bool {
	switch {
	case len(args) > 1:
		a.renderer.Errorf("usage: /permissions [normal|auto|plan|yolo]")
	case len(args) == 0:
		a.setPermissionMode(permissions.NextMode(a.policy.Mode))
	default:
		mode, ok := permissions.ParseMode(args[0])
		if !ok {
			a.renderer.Errorf("unknown permission mode %q — use normal, auto, plan, or yolo", args[0])
			return false
		}
		a.setPermissionMode(mode)
	}
	return false
}

func (a *app) slashYolo(_ []string) bool {
	if a.policy.Mode == permissions.ModeYolo {
		a.setPermissionMode(permissions.ModeAsk)
	} else {
		a.setPermissionMode(permissions.ModeYolo)
	}
	return false
}

// maskKey shows enough of an api key to recognize it and never enough to use
// it: the prefix and last four characters of a long key, only a few leading
// characters of a short one.
func maskKey(key string) string {
	switch {
	case len(key) > 16:
		return key[:6] + "…" + key[len(key)-4:]
	case len(key) > 4:
		return key[:3] + "…"
	default:
		return "…"
	}
}

// initPrompt is what /init sends: the agent writes the instruction file the
// next session will load, from what is actually in the repository.
const initPrompt = `Analyze this repository and write TLC.md at the project root: the project instructions every future session will load.

Include only what helps an engineer (or agent) work here without asking:
- the exact commands to build, test, lint and run (verify each against Makefile, package.json, go.mod, CI config and similar; Do not invent commands you did not find)
- a short map of the important directories and how the pieces fit
- conventions that are not obvious from the code (naming, error handling, test style, generated files)
- gotchas and things that must not be changed

Rules: read before you write; keep it under 60 lines, terse, no filler or generic advice. If TLC.md (or AGENTS.md / CLAUDE.md) already exists, improve it in place instead of replacing it wholesale, and keep what is still true.`

func (a *app) slashInit(_ []string) bool {
	a.runWithCancel(initPrompt)
	// the file is loaded into the system prompt; pick up what was just written
	a.loadInstructions(false)
	a.refreshSystemPrompt()
	return false
}
