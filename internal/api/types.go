// Package api is a minimal client for the turnlith HTTP API: health, the
// workspace's agents and model catalog (/v1/models), and agent creation
// (/v1/agents).
package api

// Agent is one turnlith agent (its codename is the model id clients call).
type Agent struct {
	ID       string  `json:"id"`
	Codename string  `json:"codename"`
	Label    *string `json:"label"`
	Model    string  `json:"model"`
	// ReasoningEffort is the level applied to every turn of the agent's
	// session; nil = the provider's default.
	ReasoningEffort *string `json:"reasoningEffort"`
}

// ProviderStatus is one chat provider as the workspace sees it.
type ProviderStatus struct {
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
	Source    string `json:"source"`
	// ReasoningEfforts lists the effort levels this provider's models
	// accept; empty = the endpoint has no reasoning control.
	ReasoningEfforts []string `json:"reasoningEfforts"`
}

// CatalogModel is one pickable model from the workspace's catalog.
type CatalogModel struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
	Source    string `json:"source"`
}

// ModelsResponse is the subset of GET /v1/models this client consumes.
type ModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Agents    []Agent          `json:"agents"`
	Providers []ProviderStatus `json:"providers"`
	Models    []CatalogModel   `json:"models"`
}

// CreateAgentInput is the POST /v1/agents body. An empty codename lets the
// server generate one; an empty effort leaves the provider default in place.
type CreateAgentInput struct {
	Codename        string `json:"codename,omitempty"`
	Label           string `json:"label,omitempty"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}
