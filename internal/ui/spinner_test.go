package ui

import (
	"sync/atomic"
	"testing"
	"time"
)

type countingWriter struct{ writes atomic.Int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return len(p), nil
}

func TestSpinnerRedrawsAtHumanSpeed(t *testing.T) {
	w := &countingWriter{}
	r := NewRenderer(w, &Theme{Colors: true})
	sp := r.StartSpinner("thinking…")
	if sp == nil {
		t.Fatal("spinner should start on a colored renderer")
	}
	time.Sleep(350 * time.Millisecond)
	sp.Stop()
	// ~10 frames per second: a handful of draws plus the final clear. A
	// nanosecond-scale tick would write tens of thousands of frames.
	if n := w.writes.Load(); n > 15 {
		t.Fatalf("spinner wrote %d times in 350ms; it is busy-looping", n)
	}
	if n := w.writes.Load(); n < 2 {
		t.Fatalf("spinner wrote %d times; expected an animation", n)
	}
}

func TestSpinnerLabelShowsElapsedAfterAWhile(t *testing.T) {
	if got := spinnerLabel("thinking…", time.Second); got != "thinking…" {
		t.Fatalf("early label = %q", got)
	}
	got := spinnerLabel("thinking…", 12*time.Second)
	want := "thinking… (12s · Ctrl-C to interrupt)"
	if got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
}

func TestSpinnerStopIsIdempotentAndNilSafe(t *testing.T) {
	var nilSpinner *Spinner
	nilSpinner.Stop()

	w := &countingWriter{}
	r := NewRenderer(w, &Theme{Colors: true})
	sp := r.StartSpinner("x")
	sp.Stop()
	sp.Stop()
}
