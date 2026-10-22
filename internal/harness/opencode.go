// Package harness wires a turnlith server into local AI harnesses.
// First integration: opencode (https://opencode.ai), whose config is a
// JSONC file — edits preserve the user's comments, formatting and any
// blocks other tools wrote.
package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

const (
	// DefaultProviderID is the provider key tlc writes into the harness
	// config. It keeps the historical "tlith" spelling so configs written
	// by the retired setup CLI keep matching on reinstall and uninstall.
	DefaultProviderID = "tlith"
	// ProviderDisplayName is how the provider shows up in opencode's UI.
	ProviderDisplayName = "turnlith"
	// DefaultMCPServerID is the MCP server id in opencode's config —
	// opencode prefixes its tools with it, so they surface as
	// turnlith-controller_search_memory etc.
	DefaultMCPServerID = "turnlith-controller"

	opencodeSchema = "https://opencode.ai/config.json"
	// The server assembles a bounded ~12k-token context per call; these
	// limits keep the harness's context-remaining UI honest rather than
	// describing real model limits.
	modelContextTokens = 12_000
	modelOutputTokens  = 8192

	envKeyRef = "{env:" + config.EnvAPIKey + "}"
)

// AgentModel is one agent as the harness sees it: the codename clients call
// and the label to display for it.
type AgentModel struct {
	Codename string
	Label    string
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
		name := a.Label
		if name == "" {
			name = a.Codename
		}
		models[a.Codename] = ModelRef{
			Name:  name,
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
	return upsertBlockText(existing, "provider", providerID, block)
}

// RemoveProviderText removes the provider block, plus the provider object
// when it empties. A missing block leaves the text unchanged.
func RemoveProviderText(existing []byte, providerID string) ([]byte, error) {
	return removeBlockText(existing, "provider", providerID)
}

// HasProviderBlock reports whether the provider block is present.
func HasProviderBlock(existing []byte, providerID string) bool {
	return hasBlock(existing, "provider", providerID)
}

// UpsertMcpText merges the MCP server block into JSONC text.
func UpsertMcpText(existing []byte, serverID string, block McpBlock) ([]byte, error) {
	return upsertBlockText(existing, "mcp", serverID, block)
}

// RemoveMcpText removes the MCP server block, plus the mcp object when it
// empties.
func RemoveMcpText(existing []byte, serverID string) ([]byte, error) {
	return removeBlockText(existing, "mcp", serverID)
}

// HasMcpBlock reports whether the MCP server block is present.
func HasMcpBlock(existing []byte, serverID string) bool {
	return hasBlock(existing, "mcp", serverID)
}

var errBrokenConfig = errors.New("existing opencode config has JSON syntax errors — fix it first")

// upsertBlockText merges block under [section, key], generating fresh
// documents ($schema + the section) when existing is nil.
func upsertBlockText(existing []byte, section, key string, block any) ([]byte, error) {
	if existing == nil {
		doc := map[string]any{
			"$schema": opencodeSchema,
			section:   map[string]any{key: block},
		}
		fresh, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(fresh, '\n'), nil
	}

	root, obj, err := parseRootObject(existing)
	if err != nil {
		return nil, err
	}
	unit := indentUnit(obj)

	// Resolve (or create) the section object, e.g. "provider" / "mcp".
	sectionIdx := findMember(obj, section)
	var sectionObj *hujson.Object
	// sectionUnit is the indentation prefix of the section's members (one
	// level below the section itself).
	sectionUnit := unit + unit
	if sectionIdx < 0 {
		sectionObj = &hujson.Object{}
		appendMember(obj, section, hujson.Value{Value: sectionObj}, "\n"+unit)
		if len(obj.Members) == 1 && len(obj.AfterExtra) == 0 {
			obj.AfterExtra = []byte("\n")
		}
	} else {
		so, ok := obj.Members[sectionIdx].Value.Value.(*hujson.Object)
		if !ok {
			return nil, fmt.Errorf("existing opencode config: %q is not an object", section)
		}
		sectionObj = so
		if len(sectionObj.Members) > 0 {
			sectionUnit = indentUnit(sectionObj)
		}
	}

	// Serialize the block with absolute indentation baked in: every newline
	// inside it carries the section members' prefix, so the parsed tree
	// lands with matching whitespace.
	blockJSON, err := json.MarshalIndent(block, sectionUnit, unit)
	if err != nil {
		return nil, err
	}
	blockValue, err := hujson.Parse(blockJSON)
	if err != nil {
		return nil, err
	}
	if idx := findMember(sectionObj, key); idx >= 0 {
		// Replace only the value: the member's own surrounding whitespace
		// and comments stay exactly as the user had them.
		sectionObj.Members[idx].Value.Value = blockValue.Value
	} else {
		appendMember(sectionObj, key, blockValue, "\n"+sectionUnit)
		if len(sectionObj.Members) == 1 && len(sectionObj.AfterExtra) == 0 {
			sectionObj.AfterExtra = []byte("\n" + unit)
		}
	}
	return root.Pack(), nil
}

// removeBlockText removes [section, key] and drops the emptied section.
func removeBlockText(existing []byte, section, key string) ([]byte, error) {
	root, obj, err := parseRootObject(existing)
	if err != nil {
		return nil, err
	}
	sectionIdx := findMember(obj, section)
	if sectionIdx < 0 {
		return existing, nil
	}
	sectionObj, ok := obj.Members[sectionIdx].Value.Value.(*hujson.Object)
	if !ok || findMember(sectionObj, key) < 0 {
		return existing, nil
	}
	// RFC 6902 remove keeps hujson's comment-relocation behavior for the
	// bytes around the removed member.
	if err := root.Patch([]byte(fmt.Sprintf(
		`[{"op": "remove", "path": "/%s/%s"}]`, pointerEscape(section), pointerEscape(key)))); err != nil {
		return nil, err
	}
	if sectionIdx = findMember(obj, section); sectionIdx >= 0 {
		if so, ok := obj.Members[sectionIdx].Value.Value.(*hujson.Object); ok && len(so.Members) == 0 {
			obj.Members = append(obj.Members[:sectionIdx], obj.Members[sectionIdx+1:]...)
		}
	}
	return root.Pack(), nil
}

func hasBlock(existing []byte, section, key string) bool {
	root, err := hujson.Parse(existing)
	if err != nil {
		return false
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		return false
	}
	i := findMember(obj, section)
	if i < 0 {
		return false
	}
	so, ok := obj.Members[i].Value.Value.(*hujson.Object)
	return ok && findMember(so, key) >= 0
}

func parseRootObject(existing []byte) (hujson.Value, *hujson.Object, error) {
	root, err := hujson.Parse(existing)
	if err != nil {
		return root, nil, errBrokenConfig
	}
	obj, ok := root.Value.(*hujson.Object)
	if !ok {
		return root, nil, errors.New("existing opencode config is not a JSON object")
	}
	return root, obj, nil
}

func findMember(obj *hujson.Object, name string) int {
	want := []byte(hujson.String(name))
	for i := range obj.Members {
		if lit, ok := obj.Members[i].Name.Value.(hujson.Literal); ok && bytes.Equal(lit, want) {
			return i
		}
	}
	return -1
}

// appendMember adds "name": value to obj with the given BeforeExtra (a
// newline plus indentation) and single spaces around the colon, matching
// conventional JSON style; it returns the member index.
func appendMember(obj *hujson.Object, name string, value hujson.Value, before string) int {
	nameValue := hujson.Value{Value: hujson.String(name)}
	nameValue.BeforeExtra = []byte(before)
	if len(value.BeforeExtra) == 0 {
		value.BeforeExtra = []byte(" ")
	}
	obj.Members = append(obj.Members, hujson.ObjectMember{Name: nameValue, Value: value})
	return len(obj.Members) - 1
}

// indentUnit infers one nesting level of indentation from the object's
// existing members (2 spaces by default).
func indentUnit(obj *hujson.Object) string {
	for i := range obj.Members {
		w := string(obj.Members[i].Name.BeforeExtra)
		if j := strings.LastIndexByte(w, '\n'); j >= 0 {
			return w[j+1:]
		}
	}
	return "  "
}

// pointerEscape encodes a path segment per RFC 6902.
func pointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
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
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "opencode", "opencode.json")
}

// resolveTarget prefers an existing opencode.jsonc sibling when the .json
// file is absent — opencode reads both, so tlc must not fork the user's
// config into two files.
func resolveTarget(path string) string {
	if strings.HasSuffix(path, ".json") {
		jsonc := path[:len(path)-len(".json")] + ".jsonc"
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(jsonc); err == nil {
				return jsonc
			}
		}
	}
	return path
}

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
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return InstallResult{}, err
	}
	// The mode applies only on create; an existing file keeps the user's
	// permissions.
	if err := os.WriteFile(target, next, 0o600); err != nil {
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

// UninstallOutcome says what UninstallOpencode found.
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
	if err := os.WriteFile(target, next, 0o600); err != nil {
		return "", err
	}
	return OutcomeRemoved, nil
}

func readOrNull(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}
