package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

// Client talks to one turnlith server with one api key.
type Client struct {
	BaseURL string
	APIKey  string
	hc      *http.Client

	userAgent string
	init      mcpInit
}

// Version is stamped onto requests; the CLI sets it at startup.
func (c *Client) SetUserAgent(ua string) { c.userAgent = ua }

const ClientVersion = version.Version

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 120 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
		userAgent: "tlc/" + ClientVersion,
	}
}

// APIError is a non-2xx response, carrying the server's problem detail.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("server returned HTTP %d", e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("%s (%s, HTTP %d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Status)
}

// NewRequest builds a request with auth and JSON headers applied.
func (c *Client) NewRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+c.APIKey)
	req.Header.Set("user-agent", c.userAgent)
	return req, nil
}

// Do sends a request and decodes a JSON response, or returns an *APIError.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	req, err := c.NewRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return parseAPIError(res)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Health checks GET /health.
func (c *Client) Health(ctx context.Context) error {
	return c.Do(ctx, http.MethodGet, "/health", nil, nil)
}

// ListModels fetches GET /v1/models: the org's agents, the pickable model
// catalog, and per-provider capabilities (reasoning effort).
func (c *Client) ListModels(ctx context.Context) (ModelsResponse, error) {
	var models ModelsResponse
	err := c.Do(ctx, http.MethodGet, "/v1/models", nil, &models)
	return models, err
}

// ListAgents fetches GET /v1/models and returns the org's agents.
func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	models, err := c.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return models.Agents, nil
}

// CreateAgent creates an agent (POST /v1/agents). Agent management is
// admin-gated: an agent-role key gets a 403 back.
func (c *Client) CreateAgent(ctx context.Context, input CreateAgentInput) (Agent, error) {
	var agent Agent
	err := c.Do(ctx, http.MethodPost, "/v1/agents", input, &agent)
	return agent, err
}

// UpdateAgentConfig patches an agent's model and reasoning effort
// (PATCH /v1/agents/:id); the level is always set, nil clearing it. The
// server mirrors the change onto the agent's live session.
func (c *Client) UpdateAgentConfig(ctx context.Context, id string, patch AgentConfigPatch) (Agent, error) {
	var agent Agent
	err := c.Do(ctx, http.MethodPatch, "/v1/agents/"+id, patch, &agent)
	return agent, err
}

// StreamHandlers receives incremental output while a turn streams.
type StreamHandlers struct {
	OnText func(delta string)
}

// StreamChat runs one streamed turn. It returns once the model finished the
// turn (text and/or tool calls); the caller executes calls and, if any, calls
// again with the replayed message history.
func (c *Client) StreamChat(ctx context.Context, req ChatRequest, h StreamHandlers) (TurnResult, error) {
	req.Stream = true
	httpReq, err := c.NewRequest(ctx, http.MethodPost, "/v1/chat/completions", req)
	if err != nil {
		return TurnResult{}, err
	}
	httpReq.Header.Set("accept", "text/event-stream")

	res, err := c.hc.Do(httpReq)
	if err != nil {
		return TurnResult{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return TurnResult{}, parseAPIError(res)
	}

	var (
		result    TurnResult
		callOrder []int
		calls     = map[int]*ToolCall{}
	)
	sse := NewSSEReader(res.Body)
	for {
		payload, err := sse.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			// A malformed frame is not worth aborting the turn; skip it.
			continue
		}
		if chunk.Error != nil {
			return result, &APIError{Status: res.StatusCode, Code: chunk.Error.Code, Message: chunk.Error.Message}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				result.Text += choice.Delta.Content
				if h.OnText != nil {
					h.OnText(choice.Delta.Content)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				call, ok := calls[tc.Index]
				if !ok {
					call = &ToolCall{Type: "function"}
					calls[tc.Index] = call
					callOrder = append(callOrder, tc.Index)
				}
				if tc.ID != "" {
					call.ID = tc.ID
				}
				if tc.Function.Name != "" {
					call.Function.Name = tc.Function.Name
				}
				call.Function.Arguments += tc.Function.Arguments
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				result.FinishReason = *choice.FinishReason
			}
		}
		if chunk.Usage != nil {
			result.Usage = *chunk.Usage
		}
	}
	for _, idx := range callOrder {
		result.ToolCalls = append(result.ToolCalls, *calls[idx])
	}
	return result, nil
}

type streamChunk struct {
	Choices []struct {
		Index        int     `json:"index"`
		Delta        delta   `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

type delta struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		Index    int    `json:"index"`
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
}

// parseAPIError reads an error response in either the facade's OpenAI shape
// ({"error":{"message","code"}}) or the API's problem+json shape
// ({"detail","title"}).
func parseAPIError(res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	apiErr := &APIError{Status: res.StatusCode}

	var openai struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(body, &openai); err == nil {
		if openai.Error != nil && openai.Error.Message != "" {
			apiErr.Message = openai.Error.Message
			apiErr.Code = openai.Error.Code
			return apiErr
		}
		if openai.Detail != "" {
			apiErr.Message = openai.Detail
			return apiErr
		}
		if openai.Title != "" {
			apiErr.Message = openai.Title
			return apiErr
		}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	apiErr.Message = text
	return apiErr
}
