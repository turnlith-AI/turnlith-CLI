// Package cli wires everything together: argument handling, the interactive
// REPL, the print mode, and the auxiliary commands.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

// Main is the entry point; it returns the process exit code.
func Main(argv []string) int {
	opts, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlc: %v\nrun `tlc help` for usage\n", err)
		return 2
	}
	switch opts.command {
	case "help":
		if topic, ok := helpTopics[opts.helpTopic]; ok {
			fmt.Print(topic)
		} else {
			fmt.Print(helpText)
		}
		return 0
	case "version":
		fmt.Println(version.Version)
		return 0
	case "configure":
		return runConfigure(opts)
	case "doctor":
		return runDoctor(opts)
	case "completion":
		return runCompletion(opts.prompt)
	case "install":
		return runInstall(opts)
	case "uninstall":
		return runUninstall(opts)
	}

	app, err := newApp(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlc: %s\n", friendlyErr(err))
		var usage *usageError
		if errors.As(err, &usage) {
			return 2
		}
		return 1
	}
	defer app.close()

	switch opts.command {
	case "agents":
		return app.cmdAgents()
	case "agents-create":
		return app.cmdAgentsCreate()
	}
	if opts.print {
		return app.runPrint()
	}
	return app.runInteractive()
}
