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
	// AllowAlways remembers the call's rule (Bash: its command prefix) for
	// the rest of the session.
	AllowAlways
	Deny
	// DenyAlways remembers a deny rule for the rest of the session.
	DenyAlways
	// AllowProject is AllowAlways, saved for this project so later sessions
	// start with the rule.
	AllowProject
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
	// Subject is what rules match against: the full command for Bash, the
	// workspace-relative path for file tools.
	Subject string
	// External marks a call that reaches outside the working directory. Such
	// calls are never waved through by auto mode, and an "always" answer is
	// remembered separately for them. Reason says why, for the prompt.
	External bool
	Reason   string
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
	// ModePlan is read-only: the agent can look around and plan, but edits
	// and commands are refused (read-only builtins like git status still run).
	ModePlan
	// ModeYolo runs every gated tool without asking.
	ModeYolo
)

// modes lists the words /permissions accepts, in cycle order.
var modes = []Mode{ModeAsk, ModeAuto, ModePlan, ModeYolo}

func (m Mode) String() string {
	switch m {
	case ModeAuto:
		return "auto"
	case ModePlan:
		return "plan"
	case ModeYolo:
		return "yolo"
	default:
		return "normal"
	}
}

// NextMode returns the mode after m in cycle order: normal → auto → plan →
// yolo → normal.
func NextMode(m Mode) Mode {
	return modes[(int(m)+1)%len(modes)]
}

// NextSafeMode is the keyboard cycle (Tab on an empty prompt): normal → auto
// → plan → normal. It never lands on yolo, which has to be asked for by name;
// pressing it in yolo returns to normal.
func NextSafeMode(m Mode) Mode {
	switch m {
	case ModeAsk:
		return ModeAuto
	case ModeAuto:
		return ModePlan
	default:
		return ModeAsk
	}
}

// Description explains what a mode does, for /status and the banner.
func (m Mode) Description() string {
	switch m {
	case ModeAuto:
		return "auto — Write/Edit run without asking, Bash still asks"
	case ModePlan:
		return "plan — read-only: the agent can look and plan, not edit or run commands"
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

// FeedbackTaker is implemented by prompters that can collect a reason along
// with a denial ("use the helper in util.go instead"). TakeFeedback returns
// and clears the reason given for the most recent denial.
type FeedbackTaker interface {
	TakeFeedback() string
}

// RuleStore persists "always allow in this project" answers.
type RuleStore interface {
	SaveAllow(rule Rule) error
}

// Policy is the session permission state.
type Policy struct {
	// Mode decides the baseline: which gated tools may run without asking.
	Mode Mode
	// Prompter is consulted for gated tools not covered by the mode or a rule.
	Prompter Prompter
	// Store, when set, saves AllowProject answers for later sessions.
	Store RuleStore

	allow []Rule
	deny  []Rule

	// feedback is the user's reason for the latest denial, handed to the
	// model by DenialFor.
	feedback string
}

// AddAllow and AddDeny register rules (from settings, flags, or answers).
func (p *Policy) AddAllow(r Rule) { p.allow = append(p.allow, r) }
func (p *Policy) AddDeny(r Rule)  { p.deny = append(p.deny, r) }

// Rules returns the registered allow and deny rules, for /status.
func (p *Policy) Rules() (allow, deny []Rule) {
	return append([]Rule(nil), p.allow...), append([]Rule(nil), p.deny...)
}

// denied reports whether a deny rule covers the call. For Bash, one denied
// simple command anywhere in a chain denies the whole line.
func (p *Policy) denied(req Request) bool {
	for _, r := range p.deny {
		if r.Tool != req.Tool || r.External != req.External {
			continue
		}
		if req.Tool != "Bash" {
			if r.matchesSubject(req.Subject) {
				return true
			}
			continue
		}
		if r.Pattern == "" {
			return true
		}
		segs, _ := splitCommand(req.Subject)
		for _, seg := range append(segs, strings.TrimSpace(req.Subject)) {
			if r.matchesSegment(seg) {
				return true
			}
		}
	}
	return false
}

// allowed reports whether a rule (or the read-only builtins) covers the call.
// A Bash line is covered only when every simple command in it is, and never
// when it contains substitution or redirection.
func (p *Policy) allowed(req Request) bool {
	if req.Tool != "Bash" {
		for _, r := range p.allow {
			if r.Tool == req.Tool && r.External == req.External && r.matchesSubject(req.Subject) {
				return true
			}
		}
		return false
	}
	if req.External {
		return false
	}
	var rules []Rule
	for _, r := range p.allow {
		if r.Tool != "Bash" {
			continue
		}
		if r.Pattern == "" {
			return true // a deliberate blanket rule
		}
		rules = append(rules, r)
	}
	segs, safe := splitCommand(req.Subject)
	if !safe || len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		ok := builtinReadOnly(seg)
		for _, r := range rules {
			if ok {
				break
			}
			ok = r.matchesSegment(seg)
		}
		if !ok {
			return false
		}
	}
	return true
}

func (p *Policy) takeFeedback() {
	p.feedback = ""
	if ft, ok := p.Prompter.(FeedbackTaker); ok {
		p.feedback = strings.TrimSpace(ft.TakeFeedback())
	}
}

// planAllows is plan mode's whole policy: nothing that changes anything.
// Gated tools are refused outright; the only Bash that runs is the read-only
// builtin set, whatever allow rules the user has.
func planAllows(req Request) bool {
	if req.Tool != "Bash" || req.External {
		return false
	}
	segs, safe := splitCommand(req.Subject)
	if !safe || len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if !builtinReadOnly(seg) {
			return false
		}
	}
	return true
}

// DenialFor is the tool result handed to the model when a call is refused:
// in plan mode it explains the mode instead of implying the user said no.
func (p *Policy) DenialFor(tool string) string {
	if p.Mode == ModePlan {
		return fmt.Sprintf("Plan mode is on: %s was not run. You may read, search and run read-only git commands, but not change files or run other commands. Finish by presenting your plan to the user; they will switch out of plan mode to let you carry it out.", tool)
	}
	if fb := p.feedback; fb != "" {
		p.feedback = ""
		return fmt.Sprintf("The user denied permission to run %s and said: %q. Follow that guidance instead of retrying the same call; if it is unclear, ask the user.", tool, fb)
	}
	return DenialMessage(tool)
}

// sessionRule is the rule an "always"/"never" answer records: the suggested
// one, or for outside-the-workspace calls a tool-wide rule scoped to those.
func sessionRule(req Request) *Rule {
	if rule := SuggestRule(req); rule != nil {
		return rule
	}
	if req.External {
		return &Rule{Tool: req.Tool, External: true}
	}
	return nil
}

// Approve returns whether the tool may run. Order: deny rules (always, even in
// yolo) → yolo → allow rules → auto mode's edits → ask the user. Durable
// answers become rules, so the same family of calls is never asked twice.
func (p *Policy) Approve(ctx context.Context, req Request) (bool, error) {
	if p.denied(req) {
		return false, nil
	}
	if p.Mode == ModePlan {
		return planAllows(req), nil
	}
	if p.Mode == ModeYolo {
		return true, nil
	}
	if p.allowed(req) {
		return true, nil
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
	case AllowAlways, AllowProject:
		if rule := sessionRule(req); rule != nil {
			p.AddAllow(*rule)
			if decision == AllowProject && !req.External && p.Store != nil {
				_ = p.Store.SaveAllow(*rule)
			}
		}
		return true, nil
	case DenyAlways:
		if rule := sessionRule(req); rule != nil {
			p.AddDeny(*rule)
		}
		p.takeFeedback()
		return false, nil
	default:
		p.takeFeedback()
		return false, nil
	}
}

// DenialMessage is the tool result handed back to the model after a denial,
// mirroring how harnesses teach the model to change course.
func DenialMessage(tool string) string {
	return fmt.Sprintf("The user denied permission to run %s. Do not retry it; ask the user how to proceed or continue without it.", tool)
}
