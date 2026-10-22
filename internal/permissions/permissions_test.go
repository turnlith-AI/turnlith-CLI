package permissions

import (
	"context"
	"testing"
)

type queuePrompter struct {
	answers []Decision
	asked   int
}

func (q *queuePrompter) Prompt(req Request) (Decision, error) {
	q.asked++
	if len(q.answers) == 0 {
		return Deny, nil
	}
	answer := q.answers[0]
	q.answers = q.answers[1:]
	return answer, nil
}

func TestYoloBypassesPrompt(t *testing.T) {
	p := queuePrompter{}
	policy := &Policy{Mode: ModeYolo, Prompter: &p}
	ok, err := policy.Approve(context.Background(), Request{Tool: "Bash", Category: CategoryExec})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.asked != 0 {
		t.Fatal("prompted despite yolo")
	}
}

func TestAutoApprovesEditsButAsksForExec(t *testing.T) {
	p := queuePrompter{answers: []Decision{AllowOnce, AllowOnce}}
	policy := &Policy{Mode: ModeAuto, Prompter: &p}

	ok, _ := policy.Approve(context.Background(), Request{Tool: "Edit", Category: CategoryEdit})
	if !ok || p.asked != 0 {
		t.Fatalf("auto should approve edits without asking: ok=%v asked=%d", ok, p.asked)
	}
	ok, _ = policy.Approve(context.Background(), Request{Tool: "Write", Category: CategoryEdit})
	if !ok || p.asked != 0 {
		t.Fatalf("auto should approve writes without asking: ok=%v asked=%d", ok, p.asked)
	}
	ok, _ = policy.Approve(context.Background(), Request{Tool: "Bash", Category: CategoryExec})
	if !ok || p.asked != 1 {
		t.Fatalf("auto should still ask for Bash: ok=%v asked=%d", ok, p.asked)
	}
}

func TestAutoNeverRuleBeatsAutoApprove(t *testing.T) {
	// A "never" rule can only be recorded by a prompt, which happens in
	// normal mode; once recorded it must survive a switch to auto.
	p := queuePrompter{answers: []Decision{DenyAlways}}
	policy := &Policy{Mode: ModeAsk, Prompter: &p}
	if ok, _ := policy.Approve(context.Background(), Request{Tool: "Edit", Category: CategoryEdit}); ok {
		t.Fatal("denial expected")
	}
	policy.Mode = ModeAuto
	if ok, _ := policy.Approve(context.Background(), Request{Tool: "Edit", Category: CategoryEdit}); ok {
		t.Fatal("a session-wide 'never' must beat auto mode")
	}
	if p.asked != 1 {
		t.Fatalf("asked = %d, want 1", p.asked)
	}
}

func TestAskPromptsForEdits(t *testing.T) {
	p := queuePrompter{answers: []Decision{AllowOnce}}
	policy := &Policy{Mode: ModeAsk, Prompter: &p}
	ok, _ := policy.Approve(context.Background(), Request{Tool: "Edit", Category: CategoryEdit})
	if !ok || p.asked != 1 {
		t.Fatalf("normal mode should prompt for edits: ok=%v asked=%d", ok, p.asked)
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
		ok   bool
	}{
		{"normal", ModeAsk, true},
		{"ask", ModeAsk, true},
		{"auto", ModeAuto, true},
		{"yolo", ModeYolo, true},
		{"YOLO", ModeYolo, true},
		{" no ", ModeAsk, true},
		{"y", ModeYolo, true},
		{"au", ModeAuto, true},
		{"a", ModeAsk, false}, // ambiguous: ask/auto
		{"", ModeAsk, false},
		{"wild", ModeAsk, false},
	}
	for _, tc := range cases {
		got, ok := ParseMode(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("ParseMode(%q) = %v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNextModeCycles(t *testing.T) {
	if NextMode(ModeAsk) != ModeAuto || NextMode(ModeAuto) != ModeYolo || NextMode(ModeYolo) != ModeAsk {
		t.Fatal("cycle order must be normal → auto → yolo → normal")
	}
}

func TestModeStrings(t *testing.T) {
	if ModeAsk.String() != "normal" || ModeAuto.String() != "auto" || ModeYolo.String() != "yolo" {
		t.Fatal("mode names must be stable: they are user-facing words")
	}
}

func TestAlwaysRemembers(t *testing.T) {
	p := queuePrompter{answers: []Decision{AllowAlways}}
	policy := &Policy{Prompter: &p}
	for i := 0; i < 3; i++ {
		ok, err := policy.Approve(context.Background(), Request{Tool: "Bash"})
		if err != nil || !ok {
			t.Fatalf("iteration %d: ok=%v err=%v", i, ok, err)
		}
	}
	if p.asked != 1 {
		t.Fatalf("prompted %d times, want 1", p.asked)
	}
}

func TestNeverBlocks(t *testing.T) {
	p := queuePrompter{answers: []Decision{DenyAlways}}
	policy := &Policy{Prompter: &p}
	ok, _ := policy.Approve(context.Background(), Request{Tool: "Write"})
	if ok {
		t.Fatal("deny-always approved")
	}
	ok, _ = policy.Approve(context.Background(), Request{Tool: "Write"})
	if ok || p.asked != 1 {
		t.Fatalf("ok=%v asked=%d", ok, p.asked)
	}
}

func TestDenyOnceDoesNotStick(t *testing.T) {
	p := queuePrompter{answers: []Decision{Deny, AllowOnce}}
	policy := &Policy{Prompter: &p}
	if ok, _ := policy.Approve(context.Background(), Request{Tool: "Edit"}); ok {
		t.Fatal("deny-once approved")
	}
	if ok, _ := policy.Approve(context.Background(), Request{Tool: "Edit"}); !ok {
		t.Fatal("second prompt should allow")
	}
	if p.asked != 2 {
		t.Fatalf("asked = %d", p.asked)
	}
}

func TestNoPrompterDenies(t *testing.T) {
	policy := &Policy{}
	ok, err := policy.Approve(context.Background(), Request{Tool: "Bash"})
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
