package shellclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newSrv(t *testing.T, want string, status int, resp any) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Worker-Secret"))
		if r.URL.Path != want {
			t.Errorf("path=%s want %s", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv, &seen
}

func TestValidateKeyPostsExpectedFields(t *testing.T) {
	srv, seen := newSrv(t, "/internal/live/validate-key", http.StatusOK, map[string]any{
		"accepted": true, "stream_id": "live_a", "project_id": "proj_1", "recording_enabled": true,
	})
	defer srv.Close()

	c, err := New(Config{BaseURL: srv.URL, Secret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.ValidateKey(context.Background(), ValidateKeyInput{StreamKey: "k", WorkerURL: "http://w"})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !out.Accepted || out.StreamID != "live_a" {
		t.Fatalf("unexpected result: %+v", out)
	}
	if len(*seen) != 1 || (*seen)[0] != "s" {
		t.Fatalf("X-Worker-Secret not set: %v", *seen)
	}
}

func TestUnauthorizedMaps(t *testing.T) {
	srv, _ := newSrv(t, "/internal/live/validate-key", http.StatusUnauthorized, map[string]any{})
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, Secret: "s"})
	_, err := c.ValidateKey(context.Background(), ValidateKeyInput{StreamKey: "k", WorkerURL: "http://w"})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestNotFoundConflictBadRequestMap(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusNotFound, ErrNotFound},
		{http.StatusConflict, ErrConflict},
		{http.StatusBadRequest, ErrBadRequest},
	}
	for _, tc := range cases {
		srv, _ := newSrv(t, "/internal/live/session-tick", tc.status, map[string]string{"error": "x"})
		c, _ := New(Config{BaseURL: srv.URL, Secret: "s"})
		_, err := c.SessionTick(context.Background(), SessionTickInput{StreamID: "x", Seq: 1})
		if !errors.Is(err, tc.want) {
			t.Fatalf("status=%d want=%v got=%v", tc.status, tc.want, err)
		}
		srv.Close()
	}
}

func TestSessionTickRoundTrip(t *testing.T) {
	srv, _ := newSrv(t, "/internal/live/session-tick", http.StatusOK, map[string]any{
		"ok": true, "balance_cents": 999, "runway_seconds": 30, "grace_triggered": false,
	})
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, Secret: "s"})
	out, err := c.SessionTick(context.Background(), SessionTickInput{
		StreamID: "live_a", Seq: 7, DebitSeconds: 5, CumulativeSeconds: 25,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.BalanceCents != 999 || out.RunwaySeconds != 30 {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestPostEventPostsPatternBEventBody(t *testing.T) {
	var got workerEventReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/live/events" {
			t.Fatalf("path=%s want /internal/live/events", r.URL.Path)
		}
		if r.Header.Get("X-Worker-Secret") != "s" {
			t.Fatalf("secret=%q want s", r.Header.Get("X-Worker-Secret"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := New(Config{BaseURL: srv.URL, Secret: "s"})
	err := c.PostEvent(context.Background(), WorkerEventInput{
		GatewaySessionID:    "gw_123",
		WorkerSessionID:     "worker_123",
		WorkID:              "work_123",
		Type:                "session.usage.tick",
		UsageSeq:            7,
		Units:               5,
		UnitType:            "seconds",
		RemainingRunway:     30,
		LowBalance:          false,
		OccurredAt:          time.Unix(1700000000, 123).UTC(),
		MasterStorageKey:    "live/master.m3u8",
		SegmentStorageKeys:  []string{"live/seg-1.ts"},
		TotalDurationSecond: 5,
	})
	if err != nil {
		t.Fatalf("post event: %v", err)
	}
	if got.GatewaySessionID != "gw_123" || got.WorkerSessionID != "worker_123" || got.WorkID != "work_123" {
		t.Fatalf("unexpected ids: %+v", got)
	}
	if got.Type != "session.usage.tick" || got.UsageSeq != 7 || got.Units != 5 || got.UnitType != "seconds" {
		t.Fatalf("unexpected event body: %+v", got)
	}
	if got.RemainingRunway != 30 || got.TotalDurationSecond != 5 {
		t.Fatalf("unexpected accounting fields: %+v", got)
	}
	if got.OccurredAt == "" {
		t.Fatalf("occurred_at missing: %+v", got)
	}
}

func TestRequiresBaseURLAndSecret(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("want error")
	}
	if _, err := New(Config{BaseURL: "http://x"}); err == nil {
		t.Fatal("want error on missing Secret")
	}
}
