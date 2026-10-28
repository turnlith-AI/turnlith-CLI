// Package permissions decides which gated tool calls may run. Safe tools
// never ask; gated tools (Bash, Write, Edit) consult the session's permission
// mode, and the user can allow or deny a tool for the rest of the session.
package permissions

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Decision is the user's answer to a permission prompt.
type Decision int

const (
	AllowOnce Decision = iota
	AllowAlways
	Deny
	DenyAlways
)

// Tool categories, reported by each tool and consulted by the mode rules.
const (
	CategoryEdit   = "edit"   // Write, Edit: modify files in place
	CategoryExec   = "exec"   // Bash: runs arbitrary commands
	CategoryRead   = "read"   // Read, Glob, Grep
	CategoryMemory = "memory" // SearchMemory
)

// Request describes a gated tool call awaiting approval.
type Request struct {
	Tool     string
	Category string // one of the Category* constants
	Summary  string // one-line description of what will happen
	Preview  string // optional multi-line detail (e.g. a diff)
	// External marks a call that reaches outside the working directory. Such
	// calls are never waved through by auto mode, and an "always" answer is
	// remembered separately for them. Reason says why, for the prompt.
	External bool
	Reason   string
}

// ruleKey is what session allow/deny answers are remembered under: the tool,
// split by whether the call stayed inside the workspace.
func (r Request) ruleKey() string {
	if r.External {
		return r.Tool + "@external"
	}
	return r.Tool
}

// Prompter asks the user; implementations live in the UI layer.
type Prompter interface {
	Prompt(req Request) (Decision, error)
}

// ErrAborted signals the prompt was interrupted (Ctrl-C).
var ErrAborted = errors.New("permission prompt aborted")

// Mode is the session's permission posture.
type Mode int

const (
	// ModeAsk prompts for every gated tool call.
	ModeAsk Mode = iota
	// ModeAuto runs file edits (Write, Edit) without asking; Bash still asks.
	ModeAuto
	// ModeYolo runs every gated tool without asking.
	ModeYolo
)

// modes lists the words /permissions accepts, in cycle order.
var modes = []Mode{ModeAsk, ModeAuto, ModeYolo}

func (m Mode) String() string {
	switch m {
	case ModeAuto:
		return "auto"
	case ModeYolo:
		return "yolo"
	default:
		return "normal"
	}
}

// NextMode returns the mode after m in cycle order: normal → auto → yolo → normal.
func NextMode(m Mode) Mode {
	return modes[(int(m)+1)%len(modes)]
}

// NextSafeMode is the keyboard cycle (Tab on an empty prompt): it alternates
// between the modes that still ask about commands and never lands on yolo,
// which has to be asked for by name. Pressing it in yolo returns to normal.
func NextSafeMode(m Mode) Mode {
	if m == ModeAsk {
		return ModeAuto
	}
	return ModeAsk
}

// Description explains what a mode does, for /status and the banner.
func (m Mode) Description() string {
	switch m {
	case ModeAuto:
		return "auto — Write/Edit run without asking, Bash still asks"
	case ModeYolo:
		return "yolo — no prompts, every tool runs"
	default:
		return "normal — ask before Bash/Write/Edit"
	}
}

// ParseMode reads a mode word: the full names ("normal", "auto", "yolo"),
// "ask" as an alias for normal, or a unique prefix of any of them.
func ParseMode(s string) (Mode, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ModeAsk, false
	}
	var match Mode
	found := false
	for _, m := range modes {
		for _, name := range []string{m.String(), "ask"} {
			if name == "ask" && m != ModeAsk {
				continue
			}
			if name == s {
				return m, true
			}
			if strings.HasPrefix(name, s) {
				if found {
					return ModeAsk, false // ambiguous prefix
				}
				match, found = m, true
			}
		}
	}
	return match, found
}

// Policy is the session permission state.
type Policy struct {
	// Mode decides the baseline: which gated tools may run without asking.
	Mode Mode
	// Prompter is consulted for gated tools not covered by the mode or an
	// Always/Never rule.
	Prompter Prompter

	allowAll map[string]bool
	denyAll  map[string]bool
}

// Approve returns whether the tool may run. It records durable per-session
// answers and never asks twice for the same tool.
func (p *Policy) Approve(ctx context.Context, req Request) (bool, error) {
	if p.Mode == ModeYolo {
		return true, nil
	}
	key := req.ruleKey()
	if p.allowAll != nil && p.allowAll[key] {
		return true, nil
	}
	if p.denyAll != nil && p.denyAll[key] {
		return false, nil
	}
	if p.Mode == ModeAuto && req.Category == CategoryEdit && !req.External {
		return true, nil
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
		p.allowAll[key] = true
		return true, nil
	case DenyAlways:
		if p.denyAll == nil {
			p.denyAll = map[string]bool{}
		}
		p.denyAll[key] = true
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
