package harness

import (
	"os"
	"path/filepath"
	"runtime"
)

// Cline stores MCP servers in cline_mcp_settings.json inside the editor's
// global storage, so the path depends on the OS and on which VS Code
// flavour hosts the extension; tlc writes every install it finds. Cline has
// no environment-variable interpolation, so --use-env is unavailable. Its
// provider settings live in extension state, so model setup stays manual.
const clineExtensionDir = "saoudrizwan.claude-dev"

var clineDoc = jsoncDoc{label: "Cline"}

// ClineMcpBlock is a remote MCP server entry in cline_mcp_settings.json.
type ClineMcpBlock struct {
	Type     string            `json:"type"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"`
}

func buildClineMcpBlock(baseURL, apiKey string) ClineMcpBlock {
	return ClineMcpBlock{
		Type:    "streamableHttp",
		URL:     baseURL + "/v1/mcp",
		Headers: map[string]string{"Authorization": "Bearer " + apiKey},
	}
}

// clineEditors are the VS Code flavours (and forks) that can host Cline,
// named by their user-data directory.
var clineEditors = []string{"Code", "Code - Insiders", "VSCodium", "Cursor", "Windsurf"}

// userDataDir is the directory holding each editor's user-data folders.
func userDataDir() string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(homeDir(), "Library", "Application Support")
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return appData
		}
		return filepath.Join(homeDir(), "AppData", "Roaming")
	}
	return configHome()
}

// clineInstalls are the global-storage directories of every editor that has
// Cline installed.
func clineInstalls() []string {
	var dirs []string
	for _, editor := range clineEditors {
		dir := filepath.Join(userDataDir(), editor, "User", "globalStorage", clineExtensionDir)
		if dirExists(dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// ClineConfigPaths are the settings files of every detected Cline install.
func ClineConfigPaths() []string {
	installs := clineInstalls()
	paths := make([]string, len(installs))
	for i, dir := range installs {
		paths[i] = filepath.Join(dir, "settings", "cline_mcp_settings.json")
	}
	return paths
}

func clineClient() Client {
	return Client{
		ID:      "cline",
		Display: "Cline",
		Caps:    Capabilities{MCP: true},
		Hint:    `reload the editor window; "turnlith-controller" appears under Cline → MCP Servers`,
		Detect:  func() bool { return len(clineInstalls()) > 0 },
		Install: func(req Request) (Result, error) {
			if !req.WithMCP {
				return Result{}, errNothingToInstall("Cline")
			}
			paths := ClineConfigPaths()
			if len(paths) == 0 {
				return Result{}, errNotFound("Cline", "open the Cline extension once in VS Code, then rerun")
			}
			var res Result
			block := buildClineMcpBlock(req.BaseURL, req.APIKey)
			for _, path := range paths {
				file, err := writeJSONBlock(clineDoc, path, "mcpServers", req.MCPServerID, block)
				if err != nil {
					return Result{}, err
				}
				res.Files = append(res.Files, file)
			}
			return res, nil
		},
		Uninstall: func(req UninstallRequest) (UninstallOutcome, error) {
			var outcomes []UninstallOutcome
			for _, path := range ClineConfigPaths() {
				o, err := removeJSONBlock(clineDoc, path, "mcpServers", req.MCPServerID)
				if err != nil {
					return "", err
				}
				outcomes = append(outcomes, o)
			}
			return mergeOutcomes(outcomes), nil
		},
		Inspect: func(_ bool, _, mcpServerID string) []FileState {
			var states []FileState
			for _, path := range ClineConfigPaths() {
				states = append(states, inspectJSONFile(path, "", "", "mcpServers", mcpServerID))
			}
			return states
		},
		ManualSteps: func(baseURL string, agents []AgentModel) []string {
			return append([]string{
				`Cline keeps provider settings in the extension — choose the "OpenAI Compatible" provider:`,
				"  Base URL " + baseURL + "/v1, your api key, and one of these as the Model ID:",
			}, modelNameLines(agents, "    ")...)
		},
	}
}
