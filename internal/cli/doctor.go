package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/agent"
	"github.com/turnlith-AI/turnlith-CLI/internal/api"
	"github.com/turnlith-AI/turnlith-CLI/internal/config"
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
	agent   string // the selected agent, if any
	cwd     string
}

// runChecks gathers the report: connection → server → key → agent first (a
// failure stops the network checks that depend on it), then the local
// environment. It never panics and never prints; callers render it.
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
				add("api key", checkFail, firstLine(friendlyErr(err)), "mint a new agent-role key in the console (API keys) and run `tlc configure`")
			case len(models.Agents) == 0:
				add("api key", checkWarn, "accepted, but the workspace has no agents", "run `tlc agents create` (needs an admin-role key)")
			default:
				add("api key", checkOK, fmt.Sprintf("accepted — %s", plural(len(models.Agents), "agent")), "")
				if in.agent != "" {
					out = append(out, agentCheck(models, in.agent))
				}
			}
		}
	}

	out = append(out, localChecks(in.cwd)...)
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// agentCheck verifies the selected agent exists and its provider has a key.
func agentCheck(models api.ModelsResponse, name string) check {
	for _, ag := range models.Agents {
		if !strings.EqualFold(ag.Codename, name) {
			continue
		}
		provider, _, _ := strings.Cut(ag.Model, ":")
		for _, p := range models.Providers {
			if p.Provider == provider && !p.Available {
				return check{Name: "agent", Status: checkWarn,
					Detail: fmt.Sprintf("%s runs %s, but the server has no %s credentials", ag.Codename, ag.Model, provider),
					Fix:    "add a provider key in the console, or switch with /model"}
			}
		}
		return check{Name: "agent", Status: checkOK, Detail: fmt.Sprintf("%s → %s", ag.Codename, ag.Model)}
	}
	return check{Name: "agent", Status: checkFail, Detail: fmt.Sprintf("no agent named %q in this workspace", name), Fix: "run `tlc agents` to list them"}
}

// localChecks look at the machine: terminal, shell, the tools tlc leans on,
// where it keeps files.
func localChecks(cwd string) []check {
	var out []check
	add := func(name string, st checkStatus, detail, fix string) {
		out = append(out, check{Name: name, Status: st, Detail: detail, Fix: fix})
	}

	term := os.Getenv("TERM")
	switch {
	case !ui.IsTerminal(os.Stdout):
		add("terminal", checkOK, "output is not a terminal (plain text mode)", "")
	case term == "" || term == "dumb":
		add("terminal", checkWarn, "TERM="+term+" — colors and the live prompt are disabled", "use a real terminal (TERM=xterm-256color)")
	default:
		note := "TERM=" + term
		if _, off := os.LookupEnv("NO_COLOR"); off {
			note += ", NO_COLOR set (colors off)"
		}
		add("terminal", checkOK, note, "")
	}

	if bash, err := exec.LookPath("bash"); err == nil {
		add("shell", checkOK, "bash: "+bash, "")
	} else if runtime.GOOS == "windows" {
		add("shell", checkWarn, "no bash on PATH — the Bash tool falls back to cmd.exe", "install Git for Windows to get bash")
	} else {
		add("shell", checkOK, "sh (bash not found)", "")
	}
	if git, err := exec.LookPath("git"); err == nil {
		add("git", checkOK, git, "")
	} else {
		add("git", checkWarn, "not found — /diff and branch info are unavailable", "install git")
	}
	if rg, err := exec.LookPath("rg"); err == nil {
		add("ripgrep", checkOK, rg, "")
	} else {
		add("ripgrep", checkWarn, "not found — Grep uses a slower built-in search and ignores .gitignore less precisely", "install ripgrep (rg)")
	}

	if st, err := os.Stat(config.ConnectionPath()); err == nil && runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		add("config file", checkWarn, fmt.Sprintf("%s is readable by other users (%v)", shortPath(config.ConnectionPath()), st.Mode().Perm()), "chmod 600 "+config.ConnectionPath())
	}
	if err := writableDir(config.StateDir()); err != nil {
		add("transcripts", checkWarn, fmt.Sprintf("cannot write to %s: %v", shortPath(config.StateDir()), err), "check permissions — /resume and -c need it")
	}

	instr := agent.LoadInstructions(cwd, agent.InstructionsMaxFromEnv())
	if len(instr) == 0 {
		add("instructions", checkOK, "none (TLC.md / AGENTS.md / CLAUDE.md would be loaded)", "")
	} else {
		names := make([]string, len(instr))
		for i, in := range instr {
			names[i] = shortPath(in.Path)
		}
		add("instructions", checkOK, strings.Join(names, ", "), "")
	}
	return out
}

func writableDir(dir string) error {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
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
	theme := ui.NewTheme()
	renderer := ui.NewRenderer(os.Stdout, theme)
	cwd, _ := os.Getwd()
	conn, err := config.LoadConnection()
	if err == nil {
		if opts.baseURL != "" {
			conn.BaseURL = config.NormalizeBaseURL(opts.baseURL)
		}
		if opts.apiKey != "" {
			conn.APIKey = strings.TrimSpace(opts.apiKey)
		}
	}
	name := opts.agent
	if name == "" {
		name = os.Getenv(config.EnvAgent)
	}
	if name == "" {
		name = config.LoadPrefs().DefaultAgent
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	checks := runChecks(ctx, doctorInput{conn: conn, connErr: err, agent: name, cwd: cwd})
	if renderChecks(renderer, theme, checks) {
		return 1
	}
	return 0
}

func (a *app) slashDoctor(_ []string) bool {
	conn := config.Connection{BaseURL: a.client.BaseURL, APIKey: a.client.APIKey}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	renderChecks(a.renderer, a.theme, runChecks(ctx, doctorInput{conn: conn, agent: a.agent.Codename, cwd: a.cwd}))
	return false
}
