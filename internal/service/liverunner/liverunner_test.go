package liverunner

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	repo := jobs.New(store.Memory())
	r, err := New(Config{Repo: repo})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return r
}

func TestNewDefaultsAreApplied(t *testing.T) {
	r := newTestRunner(t)
	if r.cfg.DebitCadence == 0 {
		t.Error("DebitCadence default not applied")
	}
	if r.cfg.RunwaySeconds != 30 {
		t.Errorf("RunwaySeconds=%d want 30", r.cfg.RunwaySeconds)
	}
	if r.cfg.GraceSeconds != 60 {
		t.Errorf("GraceSeconds=%d want 60", r.cfg.GraceSeconds)
	}
}

func TestStartStopActiveCount(t *testing.T) {
	r := newTestRunner(t)
	ctx := context.Background()

	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("initial ActiveCount=%d want 0", got)
	}

	if _, err := r.Start(ctx, StartRequest{WorkID: "w-1", Preset: "h264-live"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := r.Start(ctx, StartRequest{WorkID: "w-2", Preset: "h264-live"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if got := r.ActiveCount(); got != 2 {
		t.Fatalf("ActiveCount=%d want 2", got)
	}

	got, err := r.Status(ctx, "w-1")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.GatewaySessionID != "w-1" {
		t.Fatalf("GatewaySessionID=%q want w-1", got.GatewaySessionID)
	}

	if err := r.Stop(ctx, "w-1"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := r.ActiveCount(); got != 1 {
		t.Fatalf("after stop ActiveCount=%d want 1", got)
	}
}

func TestStatusUnknownStream(t *testing.T) {
	r := newTestRunner(t)
	_, err := r.Status(context.Background(), "missing")
	if !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("expected ErrStreamNotFound, got %v", err)
	}
}

func TestShutdownClearsAllStreams(t *testing.T) {
	r := newTestRunner(t)
	ctx := context.Background()
	for _, id := range []string{"w-1", "w-2", "w-3"} {
		if _, err := r.Start(ctx, StartRequest{WorkID: id, Preset: "h264-live"}); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
	}
	r.Shutdown(ctx)
	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("after shutdown ActiveCount=%d want 0", got)
	}
}

func TestTopupIsNoOpAtSkeleton(t *testing.T) {
	r := newTestRunner(t)
	if err := r.Topup(context.Background(), "any", []byte("ticket")); err != nil {
		t.Fatalf("expected nil from skeleton Topup, got %v", err)
	}
}

func TestStartPersistsPatternBCorrelationState(t *testing.T) {
	r := newTestRunner(t)
	ctx := context.Background()

	_, err := r.Start(ctx, StartRequest{
		WorkID:          "gw_123",
		Sender:          []byte("sender"),
		PaymentWorkID:   "work_123",
		WorkerSessionID: "worker_123",
		Preset:          "h264-live",
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	got, err := r.Status(ctx, "gw_123")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.GatewaySessionID != "gw_123" || got.WorkerSessionID != "worker_123" || got.PaymentWorkID != "work_123" {
		t.Fatalf("unexpected stream state: %+v", got)
	}
	if string(got.Sender) != "sender" {
		t.Fatalf("sender=%q want sender", string(got.Sender))
	}
}

func TestTopupPersistsLastTopupAtBeforeAccept(t *testing.T) {
	r := newTestRunner(t)
	ctx := context.Background()

	if _, err := r.Start(ctx, StartRequest{WorkID: "gw_123", Preset: "h264-live"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := r.Topup(ctx, "gw_123", []byte("ticket")); err != nil {
		t.Fatalf("topup: %v", err)
	}

	got, err := r.Status(ctx, "gw_123")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.LastTopupAt.IsZero() {
		t.Fatal("expected LastTopupAt to be set")
	}
}

func TestStopPersistsClosedStateBeforeAccept(t *testing.T) {
	r := newTestRunner(t)
	ctx := context.Background()

	if _, err := r.Start(ctx, StartRequest{WorkID: "gw_123", Preset: "h264-live"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := r.Stop(ctx, "gw_123"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("ActiveCount=%d want 0", got)
	}

	repoStream, err := r.cfg.Repo.GetStream(ctx, "gw_123")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if repoStream.Phase != types.StreamPhaseClosed {
		t.Fatalf("Phase=%q want %q", repoStream.Phase, types.StreamPhaseClosed)
	}
	if repoStream.CloseReason != sessionEndReasonAdminStop {
		t.Fatalf("CloseReason=%q want %q", repoStream.CloseReason, sessionEndReasonAdminStop)
	}
	if repoStream.ClosedAt.IsZero() {
		t.Fatal("expected ClosedAt to be set")
	}
}
