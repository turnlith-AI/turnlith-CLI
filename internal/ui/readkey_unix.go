//go:build !windows

package ui

import (
	"os"

	"golang.org/x/sys/unix"
)

// readSingleKey puts the terminal in raw mode, decodes one keypress, and
// restores the terminal. ok=false means stdin is not a terminal we can take
// over (or it closed); callers fall back to a typed line then.
func readSingleKey() (string, bool) {
	fd := int(os.Stdin.Fd())
	saved, err := getTermios(fd)
	if err != nil {
		return "", false
	}
	raw := *saved
	raw.Lflag &^= unix.ECHO | unix.ICANON | unix.ISIG
	raw.Iflag &^= unix.ICRNL | unix.IGNCR
	raw.Cc[unix.VMIN] = 0
	raw.Cc[unix.VTIME] = 1
	if err := setTermios(fd, &raw); err != nil {
		return "", false
	}
	defer func() { _ = setTermios(fd, saved) }()

	key, err := decodeKey(termKeySource{fd: fd})
	if err != nil {
		return "", false
	}
	return key, true
}
