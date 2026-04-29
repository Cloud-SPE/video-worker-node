package webhooks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignBodyDeterministic(t *testing.T) {
	t.Parallel()
	a := SignBody([]byte("hello"), "k")
	b := SignBody([]byte("hello"), "k")
	if a != b {
		t.Fatalf("not deterministic: %s vs %s", a, b)
	}
	c := SignBody([]byte("hello"), "j")
	if a == c {
		t.Fatal("different secrets should differ")
	}
}

func TestHTTPSenderSuccess(t *testing.T) {
	t.Parallel()
	gotSig := ""
	gotEvent := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Video-Signature")
		gotEvent = r.Header.Get("X-Webhook-Event")
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	s := NewHTTP()
	err := s.Send(context.Background(), Delivery{
		URL: srv.URL, Event: "job.complete", Secret: "shh",
		Payload: map[string]string{"id": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSig == "" || gotEvent != "job.complete" {
		t.Fatalf("missing headers: sig=%q event=%q", gotSig, gotEvent)
	}
}

func TestHTTPSenderEmptyURLNoOp(t *testing.T) {
	t.Parallel()
	s := NewHTTP()
	if err := s.Send(context.Background(), Delivery{}); err != nil {
		t.Fatalf("err=%v", err)
	}
}

type retryClient struct {
	attempts atomic.Int32
	failFor  int32
}

func (r *retryClient) Do(req *http.Request) (*http.Response, error) {
	a := r.attempts.Add(1)
	if a <= r.failFor {
		return nil, errors.New("transient")
	}
	resp := &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}
	return resp, nil
}

func TestHTTPSenderRetry(t *testing.T) {
	t.Parallel()
	rc := &retryClient{failFor: 2}
	slept := []time.Duration{}
	s := &HTTPSender{
		Client:   rc,
		Backoffs: []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond},
		SleepFn:  func(d time.Duration) { slept = append(slept, d) },
	}
	err := s.Send(context.Background(), Delivery{URL: "https://example/", Event: "e", Payload: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rc.attempts.Load() != 3 {
		t.Fatalf("attempts=%d want 3", rc.attempts.Load())
	}
	if len(slept) != 2 {
		t.Fatalf("slept=%v", slept)
	}
}

func TestHTTPSenderAllFail(t *testing.T) {
	t.Parallel()
	rc := &retryClient{failFor: 100}
	s := &HTTPSender{
		Client:   rc,
		Backoffs: []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond},
		SleepFn:  func(time.Duration) {},
	}
	err := s.Send(context.Background(), Delivery{URL: "https://example/", Event: "e"})
	if err == nil {
		t.Fatal("expected error")
	}
}

type non2xx struct{ status int }

func (n non2xx) Do(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: n.status, Body: http.NoBody}, nil
}

func TestHTTPSenderNon2xx(t *testing.T) {
	t.Parallel()
	s := &HTTPSender{
		Client:   non2xx{status: 503},
		Backoffs: []time.Duration{1 * time.Millisecond},
		SleepFn:  func(time.Duration) {},
	}
	err := s.Send(context.Background(), Delivery{URL: "https://example", Event: "e"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHTTPSenderCtxCancel(t *testing.T) {
	t.Parallel()
	rc := &retryClient{failFor: 100}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	s := &HTTPSender{
		Client:   rc,
		Backoffs: []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond},
		SleepFn:  func(time.Duration) { calls++; if calls == 1 { cancel() } },
	}
	err := s.Send(ctx, Delivery{URL: "https://example", Event: "e"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestFakeSender(t *testing.T) {
	t.Parallel()
	f := &FakeSender{}
	if err := f.Send(context.Background(), Delivery{Event: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Send(context.Background(), Delivery{Event: "b"}); err != nil {
		t.Fatal(err)
	}
	if got := len(f.Events()); got != 2 {
		t.Fatalf("events=%d", got)
	}
	f.FailFor = "bad"
	if err := f.Send(context.Background(), Delivery{Event: "bad"}); err == nil {
		t.Fatal("expected fail")
	}
	if got := f.EventsByName("a"); len(got) != 1 {
		t.Fatalf("by-name=%d", len(got))
	}
}
