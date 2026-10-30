// Package harness wires a turnlith server into local AI coding clients
// (opencode, Pi, Continue, Cursor, Cline). Every client is one entry in the
// registry: where its config lives, what tlc can write there (the agents as
// models, the turnlith-controller MCP server, or both) and how to take it out
// again. JSON configs are edited in place so the user's comments, formatting
// and other entries survive.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

// Capabilities says which parts of an install a client honours, so the CLI
// can reject flags that would silently do nothing.
type Capabilities struct {
	Provider   bool // tlc can add the agents as models
	MCP        bool // tlc can add the turnlith-controller MCP server
	Project    bool // --project: a per-directory config exists
	ProviderID bool // --provider-id names something in the config
	UseEnv     bool // the config can reference $TLITH_API_KEY instead of the key
}

// Request is everything an install needs, independent of the client.
type Request struct {
	Project     bool
	ProviderID  string
	MCPServerID string
	BaseURL     string
	APIKey      string
	UseEnv      bool
	Agents      []AgentModel
	WithMCP     bool
}

// FileResult is one config file an install touched.
type FileResult struct {
	Path    string
	Created bool
}

// Result reports what an install did.
type Result struct {
	Files      []FileResult
	AgentCount int
}

// UninstallRequest says what to remove.
type UninstallRequest struct {
	Project     bool
	ProviderID  string
	MCPServerID string
}

// FileState is what Inspect found in one config file.
type FileState struct {
	Path     string
	Exists   bool
	Provider bool // the turnlith provider/models entry is present
	MCP      bool // the turnlith-controller MCP entry is present
}

// Client is one supported coding client.
type Client struct {
	ID      string
	Display string
	Aliases []string
	Caps    Capabilities
	// EnvRef is how the client's config refers to the TLITH_API_KEY
	// variable (--use-env); empty when it cannot.
	EnvRef string
	// Hint is the last line printed after an install.
	Hint string

	// Detect reports whether the client looks installed. It only drives
	// auto-detection and `tlc install --list`; naming a client always works.
	Detect    func() bool
	Install   func(Request) (Result, error)
	Uninstall func(UninstallRequest) (UninstallOutcome, error)
	// Inspect reports the state of every config file the client uses.
	Inspect func(project bool, providerID, mcpServerID string) []FileState
	// ManualSteps are the lines to print for what tlc cannot write into a
	// file (e.g. Cursor and Cline keep their model settings in app state).
	ManualSteps func(baseURL string, agents []AgentModel) []string
}

// Registry lists every supported client, in the order shown to users.
func Registry() []Client {
	return []Client{opencodeClient(), piClient(), continueClient(), cursorClient(), clineClient()}
}

// Names are the canonical client ids, in registry order.
func Names() []string {
	clients := Registry()
	names := make([]string, len(clients))
	for i, c := range clients {
		names[i] = c.ID
	}
	return names
}

// Lookup finds a client by id or alias, ignoring case.
func Lookup(name string) (Client, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range Registry() {
		if c.ID == name {
			return c, true
		}
		for _, alias := range c.Aliases {
			if alias == name {
				return c, true
			}
		}
	}
	return Client{}, false
}

// ------------------------------------------------------------------ helpers

func onPath(binary string) bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// configHome is $XDG_CONFIG_HOME when absolute, else ~/.config.
func configHome() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return xdg
	}
	return filepath.Join(homeDir(), ".config")
}

// writeJSONBlock upserts doc[section][key] = block in the JSON(C) file at
// path, creating the file (and its directory) when it does not exist.
func writeJSONBlock(doc jsoncDoc, path, section, key string, block any) (FileResult, error) {
	existing, err := readOrNull(path)
	if err != nil {
		return FileResult{}, err
	}
	next, err := upsertBlockText(doc, existing, section, key, block)
	if err != nil {
		return FileResult{}, err
	}
	if err := writeConfig(path, next); err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: path, Created: existing == nil}, nil
}

// removeJSONBlock removes doc[section][key] from the file at path.
func removeJSONBlock(doc jsoncDoc, path, section, key string) (UninstallOutcome, error) {
	existing, err := readOrNull(path)
	if err != nil {
		return "", err
	}
	if existing == nil {
		return OutcomeNoConfig, nil
	}
	if !hasBlock(existing, section, key) {
		return OutcomeNotInstalled, nil
	}
	next, err := removeBlockText(doc, existing, section, key)
	if err != nil {
		return "", err
	}
	if err := writeConfig(path, next); err != nil {
		return "", err
	}
	return OutcomeRemoved, nil
}

// mergeOutcomes folds per-file outcomes into one: any removal wins, then
// "not installed" (a config exists but holds nothing of ours), then "no
// config".
func mergeOutcomes(outcomes []UninstallOutcome) UninstallOutcome {
	merged := OutcomeNoConfig
	for _, o := range outcomes {
		switch {
		case o == OutcomeRemoved:
			return OutcomeRemoved
		case o == OutcomeNotInstalled:
			merged = OutcomeNotInstalled
		}
	}
	return merged
}

// inspectJSONFile reports which turnlith entries a JSON config file holds; an
// empty section name skips that check.
func inspectJSONFile(path, providerSection, providerID, mcpSection, mcpServerID string) FileState {
	data, _ := os.ReadFile(path)
	st := FileState{Path: path, Exists: data != nil}
	if data == nil {
		return st
	}
	if providerSection != "" {
		st.Provider = hasBlock(data, providerSection, providerID)
	}
	if mcpSection != "" {
		st.MCP = hasBlock(data, mcpSection, mcpServerID)
	}
	return st
}

// envAPIKeyName is the variable --use-env points configs at.
const envAPIKeyName = config.EnvAPIKey

func errNothingToInstall(display string) error {
	return fmt.Errorf("%s only takes the MCP server from tlc — nothing to install with --no-mcp", display)
}

func errNotFound(display, hint string) error {
	return fmt.Errorf("no %s config found — %s", display, hint)
}

// modelNameLines renders one line per agent for the manual-setup hints.
func modelNameLines(agents []AgentModel, prefix string) []string {
	if len(agents) == 0 {
		return []string{prefix + "(no agents on the server yet)"}
	}
	lines := make([]string, len(agents))
	for i, a := range agents {
		lines[i] = prefix + a.Codename
		if a.Label != "" {
			lines[i] += " (" + a.Label + ")"
		}
	}
	return lines
}
