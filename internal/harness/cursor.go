package harness

import (
	"path/filepath"
)

// Cursor reads MCP servers from mcp.json. Its model settings (a custom
// OpenAI base URL and model names) live in the app's own state, not in a
// file tlc can edit, so only the turnlith-controller server is written and
// the model setup is printed as manual steps.
const cursorEnvRef = "${env:" + envAPIKeyName + "}"

var cursorDoc = jsoncDoc{label: "Cursor"}

// CursorMcpBlock is a remote MCP server entry in Cursor's mcp.json.
type CursorMcpBlock struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

func buildCursorMcpBlock(baseURL, apiKey string, useEnv bool) CursorMcpBlock {
	if useEnv {
		apiKey = cursorEnvRef
	}
	return CursorMcpBlock{
		URL:     baseURL + "/v1/mcp",
		Headers: map[string]string{"Authorization": "Bearer " + apiKey},
	}
}

// CursorConfigPath is ~/.cursor/mcp.json, or ./.cursor/mcp.json in project
// mode.
func CursorConfigPath(project bool) string {
	if project {
		return filepath.Join(".cursor", "mcp.json")
	}
	return filepath.Join(homeDir(), ".cursor", "mcp.json")
}

func cursorClient() Client {
	return Client{
		ID:      "cursor",
		Display: "Cursor",
		Caps:    Capabilities{MCP: true, Project: true, UseEnv: true},
		EnvRef:  cursorEnvRef,
		Hint:    `restart Cursor, then check that "turnlith-controller" is enabled under Settings → MCP`,
		Detect:  func() bool { return onPath("cursor") || dirExists(filepath.Join(homeDir(), ".cursor")) },
		Install: func(req Request) (Result, error) {
			if !req.WithMCP {
				return Result{}, errNothingToInstall("Cursor")
			}
			file, err := writeJSONBlock(cursorDoc, CursorConfigPath(req.Project), "mcpServers", req.MCPServerID,
				buildCursorMcpBlock(req.BaseURL, req.APIKey, req.UseEnv))
			if err != nil {
				return Result{}, err
			}
			return Result{Files: []FileResult{file}}, nil
		},
		Uninstall: func(req UninstallRequest) (UninstallOutcome, error) {
			return removeJSONBlock(cursorDoc, CursorConfigPath(req.Project), "mcpServers", req.MCPServerID)
		},
		Inspect: func(project bool, _, mcpServerID string) []FileState {
			return []FileState{inspectJSONFile(CursorConfigPath(project), "", "", "mcpServers", mcpServerID)}
		},
		ManualSteps: func(baseURL string, agents []AgentModel) []string {
			return append([]string{
				"Cursor keeps model settings in the app — set them under Settings → Models:",
				"  override the OpenAI Base URL with " + baseURL + "/v1 and paste your api key",
			}, modelNameLines(agents, "  add a custom model named ")...)
		},
	}
}
