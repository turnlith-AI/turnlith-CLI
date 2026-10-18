// Package permissions decides which gated tool calls may run. Safe tools
// never ask; gated tools (Bash, Write, Edit) ask once, and the user can
// allow or deny a tool for the rest of the session.
package permissions

import (
	"context"
	"errors"
	"fmt"
)

// Decision is the user's answer to a permission prompt.
type Decision int

const (
	AllowOnce Decision = iota
	AllowAlways
	Deny
	DenyAlways
)

// Request describes a gated tool call awaiting approval.
type Request struct {
	Tool    string
	Summary string // one-line description of what will happen
	Preview string // optional multi-line detail (e.g. a diff)
}

// Prompter asks the user; implementations live in the UI layer.
type Prompter interface {
	Prompt(req Request) (Decision, error)
}

// ErrAborted signals the prompt was interrupted (Ctrl-C).
var ErrAborted = errors.New("permission prompt aborted")

// Policy is the session permission state.
type Policy struct {
	// Yolo allows every gated tool without asking.
	Yolo bool
	// Prompter is consulted for gated tools not covered by an Always/Never rule.
	Prompter Prompter

	allowAll map[string]bool
	denyAll  map[string]bool
}

// Approve returns whether the tool may run. It records durable per-session
// answers and never asks twice for the same tool.
func (p *Policy) Approve(ctx context.Context, req Request) (bool, error) {
	if p.Yolo {
		return true, nil
	}
	if p.allowAll != nil && p.allowAll[req.Tool] {
		return true, nil
	}
	if p.denyAll != nil && p.denyAll[req.Tool] {
		return false, nil
	}
	if p.Prompter == nil {
		return false, nil
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	decision, err := p.Prompter.Prompt(req)
	if err != nil {
		return false, err
	}
	switch decision {
	case AllowOnce:
		return true, nil
	case AllowAlways:
		if p.allowAll == nil {
			p.allowAll = map[string]bool{}
		}
		p.allowAll[req.Tool] = true
		return true, nil
	case DenyAlways:
		if p.denyAll == nil {
			p.denyAll = map[string]bool{}
		}
		p.denyAll[req.Tool] = true
		return false, nil
	default:
		return false, nil
	}
}

// DenialMessage is the tool result handed back to the model after a denial,
// mirroring how harnesses teach the model to change course.
func DenialMessage(tool string) string {
	return fmt.Sprintf("The user denied permission to run %s. Do not retry it; ask the user how to proceed or continue without it.", tool)
}
