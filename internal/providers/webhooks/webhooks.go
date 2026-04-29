// Package webhooks delivers HMAC-SHA256-signed webhook callbacks with a
// configurable retry policy. Per plan 0007 §I, the default retry policy is
// 3 attempts with backoffs (1s, 5s, 25s). Failures are logged but
// non-blocking.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Delivery is one webhook send request.
type Delivery struct {
	URL     string
	Secret  string
	Event   string
	Payload any
}

// Sender is the testable surface.
type Sender interface {
	Send(ctx context.Context, d Delivery) error
}

// HTTPClient is the minimal subset of http.Client we need.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// HTTPSender delivers webhooks over plain HTTP with a configurable retry
// policy. Concurrency-safe.
type HTTPSender struct {
	Client   HTTPClient
	Backoffs []time.Duration
	// SleepFn is the test seam; defaults to time.Sleep.
	SleepFn func(time.Duration)
}

// NewHTTP returns an HTTPSender with backoffs (1s, 5s, 25s).
func NewHTTP() *HTTPSender {
	return &HTTPSender{
		Client:   &http.Client{Timeout: 10 * time.Second},
		Backoffs: []time.Duration{1 * time.Second, 5 * time.Second, 25 * time.Second},
		SleepFn:  time.Sleep,
	}
}

// Send marshals payload as JSON, signs it with HMAC-SHA256(secret, body),
// and PUTs/POSTs it to URL. Retries up to len(Backoffs) on transient
// errors / non-2xx responses; final failure returns an error.
func (s *HTTPSender) Send(ctx context.Context, d Delivery) error {
	if d.URL == "" {
		return nil // no-op when not configured
	}
	body, err := json.Marshal(envelope{Event: d.Event, Data: d.Payload, At: time.Now().UTC()})
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	sig := SignBody(body, d.Secret)
	last := errors.New("webhook: no attempts made")
	attempts := len(s.Backoffs) + 1
	sleep := s.SleepFn
	if sleep == nil {
		sleep = time.Sleep
	}
	for i := 0; i < attempts; i++ {
		if i > 0 {
			sleep(s.Backoffs[i-1])
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Video-Signature", "sha256="+sig)
		req.Header.Set("X-Webhook-Event", d.Event)
		resp, err := s.Client.Do(req)
		if err != nil {
			last = err
			continue
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			return nil
		}
		last = fmt.Errorf("webhook: HTTP %d", resp.StatusCode)
	}
	return last
}

type envelope struct {
	Event string    `json:"event"`
	At    time.Time `json:"at"`
	Data  any       `json:"data,omitempty"`
}

// SignBody returns the HMAC-SHA256 signature of body keyed by secret as
// a hex string. Exposed for tests + downstream verification.
func SignBody(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// FakeSender records delivered events in memory for tests.
type FakeSender struct {
	mu     sync.Mutex
	events []Delivery
	// FailFor causes Send to return an error when Event matches.
	FailFor string
}

// Send records the delivery and optionally fails.
func (f *FakeSender) Send(_ context.Context, d Delivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, d)
	if d.Event == f.FailFor {
		return errors.New("fake webhook fail")
	}
	return nil
}

// Events returns a copy of recorded deliveries.
func (f *FakeSender) Events() []Delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Delivery, len(f.events))
	copy(out, f.events)
	return out
}

// EventsByName returns deliveries whose Event field equals name.
func (f *FakeSender) EventsByName(name string) []Delivery {
	out := []Delivery{}
	for _, e := range f.Events() {
		if e.Event == name {
			out = append(out, e)
		}
	}
	return out
}
