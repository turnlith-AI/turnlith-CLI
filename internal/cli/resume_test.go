package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/history"
)

func seedSession(t *testing.T, cwd, agent string, msgs ...string) {
	t.Helper()
	s, err := history.Start(cwd, agent)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		s.Append(role, m)
	}
	_ = s.Close()
}

func TestResumeListsAndPicksEarlierSessions(t *testing.T) {
	setupEnv(t, "http://127.0.0.1:1")
	a, out := testApp(t, "http://127.0.0.1:1", append(testAgents(), agentNamed("lil-coder")))
	a.cwd = t.TempDir()
	seedSession(t, a.cwd, "sigma", "first question about auth", "first answer")
	seedSession(t, a.cwd, "lil-coder", "second question about caching", "second answer", "third")

	a.handleSlash("/resume")
	got := out.String()
	mustContain(t, "list", got, "second question about caching")
	mustContain(t, "list", got, "first question about auth")
	mustContain(t, "list", got, "lil-coder")
	mustContain(t, "list", got, "/resume <number>")

	out.Reset()
	a.handleSlash("/resume 2") // the older one, with sigma
	mustContain(t, "resume", out.String(), "first question about auth")
	if a.agent.Codename != "sigma" {
		t.Fatalf("resuming should switch to the session's agent, got %q", a.agent.Codename)
	}
	if a.transcript == nil || !strings.Contains(a.transcript.Path, ".jsonl") || a.transcript.Agent != "sigma" {
		t.Fatalf("transcript = %+v", a.transcript)
	}
	a.transcript.Append("user", "continuing")
	a.transcript.Close()
	infos, _ := history.List(a.cwd, 5)
	found := false
	for _, in := range infos {
		if in.Preview == "first question about auth" && in.Messages == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("new messages must land in the resumed file: %+v", infos)
	}

	out.Reset()
	a.handleSlash("/resume 9")
	mustContain(t, "bad index", out.String(), "no session 9")
}

func TestResumeWithNothingStored(t *testing.T) {
	setupEnv(t, "http://127.0.0.1:1")
	a, out := testApp(t, "http://127.0.0.1:1", testAgents())
	a.cwd = t.TempDir()
	a.handleSlash("/resume")
	mustContain(t, "output", out.String(), "no earlier sessions")
}

func TestAgoIsHumane(t *testing.T) {
	now := time.Now()
	cases := map[time.Duration]string{
		10 * time.Second:     "just now",
		5 * time.Minute:      "5m ago",
		3 * time.Hour:        "3h ago",
		50 * time.Hour:       "2d ago",
		24 * 40 * time.Hour:  "5w ago",
		24 * 400 * time.Hour: "1y ago",
	}
	for d, want := range cases {
		if got := ago(now.Add(-d), now); got != want {
			t.Errorf("ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func agentNamed(name string) api.Agent {
	return api.Agent{ID: "id-" + name, Codename: name, Model: "gemini:gemini-3.8-flash"}
}
