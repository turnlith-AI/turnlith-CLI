package harness

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

// writeConfig writes a config file, creating its directory. The write is
// deliberately not a temp-file rename: that would replace a symlinked dotfile
// with a regular file. Every client config tlc writes carries the api key,
// so an existing file that others could read loses its group/other bits
// (the owner's own bits are kept).
func writeConfig(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return restrictToOwner(path)
}

// restrictToOwner clears group/other permission bits. Windows has no such
// bits (os.Chmod only toggles read-only there), so it is a no-op.
func restrictToOwner(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return os.Chmod(path, perm&^0o077)
	}
	return nil
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
