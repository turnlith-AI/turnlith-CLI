// tlc install / tlc uninstall: wire the server's agents into local coding
// clients. Each client (opencode, Pi, Continue, Cursor, Cline) is a registry
// entry in the harness package; this file picks the clients, checks the flags
// against what each one supports, and reports.
package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/harness"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// selection is the clients an install/uninstall acts on, and the ones it
// leaves alone because a flag asks for something they cannot do.
type selection struct {
	run     []harness.Client
	skipped []skippedClient
}

type skippedClient struct {
	client harness.Client
	why    string
}

// pickClients resolves the clients to act on: the named ones, or — with none
// named — every detected client (install) or every client holding turnlith
// entries (uninstall). An explicit name that conflicts with a flag is a usage
// error; the same conflict while auto-selecting just skips that client.
func pickClients(opts options, uninstall bool) (selection, bool) {
	explicit := len(opts.clients) > 0
	var candidates []harness.Client
	if explicit {
		for _, id := range opts.clients {
			c, _ := harness.Lookup(id)
			candidates = append(candidates, c)
		}
	} else {
		for _, c := range harness.Registry() {
			if uninstall && (opts.project && !c.Caps.Project || !holdsTurnlithEntries(c, opts)) {
				continue
			}
			if !uninstall && !c.Detect() {
				continue
			}
			candidates = append(candidates, c)
		}
	}

	var sel selection
	for _, c := range candidates {
		if why := conflict(c, opts, uninstall); why != "" {
			sel.skipped = append(sel.skipped, skippedClient{c, why})
			continue
		}
		sel.run = append(sel.run, c)
	}
	return sel, explicit
}

// conflict says why a flag rules a client out ("" when it is fine). Writing
// somewhere the user did not ask for — a global config for --project, or the
// key in plaintext for --use-env — would be worse than skipping, so those
// skip the client instead of ignoring the flag.
func conflict(c harness.Client, opts options, uninstall bool) string {
	switch {
	case opts.project && !c.Caps.Project:
		return "it has no project-level config (drop --project)"
	case !uninstall && opts.useEnv && !c.Caps.UseEnv:
		return "its config cannot read the key from the environment (drop --use-env)"
	case !uninstall && opts.noMcp && !c.Caps.Provider:
		return "it only takes the MCP server, so --no-mcp leaves nothing to install"
	}
	return ""
}

func holdsTurnlithEntries(c harness.Client, opts options) bool {
	for _, f := range c.Inspect(opts.project && c.Caps.Project, providerIDOf(opts), harness.DefaultMCPServerID) {
		if f.Provider || f.MCP {
			return true
		}
	}
	return false
}

func providerIDOf(opts options) string {
	if opts.providerID != "" {
		return opts.providerID
	}
	return harness.DefaultProviderID
}

// reportSelection prints the skipped clients and decides whether to go on.
// It returns an exit code and whether the caller should stop. quiet turns
// "nothing to do" into a silent success (install --if-detected).
func reportSelection(con console, sel selection, explicit, quiet bool, verb string) (int, bool) {
	if len(sel.run) == 0 && explicit {
		for _, s := range sel.skipped {
			con.err.Errorf("cannot %s %s: %s", verb, s.client.Display, s.why)
		}
		return 2, true
	}
	for _, s := range sel.skipped {
		con.out.Infof("%s", con.theme.Yellow(fmt.Sprintf("skipping %s: %s", s.client.Display, s.why)))
	}
	switch {
	case len(sel.run) > 0:
		return 0, false
	case quiet:
		return 0, true
	case len(sel.skipped) > 0:
		con.err.Errorf("nothing to %s — every candidate client was skipped", verb)
		return 2, true
	case verb == "install":
		con.err.Errorf("no supported client detected — name one: tlc install <%s>", strings.Join(harness.Names(), "|"))
		return 2, true
	}
	con.out.Infof("No turnlith entries found in any supported client — nothing to remove.")
	return 0, true
}

func runInstall(opts options) int {
	con := newConsole()

	if opts.list {
		return runInstallList(con, opts)
	}

	sel, explicit := pickClients(opts, false)
	if code, stop := reportSelection(con, sel, explicit, opts.ifDetected && !explicit, "install"); stop {
		return code
	}

	conn, err := resolveConnection(opts)
	if err != nil {
		con.err.Errorf("%s", friendlyErr(err))
		return 1
	}

	// Always ask the server for the agents, even for clients that only take
	// the MCP server: it proves the URL and key work before they are written
	// into a config, and Cursor/Cline print the agent names for the model
	// settings they keep in the app.
	client := newClient(conn, con)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agents, err := client.ListAgents(ctx)
	if err != nil {
		con.err.Errorf("%s", friendlyErr(err))
		return 1
	}
	models := make([]harness.AgentModel, 0, len(agents))
	for _, ag := range agents {
		label := ""
		if ag.Label != nil {
			label = strings.TrimSpace(*ag.Label)
		}
		models = append(models, harness.AgentModel{Codename: ag.Codename, Label: label})
	}

	failed := false
	for i, c := range sel.run {
		if len(sel.run) > 1 {
			if i > 0 {
				con.out.Infof("")
			}
			con.out.Infof("%s", con.theme.Bold(c.Display))
		}
		if !installOne(con, c, opts, conn, models) {
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

// installOne installs into one client and prints what happened; it reports
// whether the install succeeded.
func installOne(con console, c harness.Client, opts options, conn config.Connection, models []harness.AgentModel) bool {
	providerID := providerIDOf(opts)
	req := harness.Request{
		Project:     opts.project,
		ProviderID:  providerID,
		MCPServerID: harness.DefaultMCPServerID,
		BaseURL:     conn.BaseURL,
		APIKey:      conn.APIKey,
		UseEnv:      opts.useEnv,
		Agents:      models,
		WithMCP:     c.Caps.MCP && !opts.noMcp,
	}
	result, err := c.Install(req)
	if err != nil {
		con.err.Errorf("could not write the %s config: %v", c.ID, err)
		return false
	}

	if opts.providerID != "" && !c.Caps.ProviderID {
		con.out.Infof("%s", con.theme.Dim("--provider-id ignored: "+c.Display+" has no provider entry to name"))
	}
	for _, f := range result.Files {
		state := "updated"
		if f.Created {
			state = "created"
		}
		con.out.Infof("%s %s", con.theme.Dim(c.ID+" config:"), f.Path+" ("+state+")")
	}
	if c.Caps.Provider {
		con.out.Infof("%s %s", con.theme.Dim("provider:"), providerID+" → "+conn.BaseURL+"/v1")
	}
	if req.WithMCP {
		con.out.Infof("%s %s", con.theme.Dim("mcp server:"),
			harness.DefaultMCPServerID+" → "+conn.BaseURL+"/v1/mcp (memory search tools)")
	}
	if c.Caps.Provider {
		con.out.Infof("%s %d", con.theme.Dim("agents:"), result.AgentCount)
		for _, m := range models {
			line := "  - " + ui.SanitizeInline(m.Codename)
			if m.Label != "" {
				line += " (" + ui.SanitizeInline(m.Label) + ")"
			}
			con.out.Infof("%s", con.theme.Dim(line))
		}
		if result.AgentCount == 0 {
			con.out.Infof("%s", con.theme.Yellow("no agents on the server yet — rerun `tlc install "+c.ID+"` after creating one"))
		}
	}
	if opts.useEnv {
		con.out.Infof("%s %s", con.theme.Dim("api key:"),
			c.EnvRef+" — export "+config.EnvAPIKey+"=<key> in your shell")
	}
	if c.ManualSteps != nil {
		for _, line := range c.ManualSteps(conn.BaseURL, models) {
			con.out.Infof("%s", con.theme.Yellow(ui.SanitizeBlock(line)))
		}
	}
	con.out.Infof("%s", con.theme.Dim(c.Hint))
	return true
}

// runInstallList is `tlc install --list`: offline, changes nothing.
func runInstallList(con console, opts options) int {
	providerID := providerIDOf(opts)
	for _, c := range harness.Registry() {
		detected := con.theme.Dim("not detected")
		if c.Detect() {
			detected = con.theme.Green("detected")
		}
		con.out.Infof("%s  %s  %s", con.theme.Bold(c.ID), detected, con.theme.Dim("writes "+capabilitySummary(c)))
		if opts.project && !c.Caps.Project {
			con.out.Infof("%s", con.theme.Dim("    no project-level config"))
			continue
		}
		states := c.Inspect(opts.project, providerID, harness.DefaultMCPServerID)
		if len(states) == 0 {
			con.out.Infof("%s", con.theme.Dim("    no "+c.Display+" install found"))
		}
		for _, f := range states {
			con.out.Infof("%s", con.theme.Dim("    "+f.Path+" — "+fileSummary(f)))
		}
	}
	return 0
}

func capabilitySummary(c harness.Client) string {
	switch {
	case c.Caps.Provider && c.Caps.MCP:
		return "the agents as models + the MCP server"
	case c.Caps.Provider:
		return "the agents as models"
	}
	return "the MCP server (model setup is manual)"
}

func fileSummary(f harness.FileState) string {
	if !f.Exists {
		return "missing"
	}
	var have []string
	if f.Provider {
		have = append(have, "agents")
	}
	if f.MCP {
		have = append(have, "mcp")
	}
	if len(have) == 0 {
		return "no turnlith entries"
	}
	return "turnlith installed: " + strings.Join(have, ", ")
}

func runUninstall(opts options) int {
	con := newConsole()

	sel, explicit := pickClients(opts, true)
	if code, stop := reportSelection(con, sel, explicit, false, "uninstall"); stop {
		return code
	}

	providerID := providerIDOf(opts)
	failed := false
	for _, c := range sel.run {
		outcome, err := c.Uninstall(harness.UninstallRequest{
			Project:     opts.project,
			ProviderID:  providerID,
			MCPServerID: harness.DefaultMCPServerID,
		})
		if err != nil {
			con.err.Errorf("could not update the %s config: %v", c.ID, err)
			failed = true
			continue
		}
		switch outcome {
		case harness.OutcomeRemoved:
			con.out.Infof("Removed %s from the %s config.", removedWhat(c, providerID), c.ID)
		case harness.OutcomeNotInstalled:
			con.out.Infof("%s is not in the %s config — nothing to remove.", subject(c, providerID), c.ID)
		default:
			con.out.Infof("No %s config found — nothing to remove.", c.ID)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// removedWhat names what an uninstall took out, matching the client's
// capabilities.
func removedWhat(c harness.Client, providerID string) string {
	switch {
	case c.Caps.Provider && c.Caps.MCP:
		return fmt.Sprintf("the %q provider and the MCP server", providerID)
	case c.Caps.Provider:
		return fmt.Sprintf("the %q provider", providerID)
	}
	return "the " + harness.DefaultMCPServerID + " MCP server"
}

// subject is the thing a "not installed" message talks about.
func subject(c harness.Client, providerID string) string {
	if c.Caps.Provider {
		return fmt.Sprintf("The %q provider", providerID)
	}
	return "The " + harness.DefaultMCPServerID + " MCP server"
}
