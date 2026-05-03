package metrics

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func waitForAddr(t *testing.T, s *Server) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := s.Addr(); addr != "" {
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("listener did not publish bound address")
	return ""
}

func TestNewEmptyDisabled(t *testing.T) {
	t.Parallel()
	s, err := New("", 0)
	if err != nil || s != nil {
		t.Fatalf("expected nil server, got %v err=%v", s, err)
	}
}

func TestListenAndStop(t *testing.T) {
	t.Parallel()
	s, err := New("127.0.0.1:0", 100)
	if err != nil {
		t.Fatal(err)
	}
	if s.Recorder() == nil {
		t.Fatal("nil recorder")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Listen(ctx) }()
	// Listen blocks; cancel ctx after the bind succeeds.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Errorf("err=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return")
	}
}

func TestListenInvalidAddr(t *testing.T) {
	t.Parallel()
	s, _ := New("999.999.999.999:0", 0)
	err := s.Listen(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHealthAndMetrics(t *testing.T) {
	t.Parallel()
	s, _ := New("127.0.0.1:0", 0)
	s.SetSnapshotFunc(func() Snapshot {
		return Snapshot{
			GPUVendor:      "nvidia",
			GPUMaxSessions: 8,
			ActiveJobs:     3,
			ActiveStreams:  2,
			Scheduler: SchedulerSnapshot{
				TotalSlots:        6,
				LiveReservedSlots: 1,
				TotalCost:         600,
				LiveReservedCost:  100,
				ActiveSlots:       4,
				ActiveCost:        360,
				QueuedBatch:       2,
			},
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Listen(ctx) }()
	addr := waitForAddr(t, s)
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: status=%d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp, err = http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics: status=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read metrics body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	for _, needle := range []string{
		`livepeer_videoworker_gpu_sessions_inflight{gpu_vendor="nvidia"} 4`,
		`livepeer_videoworker_gpu_cost_capacity{gpu_vendor="nvidia"} 600`,
		`livepeer_videoworker_gpu_cost_inflight{gpu_vendor="nvidia"} 360`,
		`livepeer_videoworker_gpu_batch_queue_depth{gpu_vendor="nvidia"} 2`,
		`livepeer_videoworker_jobs_active{gpu_vendor="nvidia"} 3`,
		`livepeer_videoworker_streams_active{gpu_vendor="nvidia"} 2`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("missing metric line %q in:\n%s", needle, text)
		}
	}
	if got := s.Recorder(); got == nil {
		t.Fatal("recorder")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("done timeout")
	}
	_ = http.NoBody
}

func TestStopIdempotent(t *testing.T) {
	t.Parallel()
	s, _ := New("127.0.0.1:0", 0)
	s.Stop() // before Listen — should not panic
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Listen(ctx)
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	s.Stop()
}
