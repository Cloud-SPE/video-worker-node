package shellclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestRequiresBaseURLAndSecret(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("want error")
	}
	if _, err := New(Config{BaseURL: "http://x"}); err == nil {
		t.Fatal("want error on missing Secret")
	}
}
