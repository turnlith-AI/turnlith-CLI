package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolate points the config location at a temp dir on every platform. Setting
// HOME as well keeps macOS hermetic (os.UserConfigDir ignores XDG there; tlc
// does not, but tests should not depend on that distinction).
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return home
}

func TestConfigDirMatchesTlith(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses HOME semantics")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := ConnectionPath(), filepath.Join(home, ".config", "tlith", "config.json"); got != want {
		t.Errorf("fallback path = %q, want %q", got, want)
	}

	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := ConnectionPath(), filepath.Join(xdg, "tlith", "config.json"); got != want {
		t.Errorf("xdg path = %q, want %q", got, want)
	}

	// a relative XDG_CONFIG_HOME is ignored, like the tlith CLI does
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	if got, want := ConnectionPath(), filepath.Join(home, ".config", "tlith", "config.json"); got != want {
		t.Errorf("relative-xdg path = %q, want %q", got, want)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"turnlith.com":          "https://turnlith.com",
		"http://turnlith.com/":  "http://turnlith.com",
		"https://x.dev/v1/":     "https://x.dev/v1",
		"localhost:8080":        "http://localhost:8080",
		"127.0.0.1:8080":        "http://127.0.0.1:8080",
		"  https://a.example  ": "https://a.example",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConnectionRoundTrip(t *testing.T) {
	isolate(t)
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "")

	if _, err := LoadConnection(); err == nil {
		t.Fatal("expected ErrNotConfigured")
	} else if _, ok := err.(*ErrNotConfigured); !ok {
		t.Fatalf("err = %#v", err)
	}

	path, err := SaveConnection(Connection{BaseURL: "https://turnlith.com", APIKey: "sk-abc"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}

	conn, err := LoadConnection()
	if err != nil {
		t.Fatal(err)
	}
	if conn.BaseURL != "https://turnlith.com" || conn.APIKey != "sk-abc" {
		t.Fatalf("conn = %+v", conn)
	}

	// env overrides win
	t.Setenv(EnvBaseURL, "http://localhost:9999")
	conn, err = LoadConnection()
	if err != nil {
		t.Fatal(err)
	}
	if conn.BaseURL != "http://localhost:9999" || conn.APIKey != "sk-abc" {
		t.Fatalf("conn = %+v", conn)
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	isolate(t)
	prefs := LoadPrefs()
	if prefs.DefaultAgent != "" {
		t.Fatalf("prefs = %+v", prefs)
	}
	prefs.DefaultAgent = "helper"
	if err := SavePrefs(prefs); err != nil {
		t.Fatal(err)
	}
	if got := LoadPrefs().DefaultAgent; got != "helper" {
		t.Fatalf("agent = %q", got)
	}
	if filepath.Base(PrefsPath()) != "tlc.json" {
		t.Fatalf("prefs path = %s", PrefsPath())
	}
}

func TestValidateBaseURL(t *testing.T) {
	t.Setenv(EnvAllowInsecure, "")
	ok := []string{
		"https://turnlith.com",
		"https://example.com:8443/base",
		"http://localhost:8080",
		"http://127.0.0.1:3000",
		"http://127.1.2.3",
		"http://[::1]:8080",
		"http://app.localhost:8080",
	}
	for _, u := range ok {
		if err := ValidateBaseURL(u); err != nil {
			t.Errorf("%s should be accepted: %v", u, err)
		}
	}
	bad := map[string]string{
		"http://turnlith.com":      "plain http",
		"http://10.0.0.5:8080":     "plain http",
		"http://192.168.1.2":       "plain http",
		"ftp://example.com":        "https",
		"https://":                 "host",
		"://nonsense":              "",
		"http://localhost.evil.io": "plain http",
	}
	for u, want := range bad {
		err := ValidateBaseURL(u)
		if err == nil {
			t.Errorf("%s should be refused", u)
			continue
		}
		if want != "" && !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %q should mention %q", u, err, want)
		}
	}
}

func TestValidateBaseURLInsecureOverride(t *testing.T) {
	t.Setenv(EnvAllowInsecure, "1")
	if err := ValidateBaseURL("http://10.0.0.5:8080"); err != nil {
		t.Fatalf("override should allow plain http: %v", err)
	}
	// the override only relaxes http, never garbage
	if err := ValidateBaseURL("ftp://example.com"); err == nil {
		t.Fatal("ftp must stay refused")
	}
}

func TestValidateBaseURLErrorNeverEchoesCredentials(t *testing.T) {
	t.Setenv(EnvAllowInsecure, "")
	err := ValidateBaseURL("http://user:hunter2@turnlith.com/x?key=sk-abc")
	if err == nil {
		t.Fatal("expected refusal")
	}
	if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "sk-abc") {
		t.Fatalf("error leaks secrets: %v", err)
	}
}
