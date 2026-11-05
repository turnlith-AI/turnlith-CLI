package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func fastClient(url string) *Client {
	c := New(url, "sk-test")
	c.SetRetryDelays(time.Millisecond, time.Millisecond, time.Millisecond)
	return c
}

func TestDoRetriesRateLimitThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"slow down","code":"rate_limited"}}`)
			return
		}
		fmt.Fprint(w, `{"agents":[{"codename":"sigma","model":"m:1"}]}`)
	}))
	defer srv.Close()

	var retries []int
	c := fastClient(srv.URL)
	c.OnRetry = func(attempt int, wait time.Duration, err error) { retries = append(retries, attempt) }
	agents, err := c.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || calls.Load() != 3 {
		t.Fatalf("agents=%v calls=%d", agents, calls.Load())
	}
	if len(retries) != 2 || retries[0] != 1 || retries[1] != 2 {
		t.Fatalf("OnRetry attempts = %v", retries)
	}
}

func TestDoGivesUpAfterRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
	}))
	defer srv.Close()

	err := fastClient(srv.URL).Health(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 4 { // first try + 3 retries
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestDoDoesNotRetryClientErrors(t *testing.T) {
	// 504 is deliberately absent from the retryable set: the upstream may
	// already have started the work.
	for _, status := range []int{400, 401, 402, 403, 404, 500, 504} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"nope"}}`)
		}))
		err := fastClient(srv.URL).Health(context.Background())
		srv.Close()
		if err == nil || calls.Load() != 1 {
			t.Fatalf("status %d: err=%v calls=%d (must fail on the first attempt)", status, err, calls.Load())
		}
	}
}

func TestDoRetriesAPostWithItsBodyIntact(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 512)
		n, _ := r.Body.Read(buf)
		bodies = append(bodies, string(buf[:n]))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"codename":"x","model":"m:1"}`)
	}))
	defer srv.Close()

	if _, err := fastClient(srv.URL).CreateAgent(context.Background(), CreateAgentInput{Model: "m:1"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] == "" || bodies[0] != bodies[1] {
		t.Fatalf("the retried request must resend the same body, got %q", bodies)
	}
}

func TestDoRetriesRefusedConnection(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more: dial fails before any request is sent

	var retries atomic.Int32
	c := fastClient(url)
	c.OnRetry = func(int, time.Duration, error) { retries.Add(1) }
	if err := c.Health(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if retries.Load() != 3 {
		t.Fatalf("retries = %d, want 3", retries.Load())
	}
}

func TestSetRetryDelaysWithNoArgumentsFailsFast(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(srv.URL, "sk-test")
	c.SetRetryDelays() // what `tlc doctor` does
	if err := c.Health(context.Background()); err == nil || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
}

func TestDoRetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(srv.URL, "sk-test")
	c.SetRetryDelays(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	c.OnRetry = func(int, time.Duration, error) { cancel() }
	start := time.Now()
	err := c.Health(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancel did not interrupt the backoff sleep")
	}
}

func TestRetryDelayHonorsRetryAfter(t *testing.T) {
	if got := retryDelay(time.Second, ""); got != time.Second {
		t.Fatalf("default = %v", got)
	}
	if got := retryDelay(time.Second, "7"); got != 7*time.Second {
		t.Fatalf("seconds = %v", got)
	}
	if got := retryDelay(time.Second, "100000"); got != maxRetryAfter {
		t.Fatalf("cap = %v", got)
	}
	if got := retryDelay(time.Second, "garbage"); got != time.Second {
		t.Fatalf("garbage = %v", got)
	}
	date := time.Now().Add(10 * time.Second).UTC().Format(http.TimeFormat)
	if got := retryDelay(time.Second, date); got < 5*time.Second || got > 11*time.Second {
		t.Fatalf("http-date = %v", got)
	}
}
