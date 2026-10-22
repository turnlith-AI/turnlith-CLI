package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// The /files browser: a raw-mode, arrow-key listing of the workspace (with
// the session's touched files pinned on top) that ends either closed or with
// a picked file, which the REPL pre-fills as an @mention.
//
// This file holds the pure model — listing, filtering, key decoding, key to
// action mapping, and rendering — so it is unit-testable without a terminal.
// The interactive loop lives in browser_unix.go; windows and non-terminal
// contexts get a static listing via RenderFileList.

// FileBadge is one session-touched file for the browser's session section
// (paths relative to the workspace root).
type FileBadge struct {
	Path                  string
	Read, Written, Edited bool
}

// browserRow is one selectable line.
type browserRow struct {
	Label                 string // what is displayed (dirs carry a trailing "/")
	Path                  string // relative to the workspace root, no trailing slash
	Dir                   bool
	Parent                bool // the "../" row
	Size                  int64
	Session               bool
	Read, Written, Edited bool
}

// baction is what a keypress does to the browser.
type baction int

const (
	actNone baction = iota
	actUp
	actDown
	actOpen
	actParent
	actClose
	actFilterStart
	actFilterStop
	actFilterClear
	actRefresh
	actBackspace
	actType // append rune to the filter
)

// browserState is the whole browser view; Rows are the filtered entries the
// cursor moves over.
type browserState struct {
	Root      string
	Cwd       string // "" at the workspace root, slash-separated below it
	Touched   []FileBadge
	Filter    string
	Filtering bool // typing into Filter; Enter stops, Esc clears
	Cursor    int
	Rows      []browserRow
}

// loadRows rebuilds the listing: the session section (root view only), then
// ../, then directories before files, narrowed by the filter.
func (st *browserState) loadRows() {
	var rows []browserRow
	if st.Cwd == "" {
		for _, f := range st.Touched {
			rows = append(rows, browserRow{
				Label: f.Path, Path: f.Path,
				Session: true, Read: f.Read, Written: f.Written, Edited: f.Edited,
			})
		}
	}
	if st.Cwd != "" {
		rows = append(rows, browserRow{Label: "../", Dir: true, Parent: true})
	}

	dir := st.Root
	if st.Cwd != "" {
		dir = filepath.Join(st.Root, filepath.FromSlash(st.Cwd))
	}
	if entries, err := os.ReadDir(dir); err == nil {
		var dirs, files []browserRow
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() && tools.IsSkipDir(name) {
				continue
			}
			rel := name
			if st.Cwd != "" {
				rel = st.Cwd + "/" + name
			}
			row := browserRow{Path: rel, Dir: e.IsDir()}
			if info, err := e.Info(); err == nil {
				row.Size = info.Size()
				row.Dir = info.IsDir()
			}
			if row.Dir {
				row.Label = name + "/"
				dirs = append(dirs, row)
			} else {
				row.Label = name
				files = append(files, row)
			}
		}
		alpha := func(rs []browserRow) {
			sort.Slice(rs, func(i, j int) bool { return strings.ToLower(rs[i].Label) < strings.ToLower(rs[j].Label) })
		}
		alpha(dirs)
		alpha(files)
		rows = append(rows, dirs...)
		rows = append(rows, files...)
	}

	if st.Filter != "" {
		needle := strings.ToLower(st.Filter)
		filtered := rows[:0]
		for _, r := range rows {
			if r.Parent {
				continue // navigation rows never match a filter
			}
			if strings.Contains(strings.ToLower(r.Label), needle) || strings.Contains(strings.ToLower(r.Path), needle) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	st.Rows = rows
	st.clampCursor()
}

func (st *browserState) clampCursor() {
	if st.Cursor >= len(st.Rows) {
		st.Cursor = len(st.Rows) - 1
	}
	if st.Cursor < 0 {
		st.Cursor = 0
	}
}

// apply performs an action. Opening a file returns it as picked; actClose
// reports closed=true.
func (st *browserState) apply(a baction, r rune) (picked string, closed bool) {
	switch a {
	case actUp:
		if st.Cursor > 0 {
			st.Cursor--
		}
	case actDown:
		if st.Cursor < len(st.Rows)-1 {
			st.Cursor++
		}
	case actOpen:
		if len(st.Rows) == 0 {
			return "", false
		}
		row := st.Rows[st.Cursor]
		switch {
		case row.Parent:
			st.Cwd = parentDir(st.Cwd)
			st.Cursor = 0
			st.loadRows()
		case row.Dir:
			st.Cwd = row.Path
			st.Cursor = 0
			st.loadRows()
		default:
			return row.Path, true
		}
	case actParent:
		if st.Cwd != "" {
			st.Cwd = parentDir(st.Cwd)
			st.Cursor = 0
			st.loadRows()
		}
	case actFilterStart:
		st.Filtering = true
	case actFilterStop:
		st.Filtering = false
	case actFilterClear:
		st.Filter = ""
		st.Filtering = false
		st.Cursor = 0
		st.loadRows()
	case actRefresh:
		st.loadRows()
	case actBackspace:
		if st.Filter != "" {
			st.Filter = st.Filter[:len(st.Filter)-1]
			st.Cursor = 0
			st.loadRows()
		}
	case actType:
		st.Filter += string(r)
		st.Cursor = 0
		st.loadRows()
	}
	return "", false
}

func parentDir(cwd string) string {
	if i := strings.LastIndexByte(cwd, '/'); i > 0 {
		return cwd[:i]
	}
	return ""
}

// ------------------------------------------------------------ key decoding

// keySource yields raw bytes: first blocks until a keypress, next polls for
// ~100 ms so an ESC prefix can be told apart from a lone Escape press.
type keySource interface {
	first() (byte, bool)
	next() (byte, bool)
}

// decodeKey turns bytes into a logical key: "up", "down", "left", "right",
// "enter", "esc", "backspace", "ctrlC", or a one-rune string.
func decodeKey(src keySource) (string, error) {
	b, ok := src.first()
	if !ok {
		return "", io.EOF
	}
	switch b {
	case 0x1b:
		b2, ok := src.next()
		if !ok || (b2 != '[' && b2 != 'O') {
			return "esc", nil
		}
		b3, ok := src.next()
		if !ok {
			return "esc", nil
		}
		switch b3 {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		case 'C':
			return "right", nil
		case 'D':
			return "left", nil
		default:
			return "esc", nil
		}
	case '\r', '\n':
		return "enter", nil
	case 0x7f, 0x08:
		return "backspace", nil
	case 0x03:
		return "ctrlC", nil
	}
	if b < 0x80 {
		return string(rune(b)), nil
	}
	// multi-byte rune: pull in its continuation bytes
	buf := []byte{b}
	for len(buf) < 4 {
		nb, ok := src.next()
		if !ok {
			break
		}
		buf = append(buf, nb)
		if utf8.FullRune(buf) {
			break
		}
	}
	if r, _ := utf8.DecodeRune(buf); r != utf8.RuneError {
		return string(r), nil
	}
	return "", nil
}

// actionForKey maps a decoded key to a browser action; while filtering,
// typing edits the filter instead of navigating.
func actionForKey(key string, filtering bool) (baction, rune) {
	if filtering {
		switch key {
		case "enter":
			return actFilterStop, 0
		case "esc":
			return actFilterClear, 0
		case "backspace":
			return actBackspace, 0
		case "up":
			return actUp, 0
		case "down":
			return actDown, 0
		}
		if r := []rune(key); len(r) == 1 {
			return actType, r[0]
		}
		return actNone, 0
	}
	switch key {
	case "up", "k":
		return actUp, 0
	case "down", "j":
		return actDown, 0
	case "right", "l", "enter":
		return actOpen, 0
	case "left", "h", "u", "backspace":
		return actParent, 0
	case "q", "esc", "ctrlC":
		return actClose, 0
	case "/":
		return actFilterStart, 0
	case "r":
		return actRefresh, 0
	}
	return actNone, 0
}

// ------------------------------------------------------------ rendering

// renderBrowser draws the whole browser and returns the number of lines
// written; with maxRows fixed, that count is stable across redraws so the
// caller can erase in place.
func renderBrowser(w io.Writer, t *Theme, st *browserState, height int) int {
	maxRows := height - 3 // header + footer + one spare line
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > 40 {
		maxRows = 40
	}

	header := "  files · " + filepath.Base(st.Root)
	if st.Cwd != "" {
		header += "/" + st.Cwd
	}
	if st.Filter != "" || st.Filtering {
		header += t.Dim("  filter:") + " " + st.Filter
		if st.Filtering {
			header += "▏"
		}
	}
	fmt.Fprintln(w, t.Bold(header))

	rows := st.Rows
	scroll := 0
	if len(rows) > maxRows {
		scroll = clamp(st.Cursor-maxRows/2, 0, len(rows)-maxRows)
	}
	shown := rows
	if len(shown) > maxRows {
		shown = shown[scroll : scroll+maxRows]
	}

	width := 0
	for _, r := range shown {
		if n := len(r.Label); n > width {
			width = n
		}
	}
	if width > 48 {
		width = 48
	}
	lines := 0
	if len(shown) == 0 {
		fmt.Fprintln(w, "  "+t.Faint("(no matching files)"))
		lines++
	}
	for i, row := range shown {
		cursor := st.Cursor == scroll+i
		marker := " "
		if cursor {
			marker = t.Accent("▸")
		}
		badge := "   "
		if row.Session {
			badge = sessionBadge(t, row)
		}
		label := row.Label
		if len(label) > width {
			label = label[:width] + "…"
		} else {
			label += strings.Repeat(" ", width-len(label))
		}
		if cursor {
			label = t.Bold(label)
		} else if row.Dir {
			label = t.Accent(label)
		} else {
			label = t.Dim(label)
		}
		note := ""
		if !row.Dir && !row.Session {
			note = t.Faint(browserSize(row.Size))
		}
		fmt.Fprintf(w, "  %s %s %s %s\n", marker, badge, label, note)
		lines++
	}
	for ; lines < maxRows; lines++ { // pad so redraws erase cleanly
		fmt.Fprintln(w)
	}

	footer := "  ↑↓ move · → open · ← up · / filter · q quit"
	if st.Filtering {
		footer = "  typing filters · Enter accept · Esc clear"
	}
	fmt.Fprintln(w, t.Faint(footer))
	return lines + 2
}

func sessionBadge(t *Theme, row browserRow) string {
	b := " "
	switch {
	case row.Written:
		b = t.Green("W")
	case row.Edited:
		b = t.Yellow("E")
	case row.Read:
		b = t.Dim("R")
	}
	return "[" + b + t.Dim("]")
}

func browserSize(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// RenderFileList draws a static listing (windows and non-terminal contexts):
// the session section, then the workspace root.
func RenderFileList(w io.Writer, t *Theme, root string, touched []FileBadge, note string) {
	st := &browserState{Root: root, Touched: touched}
	st.loadRows()
	fmt.Fprintln(w, t.Bold("  files · "+filepath.Base(root)))
	for _, row := range st.Rows {
		badge := "   "
		if row.Session {
			badge = sessionBadge(t, row)
		}
		label := row.Label
		if row.Dir {
			label = t.Accent(label)
		} else if !row.Session {
			label = t.Dim(label)
		}
		note2 := ""
		if !row.Dir && !row.Session {
			note2 = t.Faint(browserSize(row.Size))
		}
		fmt.Fprintf(w, "    %s %s %s\n", badge, label, note2)
	}
	if note != "" {
		fmt.Fprintln(w, t.Faint("  "+note))
	}
}
