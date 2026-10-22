//go:build windows

package ui

import "io"

// RunFileBrowser falls back to a static listing on windows: the interactive
// raw-mode loop is not ported, but /files still shows the session section
// and the workspace root.
func RunFileBrowser(root string, touched []FileBadge, t *Theme, out io.Writer) (string, error) {
	RenderFileList(out, t, root, touched, "interactive browsing needs a unix terminal — @-mention files by path instead")
	return "", nil
}
