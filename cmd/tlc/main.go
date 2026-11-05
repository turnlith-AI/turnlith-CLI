// Command tlc connects a turnlith workspace to the coding tools you already
// use: it saves and verifies the connection, manages agents, wires them into
// opencode, Pi, Continue, Cursor and Cline, and diagnoses the setup.
package main

import (
	"os"

	"github.com/turnlith-AI/turnlith-CLI/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
