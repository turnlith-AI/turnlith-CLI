package cli

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/turnlith-AI/turnlith-CLI/internal/tools"
)

// Path completion for @file mentions: walk the workspace once (skipping the
// heavyweight directories), then match the typed text against the relative
// path from the start or from any directory boundary — "@cli.go" finds
// "internal/cli/cli.go", "@ui/re" finds "internal/ui/render.go".

const (
	completeMaxWalked  = 10000 // entries collected from the walk, a walk budget
	completeMaxResults = 50    // candidates offered at once
)

// listPaths walks root and returns slash-relative paths; directories carry a
// trailing "/". Hidden files are listed; skip-listed trees are pruned.
func listPaths(root string) []string {
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if len(paths) >= completeMaxWalked {
			return fs.SkipAll
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != root && tools.IsSkipDir(d.Name()) {
				return fs.SkipDir
			}
			paths = append(paths, rel+"/")
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	return paths
}

// pathMatches reports whether typed matches rel: a prefix of the whole path,
// or the tail starting at any directory boundary.
func pathMatches(rel, typed string) bool {
	if typed == "" || strings.HasPrefix(rel, typed) {
		return true
	}
	return strings.Contains("/"+rel, "/"+typed)
}

// pathCandidates returns up to completeMaxResults workspace paths matching
// typed (without the leading "@"): exact-path prefix matches first, then
// boundary matches; directories before files inside each group.
func pathCandidates(root, typed string) []string {
	all := listPaths(root)
	group := func(rel string) int {
		if strings.HasPrefix(rel, typed) {
			return 0
		}
		return 1
	}
	sort.SliceStable(all, func(i, j int) bool {
		gi, gj := group(all[i]), group(all[j])
		if gi != gj {
			return gi < gj
		}
		di, dj := strings.HasSuffix(all[i], "/"), strings.HasSuffix(all[j], "/")
		if di != dj {
			return di // directories first
		}
		return all[i] < all[j]
	})
	out := make([]string, 0, completeMaxResults)
	for _, rel := range all {
		if rel == typed {
			continue // already fully typed — completing to itself is a no-op
		}
		if !pathMatches(rel, typed) {
			continue
		}
		out = append(out, rel)
		if len(out) >= completeMaxResults {
			break
		}
	}
	return out
}
