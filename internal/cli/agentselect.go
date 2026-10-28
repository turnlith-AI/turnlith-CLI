package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

func (a *app) cmdAgents() int {
	nameWidth := len("CODENAME")
	labelWidth := len("LABEL")
	for _, ag := range a.agents {
		nameWidth = max(nameWidth, len(ag.Codename))
		labelWidth = max(labelWidth, len(label(ag)))
	}
	a.renderer.Infof("%-*s  %-*s  %s", nameWidth, "CODENAME", labelWidth, "LABEL", "MODEL")
	for _, ag := range a.agents {
		a.renderer.Infof("%-*s  %-*s  %s", nameWidth, ag.Codename, labelWidth, label(ag), modelNote(ag))
	}
	return 0
}

func (a *app) chooseAgent(interactive bool) error {
	if a.opts.agent != "" {
		return a.setAgentByName(a.opts.agent, true)
	}
	if env := strings.TrimSpace(os.Getenv(config.EnvAgent)); env != "" {
		return a.setAgentByName(env, false)
	}
	prefs := config.LoadPrefs()
	if prefs.DefaultAgent != "" {
		if err := a.setAgentByName(prefs.DefaultAgent, false); err == nil {
			return nil
		}
	}
	if len(a.agents) == 1 {
		return a.setAgentByName(a.agents[0].Codename, false)
	}
	if !interactive {
		return fmt.Errorf("this workspace has %d agents — choose one with --agent (see `tlc agents`)", len(a.agents))
	}
	return a.pickAgent()
}

func (a *app) pickAgent() error {
	options := make([]string, 0, len(a.agents))
	for _, ag := range a.agents {
		opt := ag.Codename
		if l := label(ag); l != "-" {
			opt += " — " + l
		}
		opt += a.theme.Dim(" (" + modelNote(ag) + ")")
		options = append(options, opt)
	}
	idx, err := a.repl.Pick("Select an agent:", options)
	if err != nil {
		return err
	}
	return a.setAgentByName(a.agents[idx].Codename, true)
}

func (a *app) setAgentByName(name string, remember bool) error {
	for _, ag := range a.agents {
		if strings.EqualFold(ag.Codename, name) {
			changed := a.agent.Codename != "" && a.agent.Codename != ag.Codename
			a.agent = ag
			a.runner.SetAgent(ag.Codename)
			a.refreshSystemPrompt()
			if remember {
				prefs := config.LoadPrefs()
				prefs.DefaultAgent = ag.Codename
				_ = config.SavePrefs(prefs)
			}
			if changed && a.repl != nil {
				a.renderer.Notice("agent switched to " + ag.Codename + " — memory is per agent")
			}
			return nil
		}
	}
	names := make([]string, len(a.agents))
	for i, ag := range a.agents {
		names[i] = ag.Codename
	}
	return fmt.Errorf("unknown agent %q — this workspace has: %s", name, strings.Join(names, ", "))
}
