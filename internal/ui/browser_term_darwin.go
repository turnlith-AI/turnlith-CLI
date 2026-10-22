//go:build darwin

package ui

import "golang.org/x/sys/unix"

func getTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TIOCGETA)
}

func setTermios(fd int, t *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, t)
}
