package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// TestRunToolRoundTrip drives one full tool round: the mock model asks for a
// Read, the runner executes it locally, replays the result, and the second
// request's message shape is asserted to be the documented continuation.
func TestRunToolRoundTrip(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(file, []byte("hello from disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var requestCount int
	var secondRequest []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []any            `json:"tools"`
			Model    string           `json:"model"`
			Stream   bool             `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		requestCount++
		w.Header().Set("content-type", "text/event-stream")
		if requestCount == 1 {
			if len(body.Tools) == 0 {
				t.Errorf("first request carried no tools")
			}
			if body.Model != "helper" {
				t.Errorf("model = %q", body.Model)
			}
			if body.Messages[0]["role"] != "system" {
				t.Errorf("messages[0] = %#v", body.Messages[0])
			}
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"hello.txt\"}"}}]},"finish_reason":null}]}`)
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":11,"completion_tokens":4,"total_tokens":15}}`)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		secondRequest = body.Messages
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"the file says hello"},"finish_reason":null}]}`)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":30,"completion_tokens":5,"total_tokens":35}}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := api.New(srv.URL, "sk-test")
	registry := tools.NewRegistry(&tools.ReadTool{Root: dir, Tracker: tools.NewTracker()})
	runner := &Runner{
		Client:       client,
		Tools:        registry,
		Policy:       &permissions.Policy{Mode: permissions.ModeYolo},
		Agent:        "helper",
		SystemPrompt: "you are a test",
	}

	var text strings.Builder
	var events []string
	err := runner.Run(context.Background(), "read hello.txt", Events{
		OnText:      func(d string) { text.WriteString(d) },
		OnToolStart: func(name, raw string) { events = append(events, "start:"+name) },
		OnToolEnd:   func(name, result string, denied bool, _ time.Duration) { events = append(events, "end:"+name) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "the file says hello" {
		t.Fatalf("final text = %q", text.String())
	}
	wantEvents := []string{"start:Read", "end:Read"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("events = %v", events)
	}

	if len(secondRequest) != 4 {
		t.Fatalf("continuation messages = %d: %#v", len(secondRequest), secondRequest)
	}
	if secondRequest[1]["role"] != "user" {
		t.Fatalf("messages[1] = %#v", secondRequest[1])
	}
	if secondRequest[2]["role"] != "assistant" || secondRequest[2]["tool_calls"] == nil {
		t.Fatalf("messages[2] = %#v", secondRequest[2])
	}
	toolMsg := secondRequest[3]
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_1" {
		t.Fatalf("messages[3] = %#v", toolMsg)
	}
	content, _ := toolMsg["content"].(string)
	if !strings.Contains(content, "hello from disk") {
		t.Fatalf("tool content = %q", content)
	}
}

// TestRunDeniedToolFeedsModel checks that a denied gated tool becomes a tool
// result the model can read, instead of aborting the run.
func TestRunDeniedToolFeedsModel(t *testing.T) {
	dir := t.TempDir()
	var secondRequest []map[string]any
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		w.Header().Set("content-type", "text/event-stream")
		if calls == 1 {
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"rm -rf /\"}"}}]},"finish_reason":null}]}`)
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		} else {
			secondRequest = body.Messages
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"ok, not doing that"},"finish_reason":null}]}`)
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	runner := &Runner{
		Client: api.New(srv.URL, "sk-test"),
		Tools:  tools.NewRegistry(&tools.BashTool{Root: dir}),
		Policy: &permissions.Policy{
			Prompter: prompterFunc(func(permissions.Request) (permissions.Decision, error) {
				return permissions.Deny, nil
			}),
		},
		Agent:        "helper",
		SystemPrompt: "test",
	}

	denied := false
	err := runner.Run(context.Background(), "clean up", Events{
		OnToolEnd: func(name, result string, wasDenied bool, _ time.Duration) { denied = wasDenied },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("expected the tool result to be marked denied")
	}
	if len(secondRequest) != 4 {
		t.Fatalf("messages = %#v", secondRequest)
	}
	content, _ := secondRequest[3]["content"].(string)
	if !strings.Contains(content, "denied permission") {
		t.Fatalf("denial result = %q", content)
	}
}

type prompterFunc func(permissions.Request) (permissions.Decision, error)

func (f prompterFunc) Prompt(req permissions.Request) (permissions.Decision, error) { return f(req) }

// TestRunUnknownTool ensures a hallucinated tool name round-trips as an error
// result instead of failing the run.
func TestRunUnknownTool(t *testing.T) {
	var calls int
	var lastMessages []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls++
		w.Header().Set("content-type", "text/event-stream")
		if calls == 1 {
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"Teleport","arguments":"{}"}}]},"finish_reason":null}]}`)
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		} else {
			lastMessages = body.Messages
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"sorry"},"finish_reason":null}]}`)
			fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	runner := &Runner{
		Client:       api.New(srv.URL, "sk-test"),
		Tools:        tools.NewRegistry(),
		Policy:       &permissions.Policy{Mode: permissions.ModeYolo},
		Agent:        "helper",
		SystemPrompt: "test",
	}
	if err := runner.Run(context.Background(), "go", Events{}); err != nil {
		t.Fatal(err)
	}
	content, _ := lastMessages[3]["content"].(string)
	if !strings.Contains(content, "unknown tool") {
		t.Fatalf("result = %q", content)
	}
}
