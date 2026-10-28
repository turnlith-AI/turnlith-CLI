// Package config resolves the connection tlc uses to reach a turnlith
// server. The connection (baseUrl + apiKey) lives at
// ~/.config/tlith/config.json — the path the retired tlith setup helper
// used, kept so existing setups keep working; tlc-specific preferences live
// next to it in tlc.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvBaseURL = "TLITH_BASE_URL"
	EnvAPIKey  = "TLITH_API_KEY"
	EnvAgent   = "TLC_AGENT"
	// EnvAllowInsecure=1 permits plain http to a non-loopback server, for
	// networks the operator trusts (a VPN, a private LAN).
	EnvAllowInsecure = "TLC_ALLOW_INSECURE"
)

// Connection is the server + credential pair.
type Connection struct {
	BaseURL string
	APIKey  string
}

// Prefs are tlc-local preferences.
type Prefs struct {
	DefaultAgent string `json:"defaultAgent,omitempty"`
}

// ErrNotConfigured reports a missing connection with setup guidance.
type ErrNotConfigured struct{ Path string }

func (e *ErrNotConfigured) Error() string {
	return fmt.Sprintf(
		"not connected to a turnlith server yet — run `tlc configure --base-url <url> --api-key sk-…`\n"+
			"expected connection at %s", e.Path)
}

// configDir resolves $XDG_CONFIG_HOME when it is an absolute path, otherwise
// ~/.config — on every platform, macOS included — matching what the retired
// tlith helper did, so pre-tlc setups read the same connection file.
func configDir() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "tlith")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "tlith")
}

// ConnectionPath is the shared tlith connection file.
func ConnectionPath() string { return filepath.Join(configDir(), "config.json") }

// PrefsPath is tlc's own preferences file.
func PrefsPath() string { return filepath.Join(configDir(), "tlc.json") }

// StateDir is where transcripts live.
func StateDir() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local", "share")
	}
	dir := filepath.Join(base, "tlith", "tlc")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// NormalizeBaseURL trims whitespace and trailing slashes and adds a scheme
// when one is missing (https, except for localhost).
func NormalizeBaseURL(raw string) string {
	url := strings.TrimRight(strings.TrimSpace(raw), "/")
	if url == "" {
		return ""
	}
	if !strings.Contains(url, "://") {
		host := url
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		scheme := "https"
		if isLoopbackHost(hostOnly(host)) {
			scheme = "http"
		}
		url = scheme + "://" + url
	}
	return url
}

// hostOnly strips an optional port (and IPv6 brackets) from a host[:port].
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// isLoopbackHost reports whether host names this machine: localhost (or any
// *.localhost) or a loopback address. "localhost.example.com" is not.
func isLoopbackHost(host string) bool {
	host = strings.ToLower(host)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateBaseURL refuses server URLs that would put the api key at risk:
// anything but http(s), and plain http to a host other than this machine
// unless TLC_ALLOW_INSECURE=1. Errors name the host only, never userinfo or
// query strings.
func ValidateBaseURL(raw string) error {
	u, err := neturl.Parse(raw)
	if err != nil {
		return errors.New("invalid server URL: expected something like https://turnlith.com")
	}
	if u.Host == "" {
		return errors.New("invalid server URL: missing host (expected something like https://turnlith.com)")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if isLoopbackHost(host) || os.Getenv(EnvAllowInsecure) == "1" {
			return nil
		}
		return fmt.Errorf("refusing to send the api key over plain http to %s — use https://, or set %s=1 if this network is trusted", host, EnvAllowInsecure)
	default:
		return fmt.Errorf("unsupported server URL scheme %q — use https://", u.Scheme)
	}
}

// LoadConnection resolves the connection: environment first, then the shared
// config file.
func LoadConnection() (Connection, error) {
	conn := Connection{
		BaseURL: NormalizeBaseURL(os.Getenv(EnvBaseURL)),
		APIKey:  strings.TrimSpace(os.Getenv(EnvAPIKey)),
	}
	if conn.BaseURL != "" && conn.APIKey != "" {
		return conn, nil
	}
	file, err := readConnectionFile()
	if err != nil {
		if os.IsNotExist(err) {
			return Connection{}, &ErrNotConfigured{Path: ConnectionPath()}
		}
		return Connection{}, err
	}
	if conn.BaseURL == "" {
		conn.BaseURL = NormalizeBaseURL(file.BaseURL)
	}
	if conn.APIKey == "" {
		conn.APIKey = strings.TrimSpace(file.APIKey)
	}
	if conn.BaseURL == "" || conn.APIKey == "" {
		return Connection{}, &ErrNotConfigured{Path: ConnectionPath()}
	}
	return conn, nil
}

func readConnectionFile() (Connection, error) {
	data, err := os.ReadFile(ConnectionPath())
	if err != nil {
		return Connection{}, err
	}
	var conn Connection
	if err := json.Unmarshal(data, &conn); err != nil {
		return Connection{}, fmt.Errorf("invalid config at %s: %w", ConnectionPath(), err)
	}
	return conn, nil
}

// SaveConnection writes the shared connection file (mode 600), preserving
// keys other tools may have stored there.
func SaveConnection(conn Connection) (string, error) {
	path := ConnectionPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	fields := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &fields)
	}
	fields["baseUrl"] = conn.BaseURL
	fields["apiKey"] = conn.APIKey
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// LoadPrefs never fails; unreadable prefs are treated as empty.
func LoadPrefs() Prefs {
	var prefs Prefs
	if data, err := os.ReadFile(PrefsPath()); err == nil {
		_ = json.Unmarshal(data, &prefs)
	}
	return prefs
}

// SavePrefs persists tlc preferences.
func SavePrefs(prefs Prefs) error {
	path := PrefsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
