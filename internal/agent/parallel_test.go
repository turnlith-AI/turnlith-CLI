package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// probeTool sleeps and records how many calls overlapped.
type probeTool struct {
	name       string
	delay      time.Duration
	concurrent bool
	gated      bool
	running    *atomic.Int32
	peak       *atomic.Int32
}

func (p *probeTool) Name() string            { return p.name }
func (p *probeTool) Description() string     { return "probe" }
func (p *probeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (p *probeTool) Gated() bool             { return p.gated }
func (p *probeTool) Category() string        { return permissions.CategoryRead }
func (p *probeTool) ConcurrencySafe() bool   { return p.concurrent }
func (p *probeTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	n := p.running.Add(1)
	for {
		peak := p.peak.Load()
		if n <= peak || p.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(p.delay)
	p.running.Add(-1)
	var args struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &args)
	return "result-" + args.ID, nil
}

// callsServer asks for the given tool calls in one turn, then finishes; it
// records the tool messages the model got back.
func callsServer(t *testing.T, calls []string, got *[]api.Message) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body api.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "text/event-stream")
		if n.Add(1) == 1 {
			for i, c := range calls {
				parts := strings.SplitN(c, ":", 2) // "Name:id"
				fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":\"c%d\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]}}]}\n\n",
					i, i, parts[0], fmt.Sprintf(`{"id":%q}`, parts[1]))
			}
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			*got = body.Messages
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func runCalls(t *testing.T, reg *tools.Registry, calls []string, policy *permissions.Policy) (msgs []api.Message, events []string, elapsed time.Duration) {
	t.Helper()
	srv := callsServer(t, calls, &msgs)
	defer srv.Close()
	runner := &Runner{Client: api.New(srv.URL, "sk-test"), Tools: reg, Policy: policy, Agent: "a", SystemPrompt: "s"}
	var mu sync.Mutex
	start := time.Now()
	err := runner.Run(context.Background(), "go", Events{
		OnToolStart: func(name, raw string) { mu.Lock(); events = append(events, "start:"+name+raw); mu.Unlock() },
		OnToolEnd: func(o ToolOutcome) {
			mu.Lock()
			events = append(events, "end:"+o.Name+":"+o.Result)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return msgs, events, time.Since(start)
}

func TestIndependentReadOnlyCallsRunConcurrentlyInOrder(t *testing.T) {
	var running, peak atomic.Int32
	mk := func(name string) *probeTool {
		return &probeTool{name: name, delay: 150 * time.Millisecond, concurrent: true, running: &running, peak: &peak}
	}
	reg := tools.NewRegistry(mk("A"), mk("B"), mk("C"))
	msgs, events, elapsed := runCalls(t, reg, []string{"A:1", "B:2", "C:3"}, &permissions.Policy{Mode: permissions.ModeAsk})

	if peak.Load() < 2 {
		t.Fatalf("calls never overlapped (peak %d)", peak.Load())
	}
	if elapsed > 420*time.Millisecond {
		t.Fatalf("3x150ms took %v: not concurrent", elapsed)
	}
	// results reach the model in the order the model asked, not the order they finished
	var toolMsgs []string
	for _, m := range msgs {
		if m.Role == "tool" {
			toolMsgs = append(toolMsgs, m.ToolCallID+"="+m.Content.(string))
		}
	}
	if want := "c0=result-1,c1=result-2,c2=result-3"; strings.Join(toolMsgs, ",") != want {
		t.Fatalf("tool messages = %v", toolMsgs)
	}
	// the UI sees clean start/end pairs, in the order the model asked
	for i := 0; i+1 < len(events); i += 2 {
		if !strings.HasPrefix(events[i], "start:") || !strings.HasPrefix(events[i+1], "end:") {
			t.Fatalf("events must alternate start/end per call, got %v", events)
		}
	}
}

func TestGatedAndUnsafeCallsStaySequential(t *testing.T) {
	var running, peak atomic.Int32
	reg := tools.NewRegistry(
		&probeTool{name: "R", delay: 80 * time.Millisecond, concurrent: true, running: &running, peak: &peak},
		&probeTool{name: "W", delay: 80 * time.Millisecond, concurrent: false, running: &running, peak: &peak},
		&probeTool{name: "G", delay: 80 * time.Millisecond, concurrent: true, gated: true, running: &running, peak: &peak},
	)
	policy := &permissions.Policy{Mode: permissions.ModeYolo}
	runCalls(t, reg, []string{"R:1", "W:2", "G:3", "W:4"}, policy)
	if peak.Load() != 1 {
		t.Fatalf("calls that are not concurrency-safe overlapped (peak %d)", peak.Load())
	}
}

func TestOnlyAdjacentSafeCallsAreBatched(t *testing.T) {
	var running, peak atomic.Int32
	reg := tools.NewRegistry(
		&probeTool{name: "R", delay: 100 * time.Millisecond, concurrent: true, running: &running, peak: &peak},
		&probeTool{name: "W", delay: 100 * time.Millisecond, concurrent: false, running: &running, peak: &peak},
	)
	// R R W R: the first two overlap, W waits for them, the last R runs alone after it
	_, _, elapsed := runCalls(t, reg, []string{"R:1", "R:2", "W:3", "R:4"}, &permissions.Policy{Mode: permissions.ModeYolo})
	if peak.Load() != 2 {
		t.Fatalf("peak = %d, want 2", peak.Load())
	}
	if elapsed < 290*time.Millisecond {
		t.Fatalf("order of effects not preserved: finished in %v", elapsed)
	}
}

func TestParallelBatchIsBounded(t *testing.T) {
	var running, peak atomic.Int32
	reg := tools.NewRegistry(&probeTool{name: "R", delay: 60 * time.Millisecond, concurrent: true, running: &running, peak: &peak})
	calls := make([]string, 10)
	for i := range calls {
		calls[i] = fmt.Sprintf("R:%d", i)
	}
	runCalls(t, reg, calls, &permissions.Policy{Mode: permissions.ModeAsk})
	if p := peak.Load(); p > maxParallelTools || p < 2 {
		t.Fatalf("peak concurrency = %d, want 2..%d", p, maxParallelTools)
	}
}
