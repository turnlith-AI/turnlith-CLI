package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// withPendingNotes prefixes the next prompt with anything the model should
// know that happened outside its own tool calls (a change the user reverted),
// once.
func (a *app) withPendingNotes(prompt string) string {
	if len(a.pendingNotes) == 0 {
		return prompt
	}
	var b strings.Builder
	for _, n := range a.pendingNotes {
		b.WriteString("[tlc notice: " + n + "]\n")
	}
	a.pendingNotes = nil
	return b.String() + "\n" + prompt
}

func (a *app) slashUndo(args []string) bool {
	force := len(args) > 0 && strings.EqualFold(args[0], "force")
	res, err := a.tracker.Undo(force)
	switch {
	case errors.Is(err, tools.ErrNothingToUndo):
		a.renderer.Notice("nothing to undo — no file changes have been made in this session")
	case errors.Is(err, tools.ErrChangedSince):
		a.renderer.Errorf("%s changed since the agent's edit — undoing would discard those changes. Review with /diff, or run /undo force to restore the old contents anyway", shortPath(res.Path))
	case err != nil:
		a.renderer.Errorf("could not undo: %v", err)
	default:
		name := shortPath(res.Path)
		if rel, err := filepath.Rel(a.cwd, res.Path); err == nil && !strings.HasPrefix(rel, "..") {
			name = filepath.ToSlash(rel)
		}
		if res.Removed {
			a.renderer.Infof("%s removed %s (the agent had created it)", a.theme.Green("↩"), name)
			a.pendingNotes = append(a.pendingNotes, fmt.Sprintf("the user reverted your change with /undo: %s, which you created, was deleted. Read the project again before relying on it.", name))
		} else {
			a.renderer.Infof("%s restored %s", a.theme.Green("↩"), name)
			a.pendingNotes = append(a.pendingNotes, fmt.Sprintf("the user reverted your last change to %s with /undo. The file is back to its earlier contents; read it again before editing it.", name))
		}
		if res.Left > 0 {
			a.renderer.Notice(fmt.Sprintf("%d more change(s) can be undone", res.Left))
		}
	}
	return false
}

// diffCap bounds /diff output.
const diffCap = 300

func (a *app) slashDiff(_ []string) bool {
	var files []string
	for _, p := range a.tracker.ChangedFiles() {
		rel, err := filepath.Rel(a.root, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue // outside the project: git cannot show it
		}
		files = append(files, filepath.ToSlash(rel))
	}
	if len(files) == 0 {
		a.renderer.Notice("no files changed in this session")
		return false
	}
	git, err := exec.LookPath("git")
	if err != nil || !gitSucceeds(git, a.root, "rev-parse", "--is-inside-work-tree") {
		a.renderer.Notice("/diff needs git in a repository; files changed this session (use /undo to revert):")
		for _, f := range files {
			a.renderer.Infof("  %s", f)
		}
		return false
	}

	color := "--color=never"
	if a.theme.Colors {
		color = "--color=always"
	}
	var out bytes.Buffer
	tracked := gitOutput(git, a.root, append([]string{"diff", color, "--"}, files...)...)
	out.WriteString(tracked)
	// files git has not seen yet are invisible to `git diff`: show them as additions
	untracked := strings.Fields(gitOutput(git, a.root, append([]string{"ls-files", "--others", "--exclude-standard", "--"}, files...)...))
	for _, f := range untracked {
		out.WriteString(gitOutput(git, a.root, "diff", color, "--no-index", "--", os.DevNull, f))
	}
	if strings.TrimSpace(out.String()) == "" {
		a.renderer.Notice("the files changed this session match what git has (already committed, or changed back)")
		return false
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) > diffCap {
		extra := len(lines) - diffCap
		lines = append(lines[:diffCap], a.theme.Faint(fmt.Sprintf("… (+%d more lines — run `git diff` for the rest)", extra)))
	}
	a.renderer.Infof("%s", strings.Join(lines, "\n"))
	return false
}

func gitSucceeds(git, dir string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// gitOutput runs git and returns stdout; a non-zero exit is not an error here
// (`git diff --no-index` exits 1 when the files differ).
func gitOutput(git, dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

func (a *app) slashCopy(_ []string) bool {
	if strings.TrimSpace(a.lastAnswer) == "" {
		a.renderer.Notice("nothing to copy yet — ask something first")
		return false
	}
	method, err := copyToClipboard(a.lastAnswer)
	if err != nil {
		a.renderer.Errorf("could not copy: %v", err)
		return false
	}
	a.renderer.Notice(fmt.Sprintf("copied the last answer (%s) via %s", humanSize(len(a.lastAnswer)), method))
	return false
}

// osc52 is the terminal escape that sets the system clipboard; most modern
// terminals (and tmux/ssh sessions) honor it, which makes it the one method
// that works remotely.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

// clipboardTools are tried in order when not on a remote session.
var clipboardTools = [][]string{
	{"pbcopy"},
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
	{"clip.exe"},
}

func copyToClipboard(text string) (string, error) {
	remote := os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
	if !remote {
		for _, c := range clipboardTools {
			path, err := exec.LookPath(c[0])
			if err != nil {
				continue
			}
			cmd := exec.Command(path, c[1:]...)
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return c[0], nil
			}
		}
	}
	if _, err := fmt.Fprint(os.Stdout, osc52(text)); err != nil {
		return "", err
	}
	return "the terminal clipboard (OSC 52)", nil
}
