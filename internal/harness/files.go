package harness

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// preferJSONC points at the .jsonc sibling when the .json file is absent but
// the sibling exists — clients that read both must not have their config
// forked into two files.
func preferJSONC(path string) string {
	if strings.HasSuffix(path, ".json") {
		jsonc := path[:len(path)-len(".json")] + ".jsonc"
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Stat(jsonc); err == nil {
				return jsonc
			}
		}
	}
	return path
}

// writeConfig writes a config file, creating its directory. The mode applies
// only on create; an existing file keeps the user's permissions. The write is
// deliberately not a temp-file rename: that would replace a symlinked dotfile
// with a regular file.
func writeConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func readOrNull(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// homeDir is the user's home, or "." when it cannot be determined.
func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return home
}
