package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BashTool runs shell commands in the project root.
type BashTool struct {
	Root           string
	DefaultTimeout time.Duration
	MaxTimeout     time.Duration
}

func (t *BashTool) Name() string { return "Bash" }

func (t *BashTool) Description() string {
	return "Run a shell command in the working directory and return its output (stdout, stderr, " +
		"exit code). Use it for builds, tests, git, and anything without a dedicated tool. " +
		"Commands that need input, or long-running servers, will not work — use timeouts."
}

func (t *BashTool) Schema() json.RawMessage {
	return schema(map[string]any{
		"command":    prop("string", "The shell command to run."),
		"timeout_ms": prop("integer", "Optional timeout in milliseconds (default 120000, max 600000)."),
	}, "command")
}

func (t *BashTool) Gated() bool { return true }

type bashArgs struct {
	Command   string `json:"command"`
	TimeoutMS int    `json:"timeout_ms"`
}

func (t *BashTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var args bashArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return "", errors.New("command is empty")
	}

	timeout := t.DefaultTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if args.TimeoutMS > 0 {
		timeout = time.Duration(args.TimeoutMS) * time.Millisecond
	}
	maxTimeout := t.MaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = 10 * time.Minute
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell := "sh"
	if path, err := exec.LookPath("bash"); err == nil {
		shell = path
	}
	cmd := exec.CommandContext(ctx, shell, "-c", command)
	cmd.Dir = t.Root
	stdout := newLimitedBuffer(1024 * 1024)
	stderr := newLimitedBuffer(1024 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("cannot start command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var runErr error
	timedOut := false
	select {
	case runErr = <-done:
	case <-ctx.Done():
		killProcessGroup(cmd)
		runErr = <-done
		timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	}

	var b strings.Builder
	out := strings.TrimRight(stdout.String(), "\n")
	errOut := strings.TrimRight(stderr.String(), "\n")
	if out != "" {
		b.WriteString(out)
	}
	if errOut != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[stderr]\n")
		b.WriteString(errOut)
	}
	if b.Len() == 0 {
		b.WriteString("(no output)")
	}
	var exitErr *exec.ExitError
	switch {
	case timedOut:
		fmt.Fprintf(&b, "\nError: command timed out after %s", timeout)
	case errors.As(runErr, &exitErr):
		fmt.Fprintf(&b, "\n(exit code %d)", exitErr.ExitCode())
	case runErr != nil:
		fmt.Fprintf(&b, "\nError: %v", runErr)
	}
	return truncateText(b.String(), 30000), nil
}
