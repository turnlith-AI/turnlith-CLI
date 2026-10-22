//go:build windows

package ui

// readSingleKey is not implemented on windows; callers fall back to a typed
// line for one-key questions.
func readSingleKey() (string, bool) { return "", false }
