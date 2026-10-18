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
	policy := &Policy{Yolo: true, Prompter: &p}
	ok, err := policy.Approve(context.Background(), Request{Tool: "Bash"})
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.asked != 0 {
		t.Fatal("prompted despite yolo")
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
