package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
)

// warnAPIKeyFlag explains why --api-key is the least safe way to pass a key.
func warnAPIKeyFlag() {
	fmt.Fprintln(os.Stderr, "tlc: warning: --api-key is visible to other users in the process list; prefer TLITH_API_KEY, `tlc configure`, or --api-key-stdin")
}

func runConfigure(opts options) int {
	if opts.apiKeyStdin {
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, 64*1024)).ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "tlc: could not read the api key from stdin: %v\n", err)
			return 1
		}
		opts.apiKey = strings.TrimSpace(line)
	} else if opts.apiKey != "" {
		warnAPIKeyFlag()
	}
	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)
	repl, err := ui.NewREPL(renderer, theme)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tlc: %v\n", err)
		return 1
	}
	defer repl.Close()

	baseURL := config.NormalizeBaseURL(opts.baseURL)
	if baseURL == "" {
		if existing, err := config.LoadConnection(); err == nil && existing.BaseURL != "" {
			baseURL = existing.BaseURL
		} else {
			// the hosted service; a self-hosted or local dev server
			// (http://localhost:8080) is one answer away at the prompt
			baseURL = config.DefaultBaseURL
		}
		renderer.Infof("turnlith server URL [%s]:", baseURL)
		answer, err := repl.Readline()
		if err != nil {
			return 1
		}
		if strings.TrimSpace(answer) != "" {
			baseURL = config.NormalizeBaseURL(answer)
		}
	}
	if err := config.ValidateBaseURL(baseURL); err != nil {
		renderer.Errorf("%s", err)
		return 2
	}
	apiKey := strings.TrimSpace(opts.apiKey)
	if apiKey == "" {
		apiKey, err = repl.ReadSecret("api key (sk-…): ")
		if err != nil {
			return 1
		}
	}
	if baseURL == "" || apiKey == "" {
		renderer.Errorf("both a server URL and an api key are required")
		return 2
	}

	client := api.New(baseURL, apiKey)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Health(ctx); err != nil {
		renderer.Errorf("health check failed: %s", friendlyErr(err))
		return 1
	}
	agents, err := client.ListAgents(ctx)
	if err != nil {
		renderer.Errorf("key check failed: %s", friendlyErr(err))
		return 1
	}
	path, err := config.SaveConnection(config.Connection{BaseURL: baseURL, APIKey: apiKey})
	if err != nil {
		renderer.Errorf("could not save the connection: %v", err)
		return 1
	}
	renderer.Infof("%s connected to %s — %d agent(s) available", theme.Green("✓"), baseURL, len(agents))
	renderer.Infof("%s", theme.Dim("saved "+path))
	if len(agents) > 0 {
		renderer.Infof("%s", theme.Dim("run `tlc` to start, or `tlc agents` to list agents"))
	} else {
		renderer.Infof("%s", theme.Yellow("no agents exist in this workspace yet — run `tlc agents create` to make one"))
	}
	return 0
}
