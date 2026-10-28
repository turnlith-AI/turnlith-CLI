package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// continueLine implements backslash continuation at the prompt: a line ending
// in an odd number of backslashes asks for another, and the lines are joined
// with newlines. An escaped backslash (\\) at the end is literal text.
func continueLine(acc, next string, appending bool) (joined string, more bool) {
	trimmed := strings.TrimRight(next, " \t")
	n := len(trimmed) - len(strings.TrimRight(trimmed, `\`))
	if n%2 == 1 {
		trimmed = strings.TrimRight(strings.TrimSuffix(trimmed, `\`), " \t")
		more = true
	} else {
		trimmed = next
	}
	if appending {
		return acc + "\n" + trimmed, more
	}
	return trimmed, more
}

// readInput reads one prompt, following backslash continuations onto further
// lines. Ctrl-C anywhere abandons the whole input; Ctrl-D submits what was
// typed so far.
func (a *app) readInput() (string, error) {
	line, err := a.repl.Readline()
	if err != nil {
		return line, err
	}
	line, more := continueLine("", line, false)
	for more {
		var next string
		next, err = a.repl.ReadlineWithPrompt(a.theme.Dim("… "))
		if err != nil {
			if err == io.EOF {
				return line, nil
			}
			return "", err
		}
		line, more = continueLine(line, next, true)
	}
	return line, nil
}

// editInEditor opens $VISUAL (else $EDITOR, else vi / notepad) on a temporary
// file seeded with initial and returns what was saved, trimmed. An empty
// result means the user cancelled.
func editInEditor(initial string) (string, error) {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
		if runtime.GOOS == "windows" {
			editor = "notepad"
		}
	}
	parts := strings.Fields(editor)

	f, err := os.CreateTemp("", "tlc-prompt-*.md")
	if err != nil {
		return "", err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString(initial); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	cmd := exec.Command(parts[0], append(parts[1:], name)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not run your editor (%s): %w — set $VISUAL or $EDITOR", editor, err)
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), nil
}

func (a *app) slashEdit(_ []string) bool {
	text, err := editInEditor("")
	if err != nil {
		a.renderer.Errorf("%v", err)
		return false
	}
	if text == "" {
		a.renderer.Notice("empty prompt — nothing sent")
		return false
	}
	a.renderer.Notice(fmt.Sprintf("sending %s from your editor", plural(strings.Count(text, "\n")+1, "line")))
	a.runWithCancel(text)
	return false
}
