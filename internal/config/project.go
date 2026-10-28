package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
)

// ProjectPrefs are the per-project answers tlc remembers about a directory.
type ProjectPrefs struct {
	// Allow holds permission rules ("Bash(go test:*)") the user chose to
	// always allow here. They live in the user's own config, never in the
	// repository, so cloning a project cannot hand it permissions.
	Allow []string `json:"allow,omitempty"`
}

func canonicalDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		return real
	}
	return filepath.Clean(dir)
}

// FindProjectRoot returns the nearest directory at or above dir that holds a
// .git entry (directory, or file for worktrees and submodules); dir itself
// when there is none.
func FindProjectRoot(dir string) string {
	start := canonicalDir(dir)
	for cur := start; ; {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return start
		}
		cur = parent
	}
}

// ProjectAllow returns the saved always-allow rules for the project at root.
func (p Prefs) ProjectAllow(root string) []string {
	return slices.Clone(p.Projects[canonicalDir(root)].Allow)
}

// AddProjectAllow saves one always-allow rule for the project at root.
func AddProjectAllow(root, rule string) error {
	prefs := LoadPrefs()
	key := canonicalDir(root)
	if prefs.Projects == nil {
		prefs.Projects = map[string]ProjectPrefs{}
	}
	entry := prefs.Projects[key]
	if slices.Contains(entry.Allow, rule) {
		return nil
	}
	entry.Allow = append(entry.Allow, rule)
	prefs.Projects[key] = entry
	return SavePrefs(prefs)
}

// ProjectSettingsPath is the repository-shared settings file.
func ProjectSettingsPath(root string) string {
	return filepath.Join(root, ".tlc", "settings.json")
}

type projectSettings struct {
	Permissions struct {
		Deny  []string `json:"deny"`
		Allow []string `json:"allow"`
	} `json:"permissions"`
}

// LoadProjectDeny reads .tlc/settings.json from the project root. Only deny
// rules are honored — a repository may forbid things for everyone who works on
// it, but must never grant itself permissions; the number of allow rules it
// tried to ship is returned so the user can be told. A missing file is not an
// error.
func LoadProjectDeny(root string) (deny []string, ignoredAllow int, err error) {
	data, err := os.ReadFile(ProjectSettingsPath(root))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	var s projectSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, 0, err
	}
	return s.Permissions.Deny, len(s.Permissions.Allow), nil
}
