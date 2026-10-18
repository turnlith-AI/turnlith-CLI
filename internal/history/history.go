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
	name := fmt.Sprintf("%s-%d.jsonl", time.Now().UTC().Format("20060102T150405"), os.Getpid())
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
func Continue(cwd string) (*Session, []Entry, error) {
	dir := sessionsDir()
	names, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name() > names[j].Name() })
	for _, de := range names {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, de.Name())
		session, entries, err := readSession(path, cwd)
		if err != nil {
			continue
		}
		if session == nil {
			continue // different cwd
		}
		session.file, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, nil, err
		}
		return session, entries, nil
	}
	return nil, nil, nil
}

// readSession returns the session when its meta line matches cwd.
func readSession(path, cwd string) (*Session, []Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var (
		session *Session
		entries []Entry
	)
	for scanner.Scan() {
		var r record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		switch r.Type {
		case "meta":
			if r.Cwd != cwd {
				return nil, nil, nil
			}
			session = &Session{Path: path, Agent: r.Agent}
		case "user", "assistant":
			entries = append(entries, Entry{Ts: r.Ts, Role: r.Type, Text: r.Text})
		}
	}
	if session == nil {
		return nil, nil, nil
	}
	return session, entries, nil
}
