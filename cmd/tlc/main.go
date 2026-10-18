// Command tlc is a Claude-Code-style coding agent for the terminal, backed by
// a turnlith server (session-bound inference: the server owns the context).
package main

import (
	"os"

	"github.com/turnlith-AI/turnlith-CLI/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
