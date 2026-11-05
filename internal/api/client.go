package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
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

	// retryDelays are the waits before each retry of a request the server
	// provably did not start processing (see retryable); its length is the
	// retry budget.
	retryDelays []time.Duration
	// OnRetry, when set, is told before each backoff sleep so the caller can
	// say why the command is taking a while. attempt counts from 1.
	OnRetry func(attempt int, wait time.Duration, err error)
}

// maxRetryAfter caps how long a server's Retry-After can park the client.
const maxRetryAfter = 30 * time.Second

// SetRetryDelays replaces the retry schedule (no arguments disables retries).
func (c *Client) SetRetryDelays(d ...time.Duration) { c.retryDelays = d }

// ClientVersion is stamped onto requests.
const ClientVersion = version.Version

// New returns a client for one server and key.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
		userAgent:   "tlc/" + ClientVersion,
		retryDelays: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
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
// A request the server provably did not process (HTTP 429/502/503, or a
// connection that could not even be opened) is retried with backoff, so a
// momentary hiccup does not fail `tlc install` halfway.
func (c *Client) Do(ctx context.Context, method, path string, body any, out any) error {
	for attempt := 0; ; attempt++ {
		req, err := c.NewRequest(ctx, method, path, body)
		if err != nil {
			return err
		}
		var (
			lastErr    error
			retryAfter string
		)
		res, err := c.hc.Do(req)
		switch {
		case err != nil:
			if ctx.Err() != nil || !isDialError(err) {
				return err
			}
			lastErr = err
		case res.StatusCode >= 200 && res.StatusCode <= 299:
			defer res.Body.Close()
			if out == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				return nil
			}
			return json.NewDecoder(res.Body).Decode(out)
		default:
			apiErr := parseAPIError(res)
			res.Body.Close()
			if !retryable(res.StatusCode) {
				return apiErr
			}
			lastErr = apiErr
			retryAfter = res.Header.Get("Retry-After")
		}

		if attempt >= len(c.retryDelays) {
			return lastErr
		}
		wait := retryDelay(c.retryDelays[attempt], retryAfter)
		if c.OnRetry != nil {
			c.OnRetry(attempt+1, wait, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
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

// retryable reports whether an HTTP status means the server rejected the
// request before doing any work, so sending it again cannot duplicate the
// prompt in the agent's stored history: rate limited, overloaded, or a gateway
// with nothing behind it. 504 is deliberately absent — the upstream may have
// started the turn.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusBadGateway || status == http.StatusServiceUnavailable
}

// isDialError reports a failure to even connect (refused, unreachable, DNS):
// no request bytes were sent.
func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// retryDelay picks the wait before a retry: the server's Retry-After (seconds
// or an HTTP date, capped) when it sent one, the schedule's delay otherwise.
func retryDelay(scheduled time.Duration, retryAfter string) time.Duration {
	retryAfter = strings.TrimSpace(retryAfter)
	if retryAfter == "" {
		return scheduled
	}
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxRetryAfter)
	}
	if at, err := http.ParseTime(retryAfter); err == nil {
		return max(0, min(time.Until(at), maxRetryAfter))
	}
	return scheduled
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
