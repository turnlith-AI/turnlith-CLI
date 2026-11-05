package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// maxKeyStdin bounds how much of stdin `--api-key-stdin` will read.
const maxKeyStdin = 64 * 1024

func runConfigure(opts options) int {
	con := newConsole()
	fail := func(code int, format string, args ...any) int {
		con.err.Errorf(format, args...)
		return code
	}

	if opts.apiKeyStdin {
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, maxKeyStdin)).ReadString('\n')
		if err != nil && err != io.EOF {
			return fail(1, "could not read the api key from stdin: %v", err)
		}
		opts.apiKey = strings.TrimSpace(line)
		if opts.apiKey == "" {
			return fail(2, "--api-key-stdin: stdin was empty — pipe the key in, e.g. echo \"$KEY\" | tlc configure --api-key-stdin")
		}
	} else if opts.apiKey != "" {
		warnAPIKeyFlag()
	}

	baseURL := config.NormalizeBaseURL(opts.baseURL)
	apiKey := strings.TrimSpace(opts.apiKey)

	// Prompt only for what is missing, and only when a person can answer.
	if baseURL == "" || apiKey == "" {
		if !ui.IsTerminal(os.Stdin) || !ui.IsTerminal(os.Stdout) {
			return fail(2, "nothing to prompt on without a terminal — pass --base-url and either --api-key-stdin or set TLITH_API_KEY")
		}
		prompter, err := ui.NewPrompter(con.out, con.theme)
		if err != nil {
			return fail(1, "%v", err)
		}
		defer prompter.Close()
		if baseURL == "" {
			baseURL, err = promptBaseURL(con, prompter)
			if err != nil {
				return promptFailed(con, err)
			}
		}
		if apiKey == "" {
			apiKey, err = prompter.ReadSecret("api key (sk-…): ")
			if err != nil {
				return promptFailed(con, err)
			}
		}
	}
	if err := config.ValidateBaseURL(baseURL); err != nil {
		return fail(2, "%s", err)
	}
	if baseURL == "" || apiKey == "" {
		return fail(2, "both a server URL and an api key are required")
	}

	client := newClient(config.Connection{BaseURL: baseURL, APIKey: apiKey}, con)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		return fail(1, "health check failed: %s", friendlyErr(err))
	}
	agents, err := client.ListAgents(ctx)
	if err != nil {
		return fail(1, "key check failed: %s", friendlyErr(err))
	}
	path, err := config.SaveConnection(config.Connection{BaseURL: baseURL, APIKey: apiKey})
	if err != nil {
		return fail(1, "could not save the connection: %v", err)
	}
	return reportConfigured(con, baseURL, path, agents)
}

// promptBaseURL asks for the server URL, offering the saved one as default.
func promptBaseURL(con console, p *ui.Prompter) (string, error) {
	def := "http://localhost:8080"
	if existing, err := config.LoadConnection(); err == nil && existing.BaseURL != "" {
		def = existing.BaseURL
	}
	con.out.Infof("turnlith server URL [%s]:", def)
	answer, err := p.Readline()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(answer) == "" {
		return def, nil
	}
	return config.NormalizeBaseURL(answer), nil
}

// promptFailed explains a prompt that ended without an answer.
func promptFailed(con console, err error) int {
	switch {
	case errors.Is(err, ui.ErrInterrupt):
		con.err.Notice("cancelled — nothing saved")
		return 130
	case errors.Is(err, io.EOF):
		con.err.Errorf("no input — nothing saved")
		return 1
	}
	con.err.Errorf("%v", err)
	return 1
}

func reportConfigured(con console, baseURL, path string, agents []api.Agent) int {
	con.out.Infof("%s connected to %s — %d agent(s) available", con.theme.Green("✓"), baseURL, len(agents))
	con.out.Infof("%s", con.theme.Dim("saved "+path))
	if len(agents) > 0 {
		con.out.Infof("%s", con.theme.Dim("next: `tlc install` wires them into your coding clients, `tlc agents` lists them"))
	} else {
		con.out.Infof("%s", con.theme.Yellow("no agents exist in this workspace yet — run `tlc agents create` to make one"))
	}
	return 0
}
