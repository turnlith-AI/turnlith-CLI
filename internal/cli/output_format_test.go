package cli

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
)

func decodeLines(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var events []map[string]any
	sc := bufio.NewScanner(strings.NewReader(stdout))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("stdout line is not JSON: %q (%v)\nfull stdout:\n%s", line, err, stdout)
		}
		events = append(events, ev)
	}
	return events
}

func TestOutputFormatJSONIsOneResultObject(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{textChunk(t, "The answer "), finalChunk(t, "is 42.", 1200, 7)}
	})
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "--output-format", "json", "what is the answer")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	events := decodeLines(t, res.stdout)
	if len(events) != 1 {
		t.Fatalf("json mode must print exactly one object, got %d:\n%s", len(events), res.stdout)
	}
	ev := events[0]
	if ev["type"] != "result" || ev["is_error"] != false || ev["result"] != "The answer is 42." || ev["agent"] != "sigma" {
		t.Fatalf("result = %v", ev)
	}
	if ev["turns"] != float64(1) || ev["tool_calls"] != float64(0) {
		t.Fatalf("counters = %v", ev)
	}
	usage, _ := ev["usage"].(map[string]any)
	if usage["context_tokens"] != float64(1200) || usage["output_tokens"] != float64(7) {
		t.Fatalf("usage = %v", usage)
	}
}

func TestOutputFormatJSONReportsFailuresAndExitsNonZero(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string { return nil })
	srv.chatFail = func(int) (int, string) { return 400, `{"error":{"message":"context too large"}}` }
	setupEnv(t, srv.URL)

	res := runTLC(t, t.TempDir(), "-p", "--output-format=json", "hi")
	if res.code != 1 {
		t.Fatalf("code=%d", res.code)
	}
	events := decodeLines(t, res.stdout)
	if len(events) != 1 || events[0]["is_error"] != true || !strings.Contains(events[0]["error"].(string), "context too large") {
		t.Fatalf("stdout:\n%s", res.stdout)
	}
	mustContain(t, "stderr", res.stderr, "context too large") // humans still get the message
}

func TestOutputFormatStreamJSONEmitsEventsThenResult(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "n.txt"), []byte("marker-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		if turn == 0 {
			return []string{textChunk(t, "Checking. "), toolChunk(t, "call_1", "Read", `{"file_path":"n.txt"}`)}
		}
		return []string{finalChunk(t, "It says marker-line.", 800, 9)}
	})
	setupEnv(t, srv.URL)

	res := runTLC(t, dir, "-p", "--output-format", "stream-json", "read n.txt")
	if res.code != 0 {
		t.Fatalf("code=%d stderr=%q", res.code, res.stderr)
	}
	events := decodeLines(t, res.stdout)
	var kinds []string
	var text strings.Builder
	for _, ev := range events {
		kinds = append(kinds, ev["type"].(string))
		if ev["type"] == "text" {
			text.WriteString(ev["delta"].(string))
		}
	}
	if kinds[len(kinds)-1] != "result" {
		t.Fatalf("last event must be the result: %v", kinds)
	}
	want := []string{"text", "tool_use", "tool_result", "text", "result"}
	if got := collapse(kinds); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event order = %v, want %v", got, want)
	}
	if text.String() != "Checking. It says marker-line." {
		t.Fatalf("streamed text = %q", text.String())
	}
	for _, ev := range events {
		switch ev["type"] {
		case "tool_use":
			if ev["name"] != "Read" || ev["input"].(map[string]any)["file_path"] != "n.txt" {
				t.Fatalf("tool_use = %v", ev)
			}
		case "tool_result":
			if ev["name"] != "Read" || ev["is_error"] != false || !strings.Contains(ev["content"].(string), "marker-line") {
				t.Fatalf("tool_result = %v", ev)
			}
		case "result":
			if ev["result"] != "It says marker-line." || ev["tool_calls"] != float64(1) || ev["turns"] != float64(2) {
				t.Fatalf("result = %v", ev)
			}
		}
	}
}

// collapse merges runs of the same event kind: text arrives in many deltas.
func collapse(kinds []string) []string {
	var out []string
	for _, k := range kinds {
		if len(out) == 0 || out[len(out)-1] != k {
			out = append(out, k)
		}
	}
	return out
}

func TestOutputFormatTextIsUnchanged(t *testing.T) {
	srv := newMockServer(t, testAgents(), func(turn int, body api.ChatRequest) []string {
		return []string{finalChunk(t, "plain words", 10, 2)}
	})
	setupEnv(t, srv.URL)
	res := runTLC(t, t.TempDir(), "-p", "--output-format", "text", "hi")
	if res.code != 0 || strings.TrimSpace(res.stdout) != "plain words" {
		t.Fatalf("code=%d stdout=%q", res.code, res.stdout)
	}
}
