package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/logger"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/scheduler"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "p.yaml")
	os.WriteFile(p, []byte(`presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
`), 0o600)
	pl, _ := presetloader.New(p)
	repo := jobs.New(store.Memory())
	s, err := New(Config{
		Mode: types.ModeVOD, Version: "test", Repo: repo, Presets: pl,
		Scheduler: scheduler.New(scheduler.Config{TotalSlots: 4, LiveReservedSlots: 1}),
		StartedAt: time.Now(), Logger: logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHealth(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	h, err := s.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Mode != types.ModeVOD {
		t.Errorf("mode=%s", h.Mode)
	}
}

func TestListAndGetJob(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.cfg.Repo.Save(context.Background(), types.Job{ID: "j1", Phase: types.PhaseQueued})
	s.cfg.Repo.Save(context.Background(), types.Job{ID: "j2", Phase: types.PhaseEncoding})
	all, err := s.ListJobs(context.Background(), JobFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("got=%d", len(all))
	}
	filtered, _ := s.ListJobs(context.Background(), JobFilter{Phase: types.PhaseQueued})
	if len(filtered) != 1 {
		t.Errorf("filtered=%d", len(filtered))
	}
	got, err := s.GetJob(context.Background(), "j1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "j1" {
		t.Errorf("id=%q", got.ID)
	}
	if _, err := s.GetJob(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetCapacity(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	rep, err := s.GetCapacity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Mode != types.ModeVOD {
		t.Errorf("mode=%s", rep.Mode)
	}
	if rep.GPUSlotsTotal != 4 {
		t.Errorf("gpu_slots_total=%d", rep.GPUSlotsTotal)
	}
	if rep.GPULiveReserved != 1 {
		t.Errorf("gpu_live_reserved_slots=%d", rep.GPULiveReserved)
	}
	if rep.GPUCostTotal != 400 {
		t.Errorf("gpu_cost_total=%d", rep.GPUCostTotal)
	}
	if rep.GPULiveReservedCost != 100 {
		t.Errorf("gpu_live_reserved_cost=%d", rep.GPULiveReservedCost)
	}
}

func TestForceCancelJob(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	s.cfg.Repo.Save(context.Background(), types.Job{ID: "j", Phase: types.PhaseEncoding})
	if err := s.ForceCancelJob(context.Background(), "j"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.cfg.Repo.Get(context.Background(), "j")
	if got.ErrorCode != "FORCE_CANCELLED" {
		t.Errorf("code=%s", got.ErrorCode)
	}
	if err := s.ForceCancelJob(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestListAndReloadPresets(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	got, err := s.ListPresets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("got=%d", len(got))
	}
	res, err := s.ReloadPresets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.PresetCount != 1 {
		t.Errorf("count=%d", res.PresetCount)
	}
}

func TestListenAndStop(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	dir := t.TempDir()
	sock := filepath.Join(dir, "ops.sock")
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- s.Listen(sock)
	}()
	// Give it time to bind.
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(sock); err != nil {
		t.Errorf("socket not bound: %v", err)
	}
	s.Stop()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Listen did not return")
	}
}

func TestListenEmpty(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	if err := s.Listen(""); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewValidations(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected repo error")
	}
	if _, err := New(Config{Repo: jobs.New(store.Memory())}); err == nil {
		t.Fatal("expected presets error")
	}
}
