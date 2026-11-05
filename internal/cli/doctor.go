package cli

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
	"github.com/turnlith-AI/turnlith-CLI/internal/harness"
	"github.com/turnlith-AI/turnlith-CLI/internal/ui"
	"github.com/turnlith-AI/turnlith-CLI/internal/version"
)

type checkStatus int

const (
	checkOK checkStatus = iota
	checkWarn
	checkFail
)

// check is one line of the doctor report.
type check struct {
	Name   string
	Status checkStatus
	Detail string
	Fix    string // what to do about a warning or failure
}

// doctorInput is what the checks look at. conn/connErr come from
// config.LoadConnection (so a missing configuration is itself a finding).
type doctorInput struct {
	conn    config.Connection
	connErr error
}

// runChecks gathers the report: connection → server → key → agents first (a
// failure stops the network checks that depend on it), then the local
// environment and the coding clients. It never panics and never prints;
// callers render it.
func runChecks(ctx context.Context, in doctorInput) []check {
	var out []check
	add := func(name string, st checkStatus, detail, fix string) {
		out = append(out, check{Name: name, Status: st, Detail: detail, Fix: fix})
	}
	add("version", checkOK, fmt.Sprintf("tlc %s (%s/%s)", version.Version, runtime.GOOS, runtime.GOARCH), "")

	connected := false
	switch {
	case in.connErr != nil:
		add("connection", checkFail, firstLine(friendlyErr(in.connErr)), "run `tlc configure --base-url https://… ` (or set TLITH_BASE_URL and TLITH_API_KEY)")
	case config.ValidateBaseURL(in.conn.BaseURL) != nil:
		add("connection", checkFail, config.ValidateBaseURL(in.conn.BaseURL).Error(), "use an https:// URL, or set TLC_ALLOW_INSECURE=1 for a trusted network")
	default:
		src := "environment"
		if os.Getenv(config.EnvBaseURL) == "" || os.Getenv(config.EnvAPIKey) == "" {
			src = shortPath(config.ConnectionPath())
		}
		add("connection", checkOK, fmt.Sprintf("%s → %s (key %s)", src, in.conn.BaseURL, maskKey(in.conn.APIKey)), "")
		connected = true
	}

	if connected {
		client := api.New(in.conn.BaseURL, in.conn.APIKey)
		client.SetRetryDelays() // a diagnostic should fail fast
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		started := time.Now()
		if err := client.Health(cctx); err != nil {
			add("server", checkFail, firstLine(friendlyErr(err)), "check the URL, your network and that the server is running")
			connected = false
		} else {
			add("server", checkOK, fmt.Sprintf("healthy, %dms", time.Since(started).Milliseconds()), "")
		}

		if connected {
			models, err := client.ListModels(cctx)
			switch {
			case err != nil:
				add("api key", checkFail, firstLine(friendlyErr(err)), "mint a new key in the console (API keys) and run `tlc configure`")
			case len(models.Agents) == 0:
				add("api key", checkWarn, "accepted, but the workspace has no agents", "run `tlc agents create` (needs an admin-role key)")
			default:
				add("api key", checkOK, fmt.Sprintf("accepted — %s", plural(len(models.Agents), "agent")), "")
				out = append(out, agentChecks(models)...)
			}
		}
	}

	out = append(out, localChecks()...)
	out = append(out, clientChecks()...)
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// agentChecks reports every agent whose provider has no credentials on the
// server — its calls would fail — or a single OK line when all are usable.
func agentChecks(models api.ModelsResponse) []check {
	var out []check
	for _, ag := range models.Agents {
		provider, _, _ := strings.Cut(ag.Model, ":")
		for _, p := range models.Providers {
			if p.Provider == provider && !p.Available {
				out = append(out, check{Name: "agent " + ui.SanitizeInline(ag.Codename), Status: checkWarn,
					Detail: fmt.Sprintf("runs %s, but the server has no %s credentials", ui.SanitizeInline(ag.Model), ui.SanitizeInline(provider)),
					Fix:    "add a provider key in the console, or give the agent another model there"})
			}
		}
	}
	if len(out) == 0 {
		out = append(out, check{Name: "agents", Status: checkOK, Detail: "every agent's provider has credentials"})
	}
	return out
}

// localChecks look at this machine: where tlc keeps the key.
func localChecks() []check {
	var out []check
	path := config.ConnectionPath()
	st, err := os.Stat(path)
	if err != nil {
		return out // the key may come from the environment alone
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		out = append(out, check{Name: "config file", Status: checkWarn,
			Detail: fmt.Sprintf("%s is readable by other users (%v)", shortPath(path), st.Mode().Perm()),
			Fix:    "chmod 600 " + path})
	} else {
		out = append(out, check{Name: "config file", Status: checkOK, Detail: shortPath(path) + " is private to you"})
	}
	return out
}

// clientChecks report, for each coding client found on this machine, whether
// the turnlith agents are wired into it.
func clientChecks() []check {
	var out []check
	var detected []string
	for _, c := range harness.Registry() {
		if !c.Detect() {
			continue
		}
		detected = append(detected, c.ID)
		wired := false
		for _, f := range c.Inspect(false, harness.DefaultProviderID, harness.DefaultMCPServerID) {
			if f.Provider || f.MCP {
				wired = true
			}
		}
		if wired {
			out = append(out, check{Name: c.ID, Status: checkOK, Detail: "detected, turnlith is installed"})
		} else {
			out = append(out, check{Name: c.ID, Status: checkWarn, Detail: "detected, but turnlith is not installed",
				Fix: "run `tlc install " + c.ID + "`"})
		}
	}
	if len(detected) == 0 {
		out = append(out, check{Name: "coding clients", Status: checkOK,
			Detail: "none detected (supported: " + strings.Join(harness.Names(), ", ") + ")"})
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// renderChecks prints the report and reports whether anything failed.
func renderChecks(r *ui.Renderer, t *ui.Theme, checks []check) (failed bool) {
	width := 0
	for _, c := range checks {
		width = max(width, len(c.Name))
	}
	for _, c := range checks {
		mark := t.Green("✓")
		switch c.Status {
		case checkWarn:
			mark = t.Yellow("!")
		case checkFail:
			mark = t.Red("✗")
			failed = true
		}
		r.Infof("%s %-*s  %s", mark, width, c.Name, c.Detail)
		if c.Fix != "" && c.Status != checkOK {
			r.Infof("  %-*s  %s", width, "", t.Dim("→ "+c.Fix))
		}
	}
	return failed
}

// runDoctor is `tlc doctor`: it works without a usable configuration, since
// "why doesn't it work" is exactly when it is run.
func runDoctor(opts options) int {
	con := newConsole()
	conn, err := config.LoadConnection()
	if err == nil {
		if opts.baseURL != "" {
			conn.BaseURL = config.NormalizeBaseURL(opts.baseURL)
		}
		if opts.apiKey != "" {
			conn.APIKey = strings.TrimSpace(opts.apiKey)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	checks := runChecks(ctx, doctorInput{conn: conn, connErr: err})
	if renderChecks(con.out, con.theme, checks) {
		return 1
	}
	return 0
}
