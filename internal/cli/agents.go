package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// codenameRE mirrors the server's codename rules: 3-63 chars, lowercase
// letters/digits/hyphens, letter-first, colon-free so a codename can never
// parse as a provider-qualified model id.
var codenameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{1,61}[a-z0-9]$`)

// errCancelled reports a picker or prompt dismissed with an empty answer.
var errCancelled = errors.New("cancelled")

// ------------------------------------------------------------- tlc agents

// cmdAgents prints the workspace's agents as a table.
func (a *app) cmdAgents() int {
	nameWidth := len("CODENAME")
	labelWidth := len("LABEL")
	for _, ag := range a.agents {
		nameWidth = max(nameWidth, len(ui.SanitizeInline(ag.Codename)))
		labelWidth = max(labelWidth, len(label(ag)))
	}
	a.con.out.Infof("%-*s  %-*s  %s", nameWidth, "CODENAME", labelWidth, "LABEL", "MODEL")
	for _, ag := range a.agents {
		a.con.out.Infof("%-*s  %-*s  %s", nameWidth, ui.SanitizeInline(ag.Codename), labelWidth, label(ag), modelNote(ag))
	}
	return 0
}

// ------------------------------------------------------------ model catalog

// effortsFor returns the reasoning levels a model's provider accepts (empty
// when the provider has no reasoning control, or the server did not say).
func effortsFor(models *api.ModelsResponse, spec string) []string {
	if models == nil {
		return nil
	}
	provider := spec
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		provider = spec[:i]
	}
	for _, p := range models.Providers {
		if p.Provider == provider {
			return p.ReasoningEfforts
		}
	}
	return nil
}

// availableModels lists the catalog entries callable in this workspace now.
func availableModels(models *api.ModelsResponse) []api.CatalogModel {
	if models == nil {
		return nil
	}
	list := make([]api.CatalogModel, 0, len(models.Models))
	for _, m := range models.Models {
		if m.Available {
			list = append(list, m)
		}
	}
	return list
}

// ------------------------------------------------------------- prompting

// pickList shows a numbered list and reads the choice. Unlike ui.Prompter.Pick
// it also accepts the bare key (a model id) and treats an empty answer as a
// cancellation, never as a default pick.
func (a *app) pickList(p *ui.Prompter, title string, options, keys []string) (int, error) {
	a.con.out.Infof("%s", title)
	for i, opt := range options {
		a.con.out.Infof("  %s %s", a.con.theme.Accent(fmt.Sprintf("%d.", i+1)), opt)
	}
	for {
		answer, err := p.Readline()
		if err != nil {
			if errors.Is(err, ui.ErrInterrupt) {
				return -1, errCancelled
			}
			return -1, err
		}
		answer = strings.TrimSpace(answer)
		if answer == "" {
			return -1, errCancelled
		}
		if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		for i, key := range keys {
			if key != "" && strings.EqualFold(answer, key) {
				return i, nil
			}
		}
		a.con.out.Infof("%s", a.con.theme.Dim("pick a number, or type the exact value"))
	}
}

// promptInput asks one line; an empty answer returns "". Ctrl-C cancels.
func (a *app) promptInput(p *ui.Prompter, label, hint string) (string, error) {
	line := label
	if hint != "" {
		line += " " + a.con.theme.Dim("("+hint+")")
	}
	a.con.out.Infof("%s", line)
	answer, err := p.Readline()
	if err != nil {
		if errors.Is(err, ui.ErrInterrupt) {
			return "", errCancelled
		}
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

// pickModel shows the available catalog models and returns the chosen id.
func (a *app) pickModel(p *ui.Prompter, models *api.ModelsResponse) (string, error) {
	available := availableModels(models)
	if len(available) == 0 {
		return "", errors.New("no model is available in this workspace — connect a provider in the console first")
	}
	options := make([]string, 0, len(available))
	keys := make([]string, 0, len(available))
	for _, m := range available {
		options = append(options, fmt.Sprintf("%s %s", ui.SanitizeInline(m.Label), a.con.theme.Dim("("+ui.SanitizeInline(m.ID)+")")))
		keys = append(keys, m.ID)
	}
	idx, err := a.pickList(p, "Model:", options, keys)
	if err != nil {
		return "", err
	}
	return available[idx].ID, nil
}

// pickEffort asks for a reasoning level; "" = the provider default. An empty
// answer cancels, leaving the caller's decision in place.
func (a *app) pickEffort(p *ui.Prompter, levels []string) (string, error) {
	options := []string{"provider default " + a.con.theme.Dim("(nothing sent)")}
	keys := []string{""}
	for _, level := range levels {
		options = append(options, level)
		keys = append(keys, level)
	}
	idx, err := a.pickList(p, "Reasoning effort:", options, keys)
	if err != nil {
		return "", err
	}
	return keys[idx], nil
}

// resolveEffort settles the reasoning level for a new agent: an explicit
// request is validated, an interactive run is asked, and anything the provider
// cannot accept is refused with the supported levels. nil = the provider
// default.
func (a *app) resolveEffort(p *ui.Prompter, models *api.ModelsResponse, spec, requested string) (*string, error) {
	levels := effortsFor(models, spec)
	if requested != "" {
		if !containsFold(levels, requested) {
			return nil, effortError(spec, requested, levels)
		}
		return &requested, nil
	}
	if p == nil || len(levels) == 0 {
		return nil, nil
	}
	picked, err := a.pickEffort(p, levels)
	if err != nil {
		if errors.Is(err, errCancelled) {
			return nil, nil
		}
		return nil, err
	}
	if picked == "" {
		return nil, nil
	}
	return &picked, nil
}

// ------------------------------------------------------------ agents create

// cmdAgentsCreate implements `tlc agents create`: an interactive setup on a
// terminal, flag-driven otherwise.
func (a *app) cmdAgentsCreate() int {
	created, err := a.runAgentCreate()
	if errors.Is(err, errCancelled) {
		a.con.out.Notice("nothing created")
		return 0
	}
	if err != nil {
		a.con.err.Errorf("%s", friendlyErr(err))
		return 1
	}
	a.con.out.Infof("%s created agent %s %s",
		a.con.theme.Green("✓"),
		a.con.theme.Bold(ui.SanitizeInline(created.Codename)),
		a.con.theme.Dim("("+modelNote(created)+")"),
	)
	a.con.out.Infof("%s", a.con.theme.Dim("run `tlc install` to add it to your coding clients"))
	return 0
}

// runAgentCreate walks the create flow — prompting on a terminal for whatever
// the flags left open — and posts the agent. Agent management is admin-gated
// server side; a 403 is translated by friendlyErr.
func (a *app) runAgentCreate() (api.Agent, error) {
	input := api.CreateAgentInput{
		Codename:        strings.TrimSpace(a.opts.name),
		Label:           strings.TrimSpace(a.opts.label),
		Model:           strings.TrimSpace(a.opts.model),
		ReasoningEffort: strings.ToLower(strings.TrimSpace(a.opts.effort)),
	}
	if input.Codename != "" && !codenameRE.MatchString(input.Codename) {
		return api.Agent{}, fmt.Errorf(
			"codename %q is not usable — 3-63 lowercase letters, digits and hyphens, starting with a letter", input.Codename)
	}
	if level, _ := effortArg(input.ReasoningEffort); level == "" {
		input.ReasoningEffort = ""
	}

	prompter, err := a.prompt()
	if err != nil && !errors.Is(err, errNoTerminal) {
		return api.Agent{}, err
	}
	interactive := prompter != nil
	if !interactive && input.Model == "" {
		return api.Agent{}, errors.New("--model is required when not on a terminal, e.g. --model gemini:gemini-3.8-flash")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	models, err := a.client.ListModels(ctx)
	if err != nil {
		return api.Agent{}, err
	}

	if interactive {
		if input.Codename == "" {
			answer, err := a.promptInput(prompter, "Codename — the name clients call:", "enter to generate one")
			if err != nil {
				return api.Agent{}, err
			}
			for answer != "" && !codenameRE.MatchString(answer) {
				a.con.out.Infof("%s", a.con.theme.Yellow("use 3-63 lowercase letters, digits and hyphens, starting with a letter"))
				answer, err = a.promptInput(prompter, "Codename:", "enter to generate one")
				if err != nil {
					return api.Agent{}, err
				}
			}
			input.Codename = answer
		}
		if input.Model == "" {
			chosen, err := a.pickModel(prompter, &models)
			if err != nil {
				return api.Agent{}, err
			}
			input.Model = chosen
		}
		if input.Label == "" {
			answer, err := a.promptInput(prompter, "Label:", "optional, enter to skip")
			if err != nil && !errors.Is(err, errCancelled) {
				return api.Agent{}, err
			}
			input.Label = answer
		}
	}
	if input.Model == "" {
		return api.Agent{}, errors.New("no model selected")
	}
	for _, m := range models.Models {
		if m.ID == input.Model && !m.Available {
			return api.Agent{}, fmt.Errorf("model %s is not available in this workspace — connect its provider in the console", input.Model)
		}
	}

	effort, err := a.resolveEffort(prompter, &models, input.Model, input.ReasoningEffort)
	if err != nil {
		return api.Agent{}, err
	}
	if effort != nil {
		input.ReasoningEffort = *effort
	}

	created, err := a.client.CreateAgent(ctx, input)
	if err != nil {
		return api.Agent{}, err
	}
	a.agents = append(a.agents, created)
	return created, nil
}

// ------------------------------------------------------------ effort helpers

// effortArg reads an effort argument: "default" and "off" mean the provider
// default, spelled out explicitly; "" means nothing was said.
func effortArg(raw string) (level string, explicit bool) {
	switch raw = strings.ToLower(strings.TrimSpace(raw)); raw {
	case "":
		return "", false
	case "default", "off":
		return "", true
	default:
		return raw, true
	}
}

// effortError explains a level the provider cannot accept.
func effortError(spec, requested string, levels []string) error {
	if len(levels) == 0 {
		return fmt.Errorf("the provider behind %s has no reasoning control — %q cannot be set", spec, requested)
	}
	return fmt.Errorf("effort %q is not accepted by %s — supported: %s", requested, spec, strings.Join(levels, ", "))
}

// currentEffort is the agent's stored level ("" = provider default).
func currentEffort(ag api.Agent) string {
	if ag.ReasoningEffort == nil {
		return ""
	}
	return *ag.ReasoningEffort
}

// modelNote renders a model for menus and tables, with the effort when set.
func modelNote(ag api.Agent) string {
	if e := currentEffort(ag); e != "" {
		return ui.SanitizeInline(ag.Model) + " · effort " + ui.SanitizeInline(e)
	}
	return ui.SanitizeInline(ag.Model)
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}
