// tlc install / tlc uninstall: wire the server's agents into a local AI
// harness. First integration: opencode, whose config gains a turnlith
// provider (the agents as models) and the turnlith-controller MCP server
// for interactive memory search.
package cli

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/harness"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// resolveHarness picks the harness to act on: the named one, or the first
// one detected on PATH.
func resolveHarness(renderer *ui.Renderer, named string) (string, bool) {
	if named == "" {
		if _, err := exec.LookPath("opencode"); err == nil {
			return "opencode", true
		}
		renderer.Errorf("no harness specified and none detected on PATH — try: tlc install opencode")
		return "", false
	}
	if named != "opencode" {
		renderer.Errorf("unknown harness %q — supported: opencode", named)
		return "", false
	}
	return named, true
}

func runInstall(opts options) int {
	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)

	harnessName, ok := resolveHarness(renderer, opts.harness)
	if !ok {
		return 2
	}

	conn, err := config.LoadConnection()
	if err != nil {
		renderer.Errorf("%s", friendlyErr(err))
		return 1
	}
	if opts.baseURL != "" {
		conn.BaseURL = config.NormalizeBaseURL(opts.baseURL)
	}
	if opts.apiKey != "" {
		conn.APIKey = strings.TrimSpace(opts.apiKey)
	}
	client := api.New(conn.BaseURL, conn.APIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	agents, err := client.ListAgents(ctx)
	if err != nil {
		renderer.Errorf("%s", friendlyErr(err))
		return 1
	}

	providerID := opts.providerID
	if providerID == "" {
		providerID = harness.DefaultProviderID
	}
	models := make([]harness.AgentModel, 0, len(agents))
	for _, ag := range agents {
		label := ""
		if ag.Label != nil {
			label = strings.TrimSpace(*ag.Label)
		}
		models = append(models, harness.AgentModel{Codename: ag.Codename, Label: label})
	}
	installOpts := harness.InstallOptions{
		Project:    opts.project,
		ProviderID: providerID,
		Block: harness.BuildProviderBlock(harness.ProviderInput{
			BaseURL: conn.BaseURL,
			APIKey:  conn.APIKey,
			UseEnv:  opts.useEnv,
			Agents:  models,
		}),
	}
	withMcp := !opts.noMcp
	if withMcp {
		mcpBlock := harness.BuildMcpBlock(harness.MCPInput{
			BaseURL: conn.BaseURL,
			APIKey:  conn.APIKey,
			UseEnv:  opts.useEnv,
		})
		installOpts.MCPServerID = harness.DefaultMCPServerID
		installOpts.MCPBlock = &mcpBlock
	}

	result, err := harness.InstallOpencode(installOpts)
	if err != nil {
		renderer.Errorf("could not write the %s config: %v", harnessName, err)
		return 1
	}

	state := "updated"
	if result.Created {
		state = "created"
	}
	renderer.Infof("%s %s", theme.Dim("opencode config:"), result.Path+" ("+state+")")
	renderer.Infof("%s %s", theme.Dim("provider:"), providerID+" → "+conn.BaseURL+"/v1")
	if withMcp {
		renderer.Infof("%s %s", theme.Dim("mcp server:"),
			harness.DefaultMCPServerID+" → "+conn.BaseURL+"/v1/mcp (memory search tools)")
	}
	renderer.Infof("%s %d", theme.Dim("agents:"), result.AgentCount)
	for _, m := range models {
		line := "  - " + m.Codename
		if m.Label != "" {
			line += " (" + m.Label + ")"
		}
		renderer.Infof("%s", theme.Dim(line))
	}
	if result.AgentCount == 0 {
		renderer.Infof("%s", theme.Yellow("no agents on the server yet — rerun `tlc install opencode` after creating one"))
	}
	if opts.useEnv {
		renderer.Infof("%s %s", theme.Dim("api key:"),
			"{env:"+config.EnvAPIKey+"} — export "+config.EnvAPIKey+"=<key> in your shell")
	}
	renderer.Infof("%s", theme.Dim(`run "opencode" and pick an agent under "turnlith" in /models`))
	return 0
}

func runUninstall(opts options) int {
	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)

	// uninstall always knows what to undo — no PATH detection needed
	harnessName := opts.harness
	if harnessName == "" {
		harnessName = "opencode"
	}
	providerID := opts.providerID
	if providerID == "" {
		providerID = harness.DefaultProviderID
	}

	outcome, err := harness.UninstallOpencode(harness.UninstallOptions{
		Project:     opts.project,
		ProviderID:  providerID,
		MCPServerID: harness.DefaultMCPServerID,
	})
	if err != nil {
		renderer.Errorf("could not update the %s config: %v", harnessName, err)
		return 1
	}
	switch outcome {
	case harness.OutcomeRemoved:
		renderer.Infof("Removed the %q provider and the MCP server from the opencode config.", providerID)
	case harness.OutcomeNotInstalled:
		renderer.Infof("The %q provider is not in the opencode config — nothing to remove.", providerID)
	default:
		renderer.Infof("No opencode config found — nothing to remove.")
	}
	return 0
}
