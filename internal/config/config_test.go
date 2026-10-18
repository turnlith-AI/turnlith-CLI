package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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
