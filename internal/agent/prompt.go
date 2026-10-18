// Package agent runs the coding-agent loop: one user prompt in, a streamed
// assistant turn with tool calls executed locally, repeated until the model
// stops calling tools. Conversation memory lives on the turnlith server.
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// PromptEnv describes the local environment injected into the harness prompt.
type PromptEnv struct {
	Cwd           string
	Agent         string
	Model         string
	HasMemoryTool bool
}

// SystemPrompt builds the harness system prompt the facade merges next to the
// agent's own server-side instructions. Keep it lean: it shares the agent's
// context budget with everything else.
func SystemPrompt(env PromptEnv) string {
	var b strings.Builder
	b.WriteString("You are tlc, a coding agent running in the user's terminal, backed by turnlith's session memory.\n\n")

	b.WriteString("Environment:\n")
	fmt.Fprintf(&b, "- Working directory: %s\n", env.Cwd)
	fmt.Fprintf(&b, "- Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "- Date: %s\n", time.Now().Format("2006-01-02"))
	if branch := gitBranch(env.Cwd); branch != "" {
		fmt.Fprintf(&b, "- Git branch: %s\n", branch)
	}
	if env.Agent != "" {
		fmt.Fprintf(&b, "- Agent: %s", env.Agent)
		if env.Model != "" {
			fmt.Fprintf(&b, " (model %s)", env.Model)
		}
		b.WriteByte('\n')
	}

	b.WriteString(`
You help with software engineering tasks: writing code, debugging, refactoring, and explaining.

Tool use:
- Prefer the dedicated tools (Read, Glob, Grep, Edit, Write) over shell equivalents.
- Read a file before editing it; never guess file contents.
- Use Bash for builds, tests, git, and commands that have no dedicated tool.
- Never claim you read a file or ran a command unless you actually called the tool.
- Verify changes by running the project's tests or build when practical.
- Batch independent lookups; stop exploring once you have what you need.

Style:
- Be concise. No preamble, no filler, no restating the request.
- Explain only non-obvious decisions.
- Reference code as path:line.
- Use markdown; keep code blocks short.
`)
	if env.HasMemoryTool {
		b.WriteString(`
Memory: the conversation is stored on the turnlith server and older context is retrieved ` +
			`automatically; use SearchMemory when you need details from earlier work that is not in ` +
			`the current context.
`)
	}
	b.WriteString(`
Safety:
- Never run destructive commands (rm -rf, git reset --hard, force pushes) unless the user explicitly asked.
- Ask before actions affecting the network, credentials, or files outside the working directory.
`)
	return b.String()
}

// gitBranch reports the current branch, or "" outside a repository.
func gitBranch(cwd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
