//go:build !windows

package ui

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// RunFileBrowser shows the interactive /files browser on stdin/stdout and
// returns the picked file relative to root ("" when closed without a pick).
// The terminal is taken over in raw mode for the duration and restored after.
func RunFileBrowser(root string, touched []FileBadge, t *Theme, out io.Writer) (string, error) {
	fd := int(os.Stdin.Fd())
	saved, err := getTermios(fd)
	if err != nil {
		RenderFileList(out, t, root, touched, "cannot take over the terminal — showing the workspace root")
		return "", nil
	}
	raw := *saved
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG
	raw.Iflag &^= unix.ICRNL | unix.IGNCR
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1 // 100 ms poll: escapes ESC prefixes from lone Escape
	if err := setTermios(fd, &raw); err != nil {
		RenderFileList(out, t, root, touched, "cannot take over the terminal — showing the workspace root")
		return "", nil
	}
	defer func() { _ = setTermios(fd, saved) }()

	st := &browserState{Root: root, Touched: touched}
	st.loadRows()

	_, termH := terminalSize(fd)
	if !t.Colors || termH < 8 {
		// no reliable redraw surface: fall back to the static listing
		RenderFileList(out, t, root, touched, "")
		return "", nil
	}

	src := termKeySource{fd: fd}
	lines := renderBrowser(out, t, st, termH)
	for {
		key, err := decodeKey(src)
		if err != nil {
			return "", nil // EOF on stdin: give up quietly
		}
		action, r := actionForKey(key, st.Filtering)
		if action == actNone {
			continue
		}
		picked, closed := st.apply(action, r)
		if closed || picked != "" {
			eraseLines(out, lines)
			return picked, nil
		}
		eraseLines(out, lines)
		lines = renderBrowser(out, t, st, termH)
	}
}

// eraseLines moves the cursor up n lines and clears everything below, so the
// next render lands on a clean slate.
func eraseLines(w io.Writer, n int) {
	if n > 0 {
		fmt.Fprintf(w, "\x1b[%dA", n)
	}
	fmt.Fprint(w, "\x1b[J")
}

// termKeySource reads from a raw-mode fd: first blocks until a byte arrives,
// next reports whether a byte came within the VTIME poll.
type termKeySource struct {
	fd int
}

func (s termKeySource) first() (byte, bool) {
	for {
		var b [1]byte
		n, err := unix.Read(s.fd, b[:])
		if n == 1 {
			return b[0], true
		}
		if err != nil && err != unix.EINTR && err != unix.EAGAIN {
			return 0, false
		}
	}
}

func (s termKeySource) next() (byte, bool) {
	var b [1]byte
	n, err := unix.Read(s.fd, b[:])
	if n == 1 {
		return b[0], true
	}
	_ = err
	return 0, false
}

// terminalSize returns the terminal dimensions; (0, 0) when unknown.
func terminalSize(fd int) (int, int) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0
	}
	return int(ws.Col), int(ws.Row)
}
