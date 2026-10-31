package harness

import (
	"os"
	"path/filepath"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

const (
	// DefaultProviderID is the provider key tlc writes into the client
	// config. It keeps the historical "tlith" spelling so configs written
	// by the retired setup CLI keep matching on reinstall and uninstall.
	DefaultProviderID = "tlith"
	// ProviderDisplayName is how the provider shows up in the client's UI.
	ProviderDisplayName = "turnlith"
	// DefaultMCPServerID is the MCP server id in the client's config —
	// opencode prefixes its tools with it, so they surface as
	// turnlith-controller_search_memory etc.
	DefaultMCPServerID = "turnlith-controller"

	opencodeSchema = "https://opencode.ai/config.json"
	// The server assembles a bounded ~12k-token context per call; these
	// limits keep the client's context-remaining UI honest rather than
	// describing real model limits.
	modelContextTokens = 12_000
	modelOutputTokens  = 8192

	envKeyRef = "{env:" + config.EnvAPIKey + "}"
)

var opencodeDoc = jsoncDoc{label: "opencode", schema: opencodeSchema}

// AgentModel is one agent as the client sees it: the codename clients call
// and the label to display for it.
type AgentModel struct {
	Codename string
	Label    string
}

// displayName is the label shown for the agent, falling back to its codename.
func (a AgentModel) displayName() string {
	if a.Label != "" {
		return a.Label
	}
	return a.Codename
}

// ProviderInput feeds BuildProviderBlock.
type ProviderInput struct {
	BaseURL string
	APIKey  string
	UseEnv  bool
	Agents  []AgentModel
}

// ProviderBlock is the opencode provider entry exposing the server's agents
// as models over its OpenAI-compatible endpoint.
type ProviderBlock struct {
	NPM     string               `json:"npm"`
	Name    string               `json:"name"`
	Options ProviderBlockOptions `json:"options"`
	Models  map[string]ModelRef  `json:"models"`
}

type ProviderBlockOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

// ModelRef names a model with the context/output limits the server enforces.
type ModelRef struct {
	Name  string     `json:"name"`
	Limit ModelLimit `json:"limit"`
}

type ModelLimit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

func BuildProviderBlock(in ProviderInput) ProviderBlock {
	models := map[string]ModelRef{}
	for _, a := range in.Agents {
		models[a.Codename] = ModelRef{
			Name:  a.displayName(),
			Limit: ModelLimit{Context: modelContextTokens, Output: modelOutputTokens},
		}
	}
	apiKey := in.APIKey
	if in.UseEnv {
		apiKey = envKeyRef
	}
	return ProviderBlock{
		NPM:     "@ai-sdk/openai-compatible",
		Name:    ProviderDisplayName,
		Options: ProviderBlockOptions{BaseURL: in.BaseURL + "/v1", APIKey: apiKey},
		Models:  models,
	}
}

// MCPInput feeds BuildMcpBlock.
type MCPInput struct {
	BaseURL string
	APIKey  string
	UseEnv  bool
}

// McpBlock is a remote (streamable-HTTP) MCP server entry; the bearer key
// rides the Authorization header.
type McpBlock struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Enabled bool              `json:"enabled"`
	Headers map[string]string `json:"headers"`
	// Key auth only — never let opencode fall into an OAuth dance on a 401.
	OAuth bool `json:"oauth"`
}

func BuildMcpBlock(in MCPInput) McpBlock {
	apiKey := in.APIKey
	if in.UseEnv {
		apiKey = envKeyRef
	}
	return McpBlock{
		Type:    "remote",
		URL:     in.BaseURL + "/v1/mcp",
		Enabled: true,
		Headers: map[string]string{"Authorization": "Bearer " + apiKey},
		OAuth:   false,
	}
}

// ------------------------------------------------------------- JSONC editing

// UpsertProviderText merges the provider block into JSONC text, preserving
// everything else (other providers, comments, formatting). A nil existing
// document means a fresh config file.
func UpsertProviderText(existing []byte, providerID string, block ProviderBlock) ([]byte, error) {
	return upsertBlockText(opencodeDoc, existing, "provider", providerID, block)
}

// RemoveProviderText removes the provider block, plus the provider object
// when it empties. A missing block leaves the text unchanged.
func RemoveProviderText(existing []byte, providerID string) ([]byte, error) {
	return removeBlockText(opencodeDoc, existing, "provider", providerID)
}

// HasProviderBlock reports whether the provider block is present.
func HasProviderBlock(existing []byte, providerID string) bool {
	return hasBlock(existing, "provider", providerID)
}

// UpsertMcpText merges the MCP server block into JSONC text.
func UpsertMcpText(existing []byte, serverID string, block McpBlock) ([]byte, error) {
	return upsertBlockText(opencodeDoc, existing, "mcp", serverID, block)
}

// RemoveMcpText removes the MCP server block, plus the mcp object when it
// empties.
func RemoveMcpText(existing []byte, serverID string) ([]byte, error) {
	return removeBlockText(opencodeDoc, existing, "mcp", serverID)
}

// HasMcpBlock reports whether the MCP server block is present.
func HasMcpBlock(existing []byte, serverID string) bool {
	return hasBlock(existing, "mcp", serverID)
}

// ------------------------------------------------------------ file operations

// OpencodeConfigPath is the config file tlc edits: ./opencode.json in
// project mode, otherwise the opencode directory under $XDG_CONFIG_HOME
// (when absolute) or ~/.config — on every platform, macOS included.
func OpencodeConfigPath(project bool) string {
	if project {
		return "opencode.json"
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "opencode", "opencode.json")
	}
	return filepath.Join(homeDir(), ".config", "opencode", "opencode.json")
}

// resolveTarget prefers an existing opencode.jsonc sibling when the .json
// file is absent — opencode reads both, so tlc must not fork the user's
// config into two files.
func resolveTarget(path string) string { return preferJSONC(path) }

// InstallOptions carries what InstallOpencode writes.
type InstallOptions struct {
	Project    bool
	ProviderID string
	Block      ProviderBlock
	// Optional remote MCP server block written beside the provider.
	MCPServerID string
	MCPBlock    *McpBlock
}

// InstallResult reports what InstallOpencode did.
type InstallResult struct {
	Path         string
	ProviderID   string
	AgentCount   int
	MCPInstalled bool
	Created      bool
}

func InstallOpencode(opts InstallOptions) (InstallResult, error) {
	target := resolveTarget(OpencodeConfigPath(opts.Project))
	existing, err := readOrNull(target)
	if err != nil {
		return InstallResult{}, err
	}
	next, err := UpsertProviderText(existing, opts.ProviderID, opts.Block)
	if err != nil {
		return InstallResult{}, err
	}
	mcpInstalled := false
	if opts.MCPBlock != nil && opts.MCPServerID != "" {
		mcpInstalled = existing == nil || !HasMcpBlock(existing, opts.MCPServerID)
		if next, err = UpsertMcpText(next, opts.MCPServerID, *opts.MCPBlock); err != nil {
			return InstallResult{}, err
		}
	}
	if err := writeConfig(target, next); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{
		Path:         target,
		ProviderID:   opts.ProviderID,
		AgentCount:   len(opts.Block.Models),
		MCPInstalled: mcpInstalled,
		Created:      existing == nil,
	}, nil
}

// UninstallOutcome says what an uninstall found.
type UninstallOutcome string

const (
	OutcomeRemoved      UninstallOutcome = "removed"
	OutcomeNotInstalled UninstallOutcome = "not-installed"
	OutcomeNoConfig     UninstallOutcome = "no-config"
)

// UninstallOptions carries what UninstallOpencode removes.
type UninstallOptions struct {
	Project     bool
	ProviderID  string
	MCPServerID string
}

func UninstallOpencode(opts UninstallOptions) (UninstallOutcome, error) {
	target := resolveTarget(OpencodeConfigPath(opts.Project))
	existing, err := readOrNull(target)
	if err != nil {
		return "", err
	}
	if existing == nil {
		return OutcomeNoConfig, nil
	}
	hadProvider := HasProviderBlock(existing, opts.ProviderID)
	hadMcp := opts.MCPServerID != "" && HasMcpBlock(existing, opts.MCPServerID)
	if !hadProvider && !hadMcp {
		return OutcomeNotInstalled, nil
	}
	next := existing
	var err2 error
	if hadProvider {
		if next, err2 = RemoveProviderText(next, opts.ProviderID); err2 != nil {
			return "", err2
		}
	}
	if hadMcp {
		if next, err2 = RemoveMcpText(next, opts.MCPServerID); err2 != nil {
			return "", err2
		}
	}
	if err := writeConfig(target, next); err != nil {
		return "", err
	}
	return OutcomeRemoved, nil
}

// opencodeClient adapts the opencode functions above to the registry.
func opencodeClient() Client {
	return Client{
		ID:      "opencode",
		Display: "opencode",
		Caps:    Capabilities{Provider: true, MCP: true, Project: true, ProviderID: true, UseEnv: true},
		EnvRef:  envKeyRef,
		Hint:    `run "opencode" and pick an agent under "turnlith" in /models`,
		Detect:  func() bool { return onPath("opencode") },
		Install: func(req Request) (Result, error) {
			opts := InstallOptions{
				Project:    req.Project,
				ProviderID: req.ProviderID,
				Block: BuildProviderBlock(ProviderInput{
					BaseURL: req.BaseURL, APIKey: req.APIKey, UseEnv: req.UseEnv, Agents: req.Agents,
				}),
			}
			if req.WithMCP {
				mcp := BuildMcpBlock(MCPInput{BaseURL: req.BaseURL, APIKey: req.APIKey, UseEnv: req.UseEnv})
				opts.MCPServerID, opts.MCPBlock = req.MCPServerID, &mcp
			}
			res, err := InstallOpencode(opts)
			if err != nil {
				return Result{}, err
			}
			return Result{
				Files:      []FileResult{{Path: res.Path, Created: res.Created}},
				AgentCount: res.AgentCount,
			}, nil
		},
		Uninstall: func(req UninstallRequest) (UninstallOutcome, error) {
			return UninstallOpencode(UninstallOptions{
				Project: req.Project, ProviderID: req.ProviderID, MCPServerID: req.MCPServerID,
			})
		},
		Inspect: func(project bool, providerID, mcpServerID string) []FileState {
			path := resolveTarget(OpencodeConfigPath(project))
			return []FileState{inspectJSONFile(path, "provider", providerID, "mcp", mcpServerID)}
		},
	}
}
