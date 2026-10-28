// Package history stores local conversation transcripts (JSONL) so
// `tlc --continue` can show where the conversation left off. The model's
// memory lives on the turnlith server; these files are display-only.
package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/turnlith-AI/turnlith-CLI/internal/config"
)

// Entry is one displayable message.
type Entry struct {
	Ts   time.Time `json:"ts"`
	Role string    `json:"role"` // "user" or "assistant"
	Text string    `json:"text"`
}

type record struct {
	Type  string    `json:"type"` // "meta", "user", "assistant"
	Ts    time.Time `json:"ts,omitempty"`
	Text  string    `json:"text,omitempty"`
	Cwd   string    `json:"cwd,omitempty"`
	Agent string    `json:"agent,omitempty"`
}

// Session is an append-only transcript file.
type Session struct {
	Path  string
	Agent string
	file  *os.File
}

func sessionsDir() string { return filepath.Join(config.StateDir(), "sessions") }

// Start opens a fresh transcript for this directory and agent.
func Start(cwd, agent string) (*Session, error) {
	dir := sessionsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// microseconds keep sessions started in the same instant apart, and the
	// fixed-width stamp sorts newest-last as text
	name := fmt.Sprintf("%s-%d.jsonl", time.Now().UTC().Format("20060102T150405.000000"), os.Getpid())
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	s := &Session{Path: path, Agent: agent, file: f}
	if err := s.write(record{Type: "meta", Ts: time.Now().UTC(), Cwd: cwd, Agent: agent}); err != nil {
		_ = f.Close()
		return nil, err
	}
	return s, nil
}

// Append records a message.
func (s *Session) Append(role, text string) {
	if s == nil || s.file == nil || text == "" {
		return
	}
	_ = s.write(record{Type: role, Ts: time.Now().UTC(), Text: text})
}

func (s *Session) write(r record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.file.Write(append(data, '\n'))
	return err
}

func (s *Session) Close() error {
	if s == nil || s.file == nil {
		return nil
	}
	return s.file.Close()
}

// Continue reopens the newest transcript for cwd (if any) and returns its
// entries for replay.
func Continue(cwd string) (*Session, []Entry, error) { return ContinueFor(cwd, "") }

// ContinueFor is Continue preferring the newest transcript made with the given
// agent (server memory is per agent), falling back to the newest overall.
func ContinueFor(cwd, agent string) (*Session, []Entry, error) {
	infos, err := List(cwd, 0)
	if err != nil || len(infos) == 0 {
		return nil, nil, err
	}
	pick := infos[0]
	if agent != "" {
		for _, in := range infos {
			if in.Agent == agent {
				pick = in
				break
			}
		}
	}
	return Open(pick.Path)
}

// Info summarizes one stored transcript for a picker.
type Info struct {
	Path     string
	Agent    string
	Started  time.Time
	Messages int
	Preview  string // the first thing the user asked, shortened
}

// listScanLimit bounds how many of the newest files List will look at.
const listScanLimit = 300

// List returns the transcripts for cwd, newest first; limit <= 0 means all.
func List(cwd string, limit int) ([]Info, error) {
	dir := sessionsDir()
	names, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() > names[j].Name() })
	var out []Info
	for scanned, de := range names {
		if scanned >= listScanLimit || (limit > 0 && len(out) >= limit) {
			break
		}
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, de.Name())
		info, ok := scanInfo(path, cwd)
		if ok {
			out = append(out, info)
		}
	}
	return out, nil
}

// scanInfo reads one transcript's summary if it belongs to cwd.
func scanInfo(path, cwd string) (Info, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	info := Info{Path: path}
	matched := false
	for scanner.Scan() {
		var r record
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			continue
		}
		switch r.Type {
		case "meta":
			if r.Cwd != cwd {
				return Info{}, false
			}
			matched = true
			info.Agent, info.Started = r.Agent, r.Ts
		case "user", "assistant":
			info.Messages++
			if info.Preview == "" && r.Type == "user" {
				info.Preview = preview(r.Text)
			}
		}
	}
	return info, matched
}

// preview flattens a message to one short line.
func preview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 72 {
		return string(r[:72]) + "…"
	}
	return text
}

// Open reopens one stored transcript for appending and returns its entries.
func Open(path string) (*Session, []Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	var (
		session *Session
		entries []Entry
	)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var r record
		if json.Unmarshal(scanner.Bytes(), &r) != nil {
			continue
		}
		switch r.Type {
		case "meta":
			session = &Session{Path: path, Agent: r.Agent}
		case "user", "assistant":
			entries = append(entries, Entry{Ts: r.Ts, Role: r.Type, Text: r.Text})
		}
	}
	_ = f.Close()
	if session == nil {
		return nil, nil, fmt.Errorf("%s is not a tlc transcript", path)
	}
	session.file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return session, entries, nil
}
