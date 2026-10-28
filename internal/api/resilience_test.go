package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const okStream = `data: {"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}}

data: [DONE]

`

func chatReq() ChatRequest {
	return ChatRequest{Model: "m", Messages: []Message{UserMessage("hi")}}
}

func fastClient(url string) *Client {
	c := New(url, "sk-test")
	c.SetRetryDelays(time.Millisecond, time.Millisecond, time.Millisecond)
	return c
}

func TestStreamChatRetriesRateLimitThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"slow down","code":"rate_limited"}}`)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, okStream)
	}))
	defer srv.Close()

	var retries []int
	c := fastClient(srv.URL)
	c.OnRetry = func(attempt int, wait time.Duration, err error) { retries = append(retries, attempt) }
	turn, err := c.StreamChat(context.Background(), chatReq(), StreamHandlers{})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Text != "hi" || calls.Load() != 3 {
		t.Fatalf("text=%q calls=%d", turn.Text, calls.Load())
	}
	if len(retries) != 2 || retries[0] != 1 || retries[1] != 2 {
		t.Fatalf("OnRetry attempts = %v", retries)
	}
}

func TestStreamChatGivesUpAfterRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":{"message":"overloaded"}}`)
	}))
	defer srv.Close()

	_, err := fastClient(srv.URL).StreamChat(context.Background(), chatReq(), StreamHandlers{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 503 {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 4 { // first try + 3 retries
		t.Fatalf("calls = %d, want 4", calls.Load())
	}
}

func TestStreamChatDoesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{400, 401, 402, 403, 404, 504} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"nope"}}`)
		}))
		_, err := fastClient(srv.URL).StreamChat(context.Background(), chatReq(), StreamHandlers{})
		srv.Close()
		if err == nil || calls.Load() != 1 {
			t.Fatalf("status %d: err=%v calls=%d (must fail on the first attempt)", status, err, calls.Load())
		}
	}
}

func TestStreamChatRetriesRefusedConnection(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens any more: dial fails before any request is sent

	var retries atomic.Int32
	c := fastClient(url)
	c.OnRetry = func(int, time.Duration, error) { retries.Add(1) }
	if _, err := c.StreamChat(context.Background(), chatReq(), StreamHandlers{}); err == nil {
		t.Fatal("expected an error")
	}
	if retries.Load() != 3 {
		t.Fatalf("retries = %d, want 3", retries.Load())
	}
}

func TestStreamChatRetryRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := New(srv.URL, "sk-test")
	c.SetRetryDelays(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	c.OnRetry = func(int, time.Duration, error) { cancel() }
	start := time.Now()
	_, err := c.StreamChat(ctx, chatReq(), StreamHandlers{})
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

func TestStreamChatTruncatedStreamIsAnError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("content-type", "text/event-stream")
		// some text, then the connection just ends: no finish_reason, no [DONE]
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"par\"}}]}\n\n")
	}))
	defer srv.Close()

	var got strings.Builder
	_, err := fastClient(srv.URL).StreamChat(context.Background(), chatReq(), StreamHandlers{OnText: func(d string) { got.WriteString(d) }})
	if !errors.Is(err, ErrTruncatedStream) {
		t.Fatalf("err = %v, want ErrTruncatedStream", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("a stream that already produced output must not be retried (calls=%d)", calls.Load())
	}
	if got.String() != "par" {
		t.Fatalf("partial text not delivered: %q", got.String())
	}
}

func TestStreamChatDoneWithoutFinishReasonIsAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	turn, err := fastClient(srv.URL).StreamChat(context.Background(), chatReq(), StreamHandlers{})
	if err != nil || turn.Text != "ok" {
		t.Fatalf("turn=%+v err=%v", turn, err)
	}
}

func TestStreamChatIdleStallIsDetected(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select { // then go silent without closing
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := fastClient(srv.URL)
	c.SetStreamIdleTimeout(150 * time.Millisecond)
	start := time.Now()
	_, err := c.StreamChat(context.Background(), chatReq(), StreamHandlers{})
	if !errors.Is(err, ErrStreamStalled) {
		t.Fatalf("err = %v, want ErrStreamStalled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("stall detection too slow")
	}
}

func TestStreamChatUserCancelIsNotAStall(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	c := fastClient(srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := c.StreamChat(ctx, chatReq(), StreamHandlers{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
