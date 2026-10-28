package tools

import (
	"fmt"
	"strings"
)

// diffMaxCells bounds the LCS table (old lines × new lines, after trimming
// the common head and tail). Beyond it the changed region is reported as
// wholly replaced — still correct, just not minimal.
const diffMaxCells = 4_000_000

type diffOp struct {
	kind  byte // ' ' unchanged, '-' removed, '+' added
	oldNo int  // 1-based line numbers; the one that does not apply is 0
	newNo int
	text  string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffOps computes a line edit script from a to b: common head and tail are
// trimmed first (an edit is usually small), then the middle is diffed by LCS.
func diffOps(a, b []string) []diffOp {
	head := 0
	for head < len(a) && head < len(b) && a[head] == b[head] {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head && a[len(a)-1-tail] == b[len(b)-1-tail] {
		tail++
	}
	am, bm := a[head:len(a)-tail], b[head:len(b)-tail]

	var ops []diffOp
	for i := 0; i < head; i++ {
		ops = append(ops, diffOp{kind: ' ', oldNo: i + 1, newNo: i + 1, text: a[i]})
	}
	oldNo, newNo := head, head
	del := func(s string) { oldNo++; ops = append(ops, diffOp{kind: '-', oldNo: oldNo, text: s}) }
	add := func(s string) { newNo++; ops = append(ops, diffOp{kind: '+', newNo: newNo, text: s}) }
	same := func(s string) {
		oldNo++
		newNo++
		ops = append(ops, diffOp{kind: ' ', oldNo: oldNo, newNo: newNo, text: s})
	}

	n, m := len(am), len(bm)
	switch {
	case n == 0:
		for _, s := range bm {
			add(s)
		}
	case m == 0:
		for _, s := range am {
			del(s)
		}
	case n*m > diffMaxCells:
		for _, s := range am {
			del(s)
		}
		for _, s := range bm {
			add(s)
		}
	default:
		// lcs[i*(m+1)+j] = length of the LCS of am[i:] and bm[j:]
		lcs := make([]int32, (n+1)*(m+1))
		at := func(i, j int) int { return i*(m+1) + j }
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if am[i] == bm[j] {
					lcs[at(i, j)] = lcs[at(i+1, j+1)] + 1
				} else {
					lcs[at(i, j)] = max(lcs[at(i+1, j)], lcs[at(i, j+1)])
				}
			}
		}
		for i, j := 0, 0; i < n || j < m; {
			switch {
			case i < n && j < m && am[i] == bm[j]:
				same(am[i])
				i++
				j++
			case j >= m || (i < n && lcs[at(i+1, j)] >= lcs[at(i, j+1)]):
				del(am[i])
				i++
			default:
				add(bm[j])
				j++
			}
		}
	}
	for i := len(a) - tail; i < len(a); i++ {
		oldNo++
		newNo++
		ops = append(ops, diffOp{kind: ' ', oldNo: oldNo, newNo: newNo, text: a[i]})
	}
	return ops
}

// Diff renders the change from old to updated as numbered lines — context
// lines, "-" removals (old line numbers) and "+" additions (new line numbers)
// — with `context` unchanged lines around each change and " …" between distant
// hunks. Output stops after maxLines lines with a count of what was left out.
// The counts always describe the whole change. Identical inputs give "".
func Diff(old, updated string, context, maxLines int) (string, int, int) {
	ops := diffOps(splitLines(old), splitLines(updated))
	added, removed := 0, 0
	for _, op := range ops {
		switch op.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	if added == 0 && removed == 0 {
		return "", 0, 0
	}

	// keep[i]: the op is a change or within `context` lines of one
	keep := make([]bool, len(ops))
	lastChange := -1 << 30
	for i, op := range ops {
		if op.kind != ' ' {
			lastChange = i
			for k := max(0, i-context); k <= i; k++ {
				keep[k] = true
			}
		} else if i-lastChange <= context {
			keep[i] = true
		}
	}

	lastKeep := -1
	for i, k := range keep {
		if k {
			lastKeep = i
		}
	}

	var out []string
	for i, op := range ops {
		if !keep[i] {
			if len(out) > 0 && out[len(out)-1] != " …" && i < lastKeep {
				out = append(out, " …")
			}
			continue
		}
		no := op.newNo
		if op.kind == '-' {
			no = op.oldNo
		}
		text := op.text
		if r := []rune(text); len(r) > 200 {
			text = string(r[:200]) + "…"
		}
		out = append(out, fmt.Sprintf("%4d %c %s", no, op.kind, text))
	}
	if len(out) > 0 && out[len(out)-1] == " …" {
		out = out[:len(out)-1]
	}
	if maxLines > 0 && len(out) > maxLines {
		extra := len(out) - maxLines
		out = append(out[:maxLines:maxLines], fmt.Sprintf("… (+%d more lines)", extra))
	}
	return strings.Join(out, "\n"), added, removed
}
