package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// ------------------------------------------------------------ model catalog

// workspaceModels returns this session's cached /v1/models payload, fetching
// it on first use (refresh forces a new fetch after a mutation). nil when the
// server is unreachable — completions simply stay quiet then.
func (a *app) workspaceModels(refresh bool) *api.ModelsResponse {
	a.modelsMu.Lock()
	defer a.modelsMu.Unlock()
	if a.models != nil && !refresh {
		return a.models
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload, err := a.client.ListModels(ctx)
	if err != nil {
		return nil
	}
	a.models = &payload
	return a.models
}

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

// matching filters words by prefix (case-insensitive), preserving order.
func matching(words []string, prefix string) []string {
	if prefix == "" {
		return words
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		if strings.HasPrefix(strings.ToLower(w), strings.ToLower(prefix)) {
			out = append(out, w)
		}
	}
	return out
}

// completeArgument supplies TAB completions for a slash command's arguments
// and for @file mentions in plain prompts.
func (a *app) completeArgument(before, prefix string) []string {
	if strings.HasPrefix(prefix, "@") {
		words := pathCandidates(a.cwd, strings.TrimPrefix(prefix, "@"))
		out := make([]string, len(words))
		for i, w := range words {
			out[i] = "@" + w
		}
		return out
	}
	fields := strings.Fields(before)
	if len(fields) == 0 {
		return nil
	}
	switch fields[0] {
	case "/agent":
		words := []string{"new"}
		for _, ag := range a.agents {
			words = append(words, ag.Codename)
		}
		return matching(words, prefix)
	case "/model":
		models := a.workspaceModels(false)
		if models == nil {
			return nil
		}
		if len(fields) == 1 {
			ids := make([]string, 0, len(models.Models))
			for _, m := range availableModels(models) {
				ids = append(ids, m.ID)
			}
			return matching(ids, prefix)
		}
		return matching(effortsFor(models, fields[1]), prefix)
	case "/effort":
		return matching(effortsFor(a.workspaceModels(false), a.agent.Model), prefix)
	case "/permissions":
		return matching([]string{"normal", "auto", "yolo"}, prefix)
	}
	return nil
}

// ------------------------------------------------------------- prompting

// interactivePrompt returns the REPL to prompt with: the live one inside a
// session, a throwaway one for command mode, or nil when not on a terminal.
// The returned cleanup is always callable.
func (a *app) interactivePrompt() (*ui.REPL, func(), bool) {
	if a.repl != nil {
		return a.repl, func() {}, true
	}
	if !ui.IsTerminal(os.Stdin) || !ui.IsTerminal(os.Stdout) {
		return nil, func() {}, false
	}
	repl, err := ui.NewREPL(a.renderer, a.theme)
	if err != nil {
		return nil, func() {}, false
	}
	return repl, func() { repl.Close() }, true
}

// pickList shows a numbered list and reads the choice. Unlike ui.Pick it also
// accepts the bare key (a codename, a model id) and treats an empty answer as
// a cancellation, never as a default pick.
func (a *app) pickList(repl *ui.REPL, title string, options, keys []string) (int, error) {
	a.renderer.Infof("%s", title)
	for i, opt := range options {
		a.renderer.Infof("  %s %s", a.theme.Accent(fmt.Sprintf("%d.", i+1)), opt)
	}
	for {
		answer, err := repl.Readline()
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
		a.renderer.Infof("%s", a.theme.Dim("pick a number, or type the exact value"))
	}
}

// promptInput asks one line; an empty answer returns "". Ctrl-C cancels.
func (a *app) promptInput(repl *ui.REPL, label, hint string) (string, error) {
	line := label
	if hint != "" {
		line += " " + a.theme.Dim("("+hint+")")
	}
	a.renderer.Infof("%s", line)
	answer, err := repl.Readline()
	if err != nil {
		if errors.Is(err, ui.ErrInterrupt) {
			return "", errCancelled
		}
		return "", err
	}
	return strings.TrimSpace(answer), nil
}

// pickModel shows the available catalog models and returns the chosen id.
func (a *app) pickModel(repl *ui.REPL, models *api.ModelsResponse, current string) (string, error) {
	available := availableModels(models)
	if len(available) == 0 {
		return "", errors.New("no model is available in this workspace — connect a provider in the console first")
	}
	options := make([]string, 0, len(available))
	keys := make([]string, 0, len(available))
	for _, m := range available {
		id := a.theme.Dim("(" + m.ID + ")")
		if m.ID == current {
			id = a.theme.Green("(current)")
		}
		options = append(options, fmt.Sprintf("%s %s", m.Label, id))
		keys = append(keys, m.ID)
	}
	idx, err := a.pickList(repl, "Model:", options, keys)
	if err != nil {
		return "", err
	}
	return available[idx].ID, nil
}

// pickEffort asks for a reasoning level; "" = the provider default. An empty
// answer cancels, leaving the caller's decision in place.
func (a *app) pickEffort(repl *ui.REPL, levels []string, current string) (string, error) {
	options := []string{"provider default " + a.theme.Dim("(nothing sent)")}
	keys := []string{""}
	for _, level := range levels {
		marker := "  "
		if level == current {
			marker = a.theme.Green("▸ ")
		}
		options = append(options, marker+level)
		keys = append(keys, level)
	}
	idx, err := a.pickList(repl, "Reasoning effort:", options, keys)
	if err != nil {
		return "", err
	}
	return keys[idx], nil
}

// resolveEffort settles the reasoning level for a (possibly new) model: an
// explicit request is validated, an interactive session is asked, and anything
// the provider cannot accept is refused with the supported levels. nil = the
// provider default.
func (a *app) resolveEffort(repl *ui.REPL, models *api.ModelsResponse, spec, requested string, interactive bool) (*string, error) {
	levels := effortsFor(models, spec)
	if requested != "" {
		if !containsFold(levels, requested) {
			return nil, effortError(spec, requested, levels)
		}
		return &requested, nil
	}
	if !interactive || repl == nil || len(levels) == 0 {
		return nil, nil
	}
	picked, err := a.pickEffort(repl, levels, "")
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
		a.renderer.Notice("nothing created")
		return 0
	}
	if err != nil {
		a.renderer.Errorf("%s", friendlyErr(err))
		return 1
	}
	a.renderer.Infof("%s created agent %s %s",
		a.theme.Green("✓"),
		a.theme.Bold(created.Codename),
		a.theme.Dim("("+modelNote(created)+")"),
	)
	a.renderer.Infof("%s", a.theme.Dim("call it with `tlc --agent "+created.Codename+"`, or /agent "+created.Codename+" in a session"))
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

	repl, closeRepl, interactive := a.interactivePrompt()
	defer closeRepl()
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
			answer, err := a.promptInput(repl, "Codename — the name clients call:", "enter to generate one")
			if err != nil {
				return api.Agent{}, err
			}
			for answer != "" && !codenameRE.MatchString(answer) {
				a.renderer.Infof("%s", a.theme.Yellow("use 3-63 lowercase letters, digits and hyphens, starting with a letter"))
				answer, err = a.promptInput(repl, "Codename:", "enter to generate one")
				if err != nil {
					return api.Agent{}, err
				}
			}
			input.Codename = answer
		}
		if input.Model == "" {
			chosen, err := a.pickModel(repl, &models, "")
			if err != nil {
				return api.Agent{}, err
			}
			input.Model = chosen
		}
		if input.Label == "" {
			answer, err := a.promptInput(repl, "Label:", "optional, enter to skip")
			if err != nil && !errors.Is(err, errCancelled) {
				return api.Agent{}, err
			}
			input.Label = answer
		}
	}
	if input.Model == "" {
		return api.Agent{}, errors.New("no model selected")
	}

	effort, err := a.resolveEffort(repl, &models, input.Model, input.ReasoningEffort, interactive)
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
	a.invalidateModels()
	a.agents = append(a.agents, created)
	return created, nil
}

// createAgentInSession is the REPL's `/agent new`: create a sibling agent and
// switch to it.
func (a *app) createAgentInSession() error {
	created, err := a.runAgentCreate()
	if errors.Is(err, errCancelled) {
		a.renderer.Notice("nothing created")
		return nil
	}
	if err != nil {
		return err
	}
	a.renderer.Infof("%s created agent %s %s", a.theme.Green("✓"), a.theme.Bold(created.Codename), a.theme.Dim("("+modelNote(created)+")"))
	return a.setAgentByName(created.Codename, true)
}

// ------------------------------------------------------------ /model, /effort

// handleModel switches the current agent's model and reasoning effort. With no
// arguments it opens a picker (interactive sessions only); `/model <spec>
// [level]` works everywhere. The change is persisted on the agent, mirrored
// onto its live session, and applies to every client of that agent.
func (a *app) handleModel(fields []string) error {
	models := a.workspaceModels(true) // refresh: availability may have moved
	if models == nil {
		return errors.New("could not load the model catalog from the server")
	}
	var spec, requested string
	switch len(fields) {
	case 1:
		repl, closeRepl, interactive := a.interactivePrompt()
		defer closeRepl()
		if !interactive || repl == nil {
			return fmt.Errorf("use /model <spec> [effort] — the current model is %s", a.agent.Model)
		}
		chosen, err := a.pickModel(repl, models, a.agent.Model)
		if err != nil {
			return err
		}
		spec = chosen
	case 2, 3:
		spec = fields[1]
		if len(fields) == 3 {
			requested = strings.ToLower(fields[2])
		}
	default:
		return errors.New("usage: /model [spec] [effort]")
	}
	return a.chooseConfig(models, spec, requested, len(fields) < 3)
}

// handleEffort sets just the reasoning level on the current agent.
func (a *app) handleEffort(fields []string) error {
	models := a.workspaceModels(true)
	if models == nil {
		return errors.New("could not load the model catalog from the server")
	}
	levels := effortsFor(models, a.agent.Model)
	if len(levels) == 0 {
		return fmt.Errorf("the provider behind %s has no reasoning control", a.agent.Model)
	}
	var requested string
	switch len(fields) {
	case 1:
		repl, closeRepl, interactive := a.interactivePrompt()
		defer closeRepl()
		if !interactive || repl == nil {
			return fmt.Errorf("use /effort <level> — %s accepts: %s", a.agent.Model, strings.Join(levels, ", "))
		}
		picked, err := a.pickEffort(repl, levels, currentEffort(a.agent))
		if err != nil {
			if errors.Is(err, errCancelled) {
				return nil
			}
			return err
		}
		requested = pickEffortArg(picked)
	case 2:
		requested = fields[1]
	default:
		return errors.New("usage: /effort [level | default]")
	}
	return a.chooseConfig(models, a.agent.Model, requested, false)
}

// pickEffortArg interprets a /effort picker result: the picker's "provider
// default" entry comes back as "", which downstream would read as "no argument
// given" and keep the current level — so it is spelled back out explicitly.
func pickEffortArg(picked string) string {
	if picked == "" {
		return "default"
	}
	return picked
}

// effortArg reads a /model or /effort level argument: "default" and "off" mean
// the provider default, spelled out explicitly; "" means the caller said
// nothing (which lets a still-supported level survive).
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

// chooseConfig settles a model + effort pair and persists it. A level that was
// not spelled out survives the switch when the new provider still accepts it
// (otherwise it returns to the provider default); offerPicker asks for the
// level interactively on the picker path.
func (a *app) chooseConfig(models *api.ModelsResponse, spec, rawEffort string, offerPicker bool) error {
	level, explicit := effortArg(rawEffort)
	levels := effortsFor(models, spec)
	if !explicit {
		if current := currentEffort(a.agent); containsFold(levels, current) {
			level = current
		}
	}
	if offerPicker {
		repl, closeRepl, interactive := a.interactivePrompt()
		defer closeRepl()
		if interactive && repl != nil && len(levels) > 0 {
			picked, err := a.pickEffort(repl, levels, level)
			if err != nil && !errors.Is(err, errCancelled) {
				return err
			}
			if err == nil {
				level = picked
			}
		}
	}
	return a.applyModel(models, spec, level)
}

// applyModel validates and persists a model+effort pair on the current agent.
func (a *app) applyModel(models *api.ModelsResponse, spec, requested string) error {
	for _, m := range models.Models {
		if m.ID == spec && !m.Available {
			return fmt.Errorf("model %s is not available in this workspace — connect its provider in the console", spec)
		}
	}
	levels := effortsFor(models, spec)
	if requested != "" && !containsFold(levels, requested) {
		return effortError(spec, requested, levels)
	}
	var effort *string
	if requested != "" {
		effort = &requested
	}

	agentID := a.agent.ID
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	updated, err := a.client.UpdateAgentConfig(ctx, agentID, api.AgentConfigPatch{Model: spec, ReasoningEffort: effort})
	if err != nil {
		return err
	}
	for i, ag := range a.agents {
		if ag.ID == agentID {
			a.agents[i] = updated
		}
	}
	a.invalidateModels()
	if err := a.setAgentByName(updated.Codename, false); err != nil {
		return err
	}
	a.renderer.Notice(fmt.Sprintf("now on %s%s — applied to every client of %s",
		a.theme.Accent(updated.Model), effortNote(updated.ReasoningEffort), updated.Codename))
	return nil
}

// invalidateModels drops the cached /v1/models payload after a mutation.
func (a *app) invalidateModels() {
	a.modelsMu.Lock()
	a.models = nil
	a.modelsMu.Unlock()
}

// ------------------------------------------------------------ effort helpers

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

// effortNote renders a level for messages; nil says provider default.
func effortNote(effort *string) string {
	if effort == nil || *effort == "" {
		return " · provider default"
	}
	return " · effort " + *effort
}

// modelNote renders a model for menus and tables, with the effort when set.
func modelNote(ag api.Agent) string {
	if e := currentEffort(ag); e != "" {
		return ag.Model + " · effort " + e
	}
	return ag.Model
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}
