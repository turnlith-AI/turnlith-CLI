package tools

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHeadTailBufferKeepsBothEnds(t *testing.T) {
	b := newHeadTailBuffer(10, 10)
	for i := 0; i < 100; i++ {
		_, _ = b.Write([]byte("0123456789"))
	}
	_, _ = b.Write([]byte("THE-END-OK"))
	got := b.String()
	if !strings.HasPrefix(got, "0123456789") {
		t.Fatalf("head lost: %q", got)
	}
	if !strings.HasSuffix(got, "THE-END-OK") {
		t.Fatalf("tail lost: %q", got)
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("no omission marker: %q", got)
	}
	if len(got) > 200 {
		t.Fatalf("buffer grew unbounded: %d bytes", len(got))
	}
}

func TestHeadTailBufferSmallOutputIsUntouched(t *testing.T) {
	b := newHeadTailBuffer(100, 100)
	_, _ = b.Write([]byte("hello "))
	_, _ = b.Write([]byte("world"))
	if got := b.String(); got != "hello world" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateMiddleKeepsHeadAndTail(t *testing.T) {
	s := strings.Repeat("a", 1000) + strings.Repeat("m", 5000) + strings.Repeat("z", 1000)
	got := truncateMiddle(s, 100, 200)
	if !strings.HasPrefix(got, strings.Repeat("a", 100)) || !strings.HasSuffix(got, strings.Repeat("z", 200)) {
		t.Fatalf("ends not preserved: %q", got)
	}
	if !strings.Contains(got, "omitted") || len(got) > 400 {
		t.Fatalf("bad truncation (%d bytes): %q", len(got), got)
	}
	if short := "tiny"; truncateMiddle(short, 100, 200) != short {
		t.Fatal("short strings must pass through")
	}
}

func TestBashKeepsTheTailOfLongOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	tool := &BashTool{Root: t.TempDir(), DefaultTimeout: 20 * time.Second}
	out := run(t, tool, map[string]any{
		// ~2.4 MB of noise, then the line that matters
		"command": `i=0; while [ $i -lt 40000 ]; do echo "noise line number $i padding padding padding padding"; i=$((i+1)); done; echo "FAIL: the real error" >&2; exit 3`,
	})
	if !strings.Contains(out, "FAIL: the real error") {
		t.Fatalf("tail (stderr) lost:\n%s", out[max(0, len(out)-300):])
	}
	if !strings.Contains(out, "(exit code 3)") {
		t.Fatalf("exit code lost:\n%s", out[max(0, len(out)-300):])
	}
	if !strings.Contains(out, "noise line number 0 ") {
		t.Fatalf("head lost:\n%s", out[:min(len(out), 300)])
	}
	if len(out) > 40000 {
		t.Fatalf("result not bounded: %d bytes", len(out))
	}
}
