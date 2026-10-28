package ui

import (
	"math/rand"
	"strings"
	"testing"
)

func render(theme *Theme, chunks ...string) string {
	s := NewStyler(theme)
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(s.Write(c))
	}
	b.WriteString(s.Flush())
	return b.String()
}

func TestHeadingsLoseTheirHashes(t *testing.T) {
	cases := map[string]string{
		"# Title\n":        "Title\n",
		"## Sub head\n":    "Sub head\n",
		"###### Six\n":     "Six\n",
		"## With `code`\n": "With `code`\n",
	}
	for in, want := range cases {
		if got := render(plain(), in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	// not headings
	for _, in := range []string{"#hashtag\n", "####### seven\n", "a # b\n", "#\n", "  not # heading\n"} {
		if got := render(plain(), in); got != in {
			t.Errorf("%q must pass through, got %q", in, got)
		}
	}
	if got := render(colored(), "## Hi\n"); !strings.Contains(got, "\x1b[1;38;5;209mH") {
		t.Errorf("heading should be bold accent: %q", got)
	}
}

func TestBulletsBecomeDots(t *testing.T) {
	in := "- one\n  - nested\n* star\n+ plus\nafter\n"
	want := "• one\n  • nested\n• star\n• plus\nafter\n"
	if got := render(plain(), in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	for _, in := range []string{"-5 degrees\n", "---\n", "*args and **kwargs\n", "- \n", "-\n"} {
		got := render(plain(), in)
		if strings.Contains(got, "•") && in != "- \n" {
			t.Errorf("%q wrongly became a bullet: %q", in, got)
		}
	}
	if got := render(plain(), "**bold** start\n"); got != "bold start\n" {
		t.Errorf("bold at line start broke: %q", got)
	}
}

func TestBlockquotes(t *testing.T) {
	if got := render(plain(), "> quoted words\nnormal\n"); got != "▎ quoted words\nnormal\n" {
		t.Fatalf("got %q", got)
	}
	for _, in := range []string{">>> repl\n", "a > b\n", ">no space\n"} {
		if got := render(plain(), in); got != in {
			t.Errorf("%q must pass through, got %q", in, got)
		}
	}
}

func TestMarkdownIsLeftAloneInsideCodeFences(t *testing.T) {
	in := "```md\n# not a heading\n- not a bullet\n> not a quote\n```\n- real bullet\n"
	got := render(plain(), in)
	want := "```md\n# not a heading\n- not a bullet\n> not a quote\n```\n• real bullet\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

const sampleMarkdown = "## Plan\n\nIntro with **bold**, `code` and a * lone star.\n\n- first item\n  - nested `x`\n* second\n\n> a quote\n\n1. one\n2. two\n\n```go\n# comment\n- x\nfunc f() {}\n```\n\n#hash and -5 and ---\n*args **kw\nlast line without newline"

// However the stream is cut into deltas, the output must be identical: the
// renderer holds back only what it cannot decide yet.
func TestStreamingIsChunkInvariant(t *testing.T) {
	for _, theme := range []*Theme{plain(), colored()} {
		whole := render(theme, sampleMarkdown)
		// every single split point
		for i := 0; i <= len(sampleMarkdown); i++ {
			if got := render(theme, sampleMarkdown[:i], sampleMarkdown[i:]); got != whole {
				t.Fatalf("colors=%v split at %d changed the output:\n got %q\nwant %q", theme.Colors, i, got, whole)
			}
		}
		// rune by rune
		var runes []string
		for _, r := range sampleMarkdown {
			runes = append(runes, string(r))
		}
		if got := render(theme, runes...); got != whole {
			t.Fatalf("colors=%v rune-by-rune differs:\n got %q\nwant %q", theme.Colors, got, whole)
		}
		// random chunkings
		rng := rand.New(rand.NewSource(1))
		for n := 0; n < 200; n++ {
			var chunks []string
			rest := []rune(sampleMarkdown)
			for len(rest) > 0 {
				k := 1 + rng.Intn(9)
				if k > len(rest) {
					k = len(rest)
				}
				chunks = append(chunks, string(rest[:k]))
				rest = rest[k:]
			}
			if got := render(theme, chunks...); got != whole {
				t.Fatalf("colors=%v random chunking %q differs:\n got %q\nwant %q", theme.Colors, chunks, got, whole)
			}
		}
	}
}

func FuzzStylerChunkInvariance(f *testing.F) {
	f.Add(sampleMarkdown, 7)
	f.Add("# h\n- a\n> q\n```\n- x\n```\n", 3)
	f.Add("**a*b**\n`x\n", 2)
	f.Fuzz(func(t *testing.T, text string, cut int) {
		if cut < 0 {
			cut = -cut
		}
		runes := []rune(text)
		if len(runes) == 0 {
			return
		}
		cut %= len(runes) + 1
		whole := render(colored(), text)
		if got := render(colored(), string(runes[:cut]), string(runes[cut:])); got != whole {
			t.Fatalf("split at %d changed the output:\n got %q\nwant %q", cut, got, whole)
		}
	})
}

func TestRunSummaryFormat(t *testing.T) {
	cases := []struct {
		ctx, out, turns int
		elapsedMs       int
		want            string
	}{
		{4210, 87, 2, 6300, "4.2k context · 87 out · 2 turns · 6.3s"},
		{950, 12, 1, 400, "950 context · 12 out · 0.4s"},
		{128000, 1500, 1, 72000, "128.0k context · 1.5k out · 1m12s"},
		{0, 0, 1, 1000, "1.0s"},
	}
	for _, c := range cases {
		if got := formatRunSummary(c.ctx, c.out, c.turns, durMs(c.elapsedMs)); got != c.want {
			t.Errorf("formatRunSummary(%d,%d,%d,%dms) = %q, want %q", c.ctx, c.out, c.turns, c.elapsedMs, got, c.want)
		}
	}
}
