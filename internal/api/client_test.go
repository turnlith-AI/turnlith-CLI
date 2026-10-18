package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSSEReader(t *testing.T) {
	stream := strings.Join([]string{
		": ping\n",
		"\n",
		"data: {\"a\":1}\n",
		"\n",
		"data: line one\n",
		"data: line two\n",
		"\n",
		"data: [DONE]\n",
		"\n",
	}, "")
	r := NewSSEReader(strings.NewReader(stream))
	var got []string
	for {
		payload, err := r.Next()
		if err != nil {
			break
		}
		got = append(got, payload)
	}
	want := []string{`{"a":1}`, "line one\nline two", "[DONE]"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event %d: got %q want %q", i, got[i], want[i])
		}
	}
}

// sseServer streams the given events and asserts basic request shape.
func sseServer(t *testing.T, events []string, onRequest func(body map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("authorization"); got != "Bearer sk-test" {
			t.Errorf("auth header = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if onRequest != nil {
			onRequest(body)
		}
		w.Header().Set("content-type", "text/event-stream")
		for _, e := range events {
			fmt.Fprintf(w, "data: %s\n\n", e)
		}
	}))
}

func TestStreamChatText(t *testing.T) {
	events := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"hello "},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"content":"world"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":42,"completion_tokens":2,"total_tokens":44}}`,
		`[DONE]`,
	}
	srv := sseServer(t, events, nil)
	defer srv.Close()

	var streamed strings.Builder
	client := New(srv.URL, "sk-test")
	turn, err := client.StreamChat(context.Background(), ChatRequest{
		Model:    "helper",
		Messages: []Message{SystemMessage("sys"), UserMessage("hi")},
	}, StreamHandlers{OnText: func(d string) { streamed.WriteString(d) }})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != "hello world" || streamed.String() != "hello world" {
		t.Fatalf("text = %q streamed = %q", turn.Text, streamed.String())
	}
	if turn.FinishReason != "stop" || turn.Usage.TotalTokens != 44 {
		t.Fatalf("turn = %+v", turn)
	}
}

func TestStreamChatToolCallsFragmented(t *testing.T) {
	// arguments may arrive in fragments across deltas; the client reassembles
	events := []string{
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"comm"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"a.go\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
		`[DONE]`,
	}
	srv := sseServer(t, events, nil)
	defer srv.Close()

	client := New(srv.URL, "sk-test")
	turn, err := client.StreamChat(context.Background(), ChatRequest{
		Model:    "helper",
		Messages: []Message{UserMessage("do it")},
	}, StreamHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", turn.ToolCalls)
	}
	if turn.ToolCalls[0].Function.Name != "Bash" || turn.ToolCalls[0].Function.Arguments != `{"command":"ls"}` {
		t.Fatalf("call 0 = %+v", turn.ToolCalls[0])
	}
	if turn.ToolCalls[1].ID != "call_2" || turn.ToolCalls[1].Function.Name != "Read" {
		t.Fatalf("call 1 = %+v", turn.ToolCalls[1])
	}
	if turn.FinishReason != "tool_calls" {
		t.Fatalf("finish = %q", turn.FinishReason)
	}
}

func TestStreamChatSendsReplayedRounds(t *testing.T) {
	// continuation requests must replay assistant tool_calls + tool results
	var got []map[string]any
	srv := sseServer(t, []string{
		`{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
	}, func(body map[string]any) {
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			got = append(got, m.(map[string]any))
		}
	})
	defer srv.Close()

	client := New(srv.URL, "sk-test")
	_, err := client.StreamChat(context.Background(), ChatRequest{
		Model: "helper",
		Messages: []Message{
			UserMessage("run ls"),
			AssistantMessage("", []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunc{Name: "Bash", Arguments: `{"command":"ls"}`}}}),
			ToolResultMessage("call_1", "Bash", "a.go"),
		},
	}, StreamHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("messages = %d", len(got))
	}
	if got[1]["role"] != "assistant" || got[1]["tool_calls"] == nil {
		t.Fatalf("assistant round missing: %#v", got[1])
	}
	if got[2]["role"] != "tool" || got[2]["tool_call_id"] != "call_1" {
		t.Fatalf("tool result missing: %#v", got[2])
	}
}

func TestAPIErrorShapes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("content-type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"type":"https://turnlith.dev/errors/unauthorized","title":"Unauthorized","status":401,"detail":"missing or invalid api key"}`)
		case "/v1/chat/completions":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":{"message":"The model 'nope' does not exist","type":"invalid_request_error","code":"model_not_found"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, "sk-test")
	_, err := client.ListAgents(context.Background())
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 401 || apiErr.Message != "missing or invalid api key" {
		t.Fatalf("err = %#v", err)
	}

	_, err = client.StreamChat(context.Background(), ChatRequest{Model: "nope", Messages: []Message{UserMessage("hi")}}, StreamHandlers{})
	apiErr, ok = err.(*APIError)
	if !ok || apiErr.Status != 404 || apiErr.Code != "model_not_found" {
		t.Fatalf("err = %#v", err)
	}
}

func TestCallToolMCP(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mcp" {
			http.NotFound(w, r)
			return
		}
		if !strings.Contains(r.Header.Get("accept"), "text/event-stream") {
			t.Errorf("accept header = %q", r.Header.Get("accept"))
		}
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		methods = append(methods, req.Method)
		w.Header().Set("content-type", "application/json")
		switch req.Method {
		case "initialize":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18"}}`, req.ID)
		case "tools/call":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"[memory] deploy region is eu-west-1"}]}}`, req.ID)
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, "sk-test")
	text, err := client.CallTool(context.Background(), "search_memory", map[string]any{"query": "region"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "[memory] deploy region is eu-west-1" {
		t.Fatalf("text = %q", text)
	}
	if len(methods) != 2 || methods[0] != "initialize" || methods[1] != "tools/call" {
		t.Fatalf("methods = %v", methods)
	}
	// second call must not initialize again
	if _, err := client.CallTool(context.Background(), "search_memory", map[string]any{"query": "x"}); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 3 {
		t.Fatalf("expected no second initialize, got %v", methods)
	}
}
