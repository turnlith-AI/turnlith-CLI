package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Continue loads "blocks" from YAML files in <dir>/models and
// <dir>/mcpServers, where <dir> is the user's ~/.continue (or
// $CONTINUE_GLOBAL_DIR) or the workspace's .continue. tlc writes whole files
// of its own instead of editing config.yaml, which keeps the user's YAML —
// comments, anchors, ordering — out of reach and needs no YAML library. A
// marker on the first line identifies the files tlc owns: it refuses to
// overwrite a same-named file without it and only ever deletes marked ones.
const (
	continueMarker = "# Managed by tlc — `tlc install continue` rewrites this file, `tlc uninstall continue` deletes it."
	continueEnvRef = "${{ secrets." + envAPIKeyName + " }}"
)

func continueDir(project bool) string {
	if project {
		return ".continue"
	}
	if dir := os.Getenv("CONTINUE_GLOBAL_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(homeDir(), ".continue")
}

// ContinueModelsPath is the models block file tlc owns.
func ContinueModelsPath(project bool, providerID string) string {
	return filepath.Join(continueDir(project), "models", providerID+".yaml")
}

// ContinueMCPPath is the MCP server block file tlc owns.
func ContinueMCPPath(project bool, serverID string) string {
	return filepath.Join(continueDir(project), "mcpServers", serverID+".yaml")
}

// yamlString quotes s as a YAML scalar. JSON strings are valid YAML double-
// quoted scalars, so no escaping rules of our own are needed.
func yamlString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}

func continueModelsYAML(req Request) string {
	apiKey := req.APIKey
	if req.UseEnv {
		apiKey = continueEnvRef
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nname: %s\nversion: 0.0.1\nschema: v1\nmodels:\n", continueMarker, yamlString(ProviderDisplayName))
	for _, a := range req.Agents {
		fmt.Fprintf(&b, "  - name: %s\n", yamlString(a.displayName()+" (turnlith)"))
		b.WriteString("    provider: openai\n")
		fmt.Fprintf(&b, "    model: %s\n", yamlString(a.Codename))
		fmt.Fprintf(&b, "    apiBase: %s\n", yamlString(req.BaseURL+"/v1"))
		fmt.Fprintf(&b, "    apiKey: %s\n", yamlString(apiKey))
		b.WriteString("    roles:\n      - chat\n      - edit\n      - apply\n")
		b.WriteString("    capabilities:\n      - tool_use\n")
		fmt.Fprintf(&b, "    defaultCompletionOptions:\n      contextLength: %d\n      maxTokens: %d\n",
			modelContextTokens, modelOutputTokens)
	}
	return b.String()
}

func continueMCPYAML(req Request) string {
	apiKey := req.APIKey
	if req.UseEnv {
		apiKey = continueEnvRef
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nname: %s\nversion: 0.0.1\nschema: v1\nmcpServers:\n", continueMarker, yamlString(req.MCPServerID))
	fmt.Fprintf(&b, "  - name: %s\n", yamlString(req.MCPServerID))
	b.WriteString("    type: streamable-http\n")
	fmt.Fprintf(&b, "    url: %s\n", yamlString(req.BaseURL+"/v1/mcp"))
	fmt.Fprintf(&b, "    requestOptions:\n      headers:\n        Authorization: %s\n", yamlString("Bearer "+apiKey))
	return b.String()
}

// writeOwned writes a tlc-owned file, refusing to clobber one it did not
// write.
func writeOwned(path, content string) (FileResult, error) {
	existing, err := readOrNull(path)
	if err != nil {
		return FileResult{}, err
	}
	if existing != nil && !bytes.HasPrefix(existing, []byte(continueMarker)) {
		return FileResult{}, fmt.Errorf("%s exists and was not written by tlc — move it aside or pick another name with --provider-id", path)
	}
	if err := writeConfig(path, []byte(content)); err != nil {
		return FileResult{}, err
	}
	return FileResult{Path: path, Created: existing == nil}, nil
}

// removeOwned deletes a tlc-owned file; files without the marker are left
// alone and count as not installed.
func removeOwned(path string) (UninstallOutcome, error) {
	existing, err := readOrNull(path)
	if err != nil {
		return "", err
	}
	if existing == nil {
		return OutcomeNoConfig, nil
	}
	if !bytes.HasPrefix(existing, []byte(continueMarker)) {
		return OutcomeNotInstalled, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return OutcomeRemoved, nil
}

func continueClient() Client {
	return Client{
		ID:      "continue",
		Display: "Continue",
		Aliases: []string{"continue.dev"},
		Caps:    Capabilities{Provider: true, MCP: true, Project: true, ProviderID: true, UseEnv: true},
		EnvRef:  continueEnvRef,
		Hint:    `reload Continue and pick a turnlith agent in the model dropdown`,
		Detect:  func() bool { return dirExists(continueDir(false)) },
		Install: func(req Request) (Result, error) {
			var res Result
			if len(req.Agents) > 0 {
				file, err := writeOwned(ContinueModelsPath(req.Project, req.ProviderID), continueModelsYAML(req))
				if err != nil {
					return Result{}, err
				}
				res.Files = append(res.Files, file)
				res.AgentCount = len(req.Agents)
			}
			if req.WithMCP {
				file, err := writeOwned(ContinueMCPPath(req.Project, req.MCPServerID), continueMCPYAML(req))
				if err != nil {
					return Result{}, err
				}
				res.Files = append(res.Files, file)
			}
			return res, nil
		},
		Uninstall: func(req UninstallRequest) (UninstallOutcome, error) {
			paths := []string{ContinueModelsPath(req.Project, req.ProviderID)}
			if req.MCPServerID != "" {
				paths = append(paths, ContinueMCPPath(req.Project, req.MCPServerID))
			}
			var outcomes []UninstallOutcome
			for _, path := range paths {
				o, err := removeOwned(path)
				if err != nil {
					return "", err
				}
				outcomes = append(outcomes, o)
			}
			return mergeOutcomes(outcomes), nil
		},
		Inspect: func(project bool, providerID, mcpServerID string) []FileState {
			owned := func(path string) bool {
				data, _ := os.ReadFile(path)
				return bytes.HasPrefix(data, []byte(continueMarker))
			}
			models, mcp := ContinueModelsPath(project, providerID), ContinueMCPPath(project, mcpServerID)
			exists := func(path string) bool { _, err := os.Stat(path); return err == nil }
			return []FileState{
				{Path: models, Exists: exists(models), Provider: owned(models)},
				{Path: mcp, Exists: exists(mcp), MCP: owned(mcp)},
			}
		},
	}
}
