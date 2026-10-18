package ui

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolCallSummary renders a one-line summary and an optional multi-line
// preview for a tool call, used by the transcript and permission prompts.
func ToolCallSummary(name, rawArgs string) (summary, preview string) {
	var args map[string]any
	if rawArgs != "" {
		_ = json.Unmarshal([]byte(rawArgs), &args)
	}
	str := func(key string) string {
		if v, ok := args[key].(string); ok {
			return v
		}
		return ""
	}
	num := func(key string) (int, bool) {
		if v, ok := args[key].(float64); ok {
			return int(v), true
		}
		return 0, false
	}

	switch name {
	case "Bash":
		cmd := strings.TrimSpace(str("command"))
		summary = firstLine(cmd, 120)
		if lines := strings.Split(strings.TrimSpace(cmd), "\n"); len(lines) > 1 {
			preview = "$ " + truncLines(lines, 12)
		}
		return summary, preview
	case "Read":
		summary = str("file_path")
		if off, ok := num("offset"); ok {
			summary += fmt.Sprintf(" from line %d", off)
		}
		if limit, ok := num("limit"); ok {
			summary += fmt.Sprintf(" (%d lines)", limit)
		}
		return summary, ""
	case "Write":
		content := str("content")
		summary = fmt.Sprintf("%s (%d lines)", str("file_path"), lineCount(content))
		preview = "+ " + truncLines(strings.Split(strings.TrimSuffix(content, "\n"), "\n"), 10)
		return summary, preview
	case "Edit":
		summary = str("file_path")
		older, newer := str("old_string"), str("new_string")
		oldLines, newLines := headLines(older, 6), headLines(newer, 6)
		var diff strings.Builder
		for _, l := range oldLines {
			diff.WriteString("- " + l + "\n")
		}
		for _, l := range newLines {
			diff.WriteString("+ " + l + "\n")
		}
		if omitted := (lineCount(older) - len(oldLines)) + (lineCount(newer) - len(newLines)); omitted > 0 {
			diff.WriteString(fmt.Sprintf("… (+%d more lines)", omitted))
		}
		preview = strings.TrimSuffix(diff.String(), "\n")
		return summary, preview
	case "Glob":
		summary = fmt.Sprintf("%s in %s", str("pattern"), orDefault(str("path"), "."))
		return summary, ""
	case "Grep":
		summary = str("pattern")
		if g := str("glob"); g != "" {
			summary += " (" + g + ")"
		}
		if p := str("path"); p != "" {
			summary += " in " + p
		}
		return summary, ""
	case "SearchMemory":
		summary = str("query")
		if s := str("scope"); s != "" && s != "all" {
			summary += " [" + s + "]"
		}
		return summary, ""
	default:
		return firstLine(rawArgs, 120), ""
	}
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1 - boolToInt(strings.HasSuffix(s, "\n"))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func headLines(s string, n int) []string {
	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
}

func truncLines(lines []string, n int) string {
	if len(lines) > n {
		lines = append(lines[:n:n], fmt.Sprintf("… (+%d more lines)", len(lines)-n))
	}
	return strings.Join(lines, "\n")
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
