package harness

import (
	"os"
	"path/filepath"
)

// Pi (https://pi.dev) reads custom providers from models.json in its agent
// directory. It has no MCP support, so only the provider is written.
// The directory is $PI_CODING_AGENT_DIR when set, else ~/.pi/agent.
const piEnvRef = "$" + envAPIKeyName

var piDoc = jsoncDoc{label: "Pi"}

// PiProviderBlock is a custom provider in Pi's models.json: the server's
// OpenAI-compatible endpoint with the agents as models.
type PiProviderBlock struct {
	BaseURL string    `json:"baseUrl"`
	API     string    `json:"api"`
	APIKey  string    `json:"apiKey"`
	Models  []PiModel `json:"models"`
}

// PiModel is one agent as a Pi model.
type PiModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
	MaxTokens     int    `json:"maxTokens"`
}

func buildPiProviderBlock(in ProviderInput) PiProviderBlock {
	models := make([]PiModel, 0, len(in.Agents))
	for _, a := range in.Agents {
		models = append(models, PiModel{
			ID:            a.Codename,
			Name:          a.displayName(),
			ContextWindow: modelContextTokens,
			MaxTokens:     modelOutputTokens,
		})
	}
	apiKey := in.APIKey
	if in.UseEnv {
		apiKey = piEnvRef
	}
	return PiProviderBlock{
		BaseURL: in.BaseURL + "/v1",
		API:     "openai-completions",
		APIKey:  apiKey,
		Models:  models,
	}
}

// PiConfigPath is Pi's models.json. Pi has a single user-level config, so
// there is no project variant.
func PiConfigPath() string {
	dir := os.Getenv("PI_CODING_AGENT_DIR")
	if dir == "" {
		dir = filepath.Join(homeDir(), ".pi", "agent")
	}
	return filepath.Join(dir, "models.json")
}

func piClient() Client {
	return Client{
		ID:      "pi",
		Display: "Pi",
		Aliases: []string{"pi-agent", "pi-coding-agent"},
		Caps:    Capabilities{Provider: true, ProviderID: true, UseEnv: true},
		EnvRef:  piEnvRef,
		Hint:    `run "pi" and pick a turnlith agent with /model (the model list reloads on open)`,
		Detect:  func() bool { return dirExists(filepath.Dir(PiConfigPath())) },
		Install: func(req Request) (Result, error) {
			block := buildPiProviderBlock(ProviderInput{
				BaseURL: req.BaseURL, APIKey: req.APIKey, UseEnv: req.UseEnv, Agents: req.Agents,
			})
			file, err := writeJSONBlock(piDoc, PiConfigPath(), "providers", req.ProviderID, block)
			if err != nil {
				return Result{}, err
			}
			return Result{Files: []FileResult{file}, AgentCount: len(block.Models)}, nil
		},
		Uninstall: func(req UninstallRequest) (UninstallOutcome, error) {
			return removeJSONBlock(piDoc, PiConfigPath(), "providers", req.ProviderID)
		},
		Inspect: func(_ bool, providerID, _ string) []FileState {
			return []FileState{inspectJSONFile(PiConfigPath(), "providers", providerID, "", "")}
		},
		ManualSteps: func(string, []AgentModel) []string {
			return []string{"Pi has no MCP support, so the memory-search tools are not available there."}
		},
	}
}
