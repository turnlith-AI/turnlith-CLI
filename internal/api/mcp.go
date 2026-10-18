package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// The MCP surface of turnlith-controller: a stateless streamable-HTTP
// JSON-RPC endpoint at /v1/mcp. One initialize handshake is sent per client,
// then tools/call requests are plain JSON. This client only needs tools/call.

const mcpProtocolVersion = "2025-06-18"

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// mcpInit memoizes the one-time initialize handshake for this client.
type mcpInit struct {
	once sync.Once
	err  error
}

// CallTool invokes an MCP tool on turnlith-controller and returns its text
// content.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	c.init.once.Do(func() { c.init.err = c.initializeMCP(ctx) })
	if c.init.err != nil {
		return "", c.init.err
	}
	raw, err := c.mcpRPC(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		return "", err
	}
	var result mcpToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("unexpected MCP result: %w", err)
	}
	var text string
	for _, part := range result.Content {
		if part.Type == "text" && part.Text != "" {
			if text != "" {
				text += "\n"
			}
			text += part.Text
		}
	}
	if result.IsError {
		return "", fmt.Errorf("%s", text)
	}
	if text == "" {
		return "no results", nil
	}
	return text, nil
}

func (c *Client) initializeMCP(ctx context.Context) error {
	_, err := c.mcpRPC(ctx, rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "tlc", "version": ClientVersion},
		},
	})
	return err
}

func (c *Client) mcpRPC(ctx context.Context, payload rpcRequest) (json.RawMessage, error) {
	req, err := c.NewRequest(ctx, http.MethodPost, "/v1/mcp", payload)
	if err != nil {
		return nil, err
	}
	// the MCP streamable-HTTP transport expects both content types offered
	req.Header.Set("accept", "application/json, text/event-stream")
	req.Header.Set("mcp-protocol-version", mcpProtocolVersion)

	res, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, parseAPIError(res)
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	data := body
	if strings.Contains(res.Header.Get("content-type"), "text/event-stream") {
		data = firstSSEData(body)
	}
	var resp rpcResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("invalid MCP response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func firstSSEData(body []byte) []byte {
	sse := NewSSEReader(bytes.NewReader(body))
	for {
		data, err := sse.Next()
		if err != nil {
			return body
		}
		if data != "" {
			return []byte(data)
		}
	}
}
