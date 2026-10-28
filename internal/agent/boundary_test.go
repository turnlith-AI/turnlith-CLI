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
	"sync/atomic"
	"testing"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/permissions"
	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// oneToolServer answers the first chat request with a single tool call and the
// second with plain text; it records the tool result the model was shown.
func oneToolServer(t *testing.T, tool, args string, result *atomic.Value) *httptest.Server {
	t.Helper()
	var n atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body api.ChatRequest
		_ = decodeJSON(r, &body)
		w.Header().Set("content-type", "text/event-stream")
		if n.Add(1) == 1 {
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", tool, args)
		} else {
			last := body.Messages[len(body.Messages)-1]
			result.Store(last.Content.(string))
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

type recordingPrompter struct {
	reqs   []permissions.Request
	answer permissions.Decision
}

func (p *recordingPrompter) Prompt(req permissions.Request) (permissions.Decision, error) {
	p.reqs = append(p.reqs, req)
	return p.answer, nil
}

func runOneTool(t *testing.T, mode permissions.Mode, prompter *recordingPrompter, dir, tool, args string, alreadyRead ...string) string {
	t.Helper()
	var result atomic.Value
	srv := oneToolServer(t, tool, args, &result)
	defer srv.Close()
	tracker := tools.NewTracker()
	for _, p := range alreadyRead {
		tracker.MarkRead(p)
	}
	runner := &Runner{
		Client: api.New(srv.URL, "sk-test"),
		Tools: tools.NewRegistry(
			&tools.ReadTool{Root: dir, Tracker: tracker},
			&tools.EditTool{Root: dir, Tracker: tracker},
		),
		Policy:       &permissions.Policy{Mode: mode, Prompter: prompter},
		Agent:        "helper",
		SystemPrompt: "test",
	}
	if err := runner.Run(context.Background(), "go", Events{}); err != nil {
		t.Fatal(err)
	}
	got, _ := result.Load().(string)
	return got
}

func TestReadOutsideWorkspaceAsksAndDenialReachesTheModel(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &recordingPrompter{answer: permissions.Deny}
	got := runOneTool(t, permissions.ModeAsk, prompter, dir, "Read", fmt.Sprintf(`{"file_path":%q}`, outside))

	if len(prompter.reqs) != 1 || !prompter.reqs[0].External || !strings.Contains(prompter.reqs[0].Reason, "outside the working directory") {
		t.Fatalf("expected one external prompt, got %+v", prompter.reqs)
	}
	if !strings.Contains(got, "denied permission") || strings.Contains(got, "not yours") {
		t.Fatalf("model saw %q", got)
	}
}

func TestReadOutsideWorkspaceRunsWhenAllowed(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("shared notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &recordingPrompter{answer: permissions.AllowOnce}
	got := runOneTool(t, permissions.ModeAsk, prompter, dir, "Read", fmt.Sprintf(`{"file_path":%q}`, outside))
	if !strings.Contains(got, "shared notes") {
		t.Fatalf("model saw %q", got)
	}
}

func TestReadInsideWorkspaceNeverPrompts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &recordingPrompter{answer: permissions.Deny}
	got := runOneTool(t, permissions.ModeAsk, prompter, dir, "Read", `{"file_path":"a.txt"}`)
	if len(prompter.reqs) != 0 || !strings.Contains(got, "mine") {
		t.Fatalf("prompts=%d result=%q", len(prompter.reqs), got)
	}
}

func TestAutoModeStillAsksForEditsOutsideTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "e.txt")
	if err := os.WriteFile(outside, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &recordingPrompter{answer: permissions.Deny}
	// the model has read the file already (a prompt for that is covered above)
	runOneTool(t, permissions.ModeAuto, prompter, dir, "Edit", fmt.Sprintf(`{"file_path":%q,"old_string":"old","new_string":"new"}`, outside), outside)
	if len(prompter.reqs) != 1 {
		t.Fatalf("auto mode must ask for an outside edit; prompts=%d", len(prompter.reqs))
	}
	if data, _ := os.ReadFile(outside); string(data) != "old" {
		t.Fatalf("denied edit was applied: %q", data)
	}
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func TestAnEditThatCannotWorkNeverAsksForPermission(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &recordingPrompter{answer: permissions.AllowOnce}
	// never read, and the text is not there: both mean the edit could not run
	got := runOneTool(t, permissions.ModeAsk, prompter, dir, "Edit", `{"file_path":"a.txt","old_string":"nope","new_string":"x"}`)
	if len(prompter.reqs) != 0 {
		t.Fatalf("asked the user to approve an edit that cannot work: %+v", prompter.reqs)
	}
	if !strings.Contains(got, "has not been read") {
		t.Fatalf("the model should learn why: %q", got)
	}

	got = runOneTool(t, permissions.ModeAsk, prompter, dir, "Edit", `{"file_path":"a.txt","old_string":"nope","new_string":"x"}`, filepath.Join(dir, "a.txt"))
	if len(prompter.reqs) != 0 || !strings.Contains(got, "not found") {
		t.Fatalf("prompts=%d result=%q", len(prompter.reqs), got)
	}
}

func TestPermissionPromptShowsTheRealDiff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompter := &recordingPrompter{answer: permissions.Deny}
	runOneTool(t, permissions.ModeAsk, prompter, dir, "Edit", `{"file_path":"a.go","old_string":"two","new_string":"TWO"}`, path)
	if len(prompter.reqs) != 1 {
		t.Fatalf("prompts = %d", len(prompter.reqs))
	}
	prev := prompter.reqs[0].Preview
	if !strings.Contains(prev, "   2 - two") || !strings.Contains(prev, "   2 + TWO") {
		t.Fatalf("preview should be a numbered diff, got:\n%s", prev)
	}
}
