// Package api is a minimal client for the turnlith HTTP API: the
// OpenAI-compatible chat facade (/v1/chat/completions), the agent list
// (/v1/models), health, and the stateless turnlith-controller MCP endpoint
// (/v1/mcp) that serves memory search.
package api

import "encoding/json"

// Message is one OpenAI-compatible wire message. The facade keeps the last
// user message as the prompt and passes tool traffic (assistant tool_calls +
// role:"tool" results) through to the model, deduplicating replayed results.
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"` // string, or nil for tool-call-only assistant turns
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

func UserMessage(text string) Message   { return Message{Role: "user", Content: text} }
func SystemMessage(text string) Message { return Message{Role: "system", Content: text} }

// AssistantMessage builds an assistant turn. Content is omitted when empty so
// tool-call-only turns serialize as content:null (which the facade accepts).
func AssistantMessage(text string, calls []ToolCall) Message {
	m := Message{Role: "assistant", ToolCalls: calls}
	if text != "" {
		m.Content = text
	}
	return m
}

func ToolResultMessage(callID, name, text string) Message {
	return Message{Role: "tool", Content: text, ToolCallID: callID, Name: name}
}

// ToolCall is a complete model tool invocation.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef offers a client-side tool to the model. The server registers it
// without an executor, so every call comes back over the wire.
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolDefFunc `json:"function"`
}

type ToolDefFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func NewToolDef(name, description string, parameters json.RawMessage) ToolDef {
	return ToolDef{Type: "function", Function: ToolDefFunc{Name: name, Description: description, Parameters: parameters}}
}

// Usage is the token accounting the facade attaches to the final chunk.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Agent is one turnlith agent (its codename is the model id clients call).
type Agent struct {
	ID       string  `json:"id"`
	Codename string  `json:"codename"`
	Label    *string `json:"label"`
	Model    string  `json:"model"`
}

// ModelsResponse is the subset of GET /v1/models this client consumes.
type ModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Agents []Agent `json:"agents"`
}

// ChatRequest is the (streaming) chat-completions body.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
	Stream   bool      `json:"stream"`
}

// TurnResult is the outcome of one streamed model turn.
type TurnResult struct {
	Text         string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
}

// ContextTokens returns the assembled context size of the turn (the facade
// reports it as prompt_tokens).
func (r TurnResult) ContextTokens() int { return r.Usage.PromptTokens }
