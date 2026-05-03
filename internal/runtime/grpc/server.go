// Package grpc serves operator RPCs over a unix socket. Per core belief
// #13, operator surfaces are unix-socket only — never TCP.
//
// The operator surface is small at v1: Health, ListJobs, GetJob,
// GetCapacity, ForceCancelJob, ListPresets, ReloadPresets. These are
// exposed via a Go-native interface that an external `.proto` consumer
// can be wrapped around in a future plan.
package grpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/scheduler"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/abrrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/jobrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Surface is the operator gRPC surface. Implementations are tested via
// direct method calls (no gRPC required for tests).
type Surface interface {
	Health(ctx context.Context) (HealthStatus, error)
	ListJobs(ctx context.Context, filter JobFilter) ([]types.Job, error)
	GetJob(ctx context.Context, id string) (types.Job, error)
	GetCapacity(ctx context.Context) (types.CapacityReport, error)
	ForceCancelJob(ctx context.Context, id string) error
	ListPresets(ctx context.Context) ([]types.Preset, error)
	ReloadPresets(ctx context.Context) (ReloadResult, error)
}

// HealthStatus is the response to Health.
type HealthStatus struct {
	Mode          types.Mode       `json:"mode"`
	Version       string           `json:"version"`
	Dev           bool             `json:"dev"`
	GPU           types.GPUProfile `json:"gpu"`
	UptimeSeconds float64          `json:"uptime_seconds"`
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	Phase types.JobPhase
}

// ReloadResult reports the outcome of ReloadPresets.
type ReloadResult struct {
	PresetCount int    `json:"preset_count"`
	Message     string `json:"message"`
}

// Config wires the gRPC server.
type Config struct {
	Mode       types.Mode
	Version    string
	Dev        bool
	GPU        types.GPUProfile
	Repo       *jobs.Repo
	JobRunner  *jobrunner.Runner
	ABRRunner  *abrrunner.Runner
	LiveRunner *liverunner.Runner
	Scheduler  scheduler.Controller
	Presets    *presetloader.Loader
	StartedAt  time.Time
	Logger     *slog.Logger
}

// Server is the operator gRPC server.
type Server struct {
	cfg    Config
	mu     sync.Mutex
	srv    *grpc.Server
	socket string
}

// New constructs a Server. Validates required deps.
func New(cfg Config) (*Server, error) {
	if cfg.Repo == nil {
		return nil, errors.New("grpc: Repo is required")
	}
	if cfg.Presets == nil {
		return nil, errors.New("grpc: Presets is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now()
	}
	return &Server{cfg: cfg}, nil
}

// Health returns the daemon health snapshot.
func (s *Server) Health(_ context.Context) (HealthStatus, error) {
	return HealthStatus{
		Mode: s.cfg.Mode, Version: s.cfg.Version, Dev: s.cfg.Dev,
		GPU: s.cfg.GPU, UptimeSeconds: time.Since(s.cfg.StartedAt).Seconds(),
	}, nil
}

// ListJobs returns all jobs (optionally filtered by Phase).
func (s *Server) ListJobs(ctx context.Context, filter JobFilter) ([]types.Job, error) {
	all, err := s.cfg.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	if filter.Phase == "" {
		return all, nil
	}
	out := make([]types.Job, 0, len(all))
	for _, j := range all {
		if j.Phase == filter.Phase {
			out = append(out, j)
		}
	}
	return out, nil
}

// GetJob returns one job by ID.
func (s *Server) GetJob(ctx context.Context, id string) (types.Job, error) {
	if id == "" {
		return types.Job{}, errors.New("grpc: job id required")
	}
	return s.cfg.Repo.Get(ctx, id)
}

// GetCapacity returns the current load.
func (s *Server) GetCapacity(_ context.Context) (types.CapacityReport, error) {
	rep := types.CapacityReport{Mode: s.cfg.Mode, GPU: s.cfg.GPU}
	if s.cfg.JobRunner != nil {
		rep.MaxQueueSize = s.cfg.JobRunner.MaxConcurrent()
		rep.ActiveJobs += s.cfg.JobRunner.ActiveCount()
		rep.QueuedJobs = s.cfg.JobRunner.QueueDepth()
	}
	if s.cfg.ABRRunner != nil {
		if rep.MaxQueueSize == 0 {
			rep.MaxQueueSize = s.cfg.ABRRunner.MaxConcurrent()
		}
		rep.ActiveJobs += s.cfg.ABRRunner.ActiveCount()
		rep.QueuedJobs += s.cfg.ABRRunner.QueueDepth()
	}
	if s.cfg.LiveRunner != nil {
		rep.ActiveStreams = s.cfg.LiveRunner.ActiveCount()
	}
	if s.cfg.Scheduler != nil {
		snap := s.cfg.Scheduler.Snapshot()
		rep.GPUSlotsTotal = snap.TotalSlots
		rep.GPULiveReserved = snap.LiveReservedSlots
		rep.GPUCostTotal = snap.TotalCost
		rep.GPULiveReservedCost = snap.LiveReservedCost
		rep.GPUActiveSlots = snap.ActiveSlots
		rep.GPUActiveBatch = snap.ActiveBatchSlots
		rep.GPUActiveLive = snap.ActiveLiveSlots
		rep.GPUActiveCost = snap.ActiveCost
		rep.GPUActiveBatchCost = snap.ActiveBatchCost
		rep.GPUActiveLiveCost = snap.ActiveLiveCost
		rep.GPUQueuedBatchJobs = snap.QueuedBatch
	}
	return rep, nil
}

// ForceCancelJob marks a job as cancelled (writes JOB_CANCELLED error).
// At v1 we don't yank a running ffmpeg subprocess via this RPC — that's a
// future plan.
func (s *Server) ForceCancelJob(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("grpc: job id required")
	}
	_, err := s.cfg.Repo.MarkError(ctx, id, "FORCE_CANCELLED", "operator forced cancellation")
	return err
}

// ListPresets returns the current preset catalogue.
func (s *Server) ListPresets(_ context.Context) ([]types.Preset, error) {
	return s.cfg.Presets.Catalogue().Presets, nil
}

// ReloadPresets re-reads the YAML preset file from disk.
func (s *Server) ReloadPresets(_ context.Context) (ReloadResult, error) {
	if err := s.cfg.Presets.Reload(); err != nil {
		return ReloadResult{}, err
	}
	cat := s.cfg.Presets.Catalogue()
	return ReloadResult{PresetCount: len(cat.Presets), Message: "ok"}, nil
}

// Listen binds a unix socket and serves the gRPC server. Returns a
// non-nil error iff binding or serving fails.
//
// The socket file is removed first (if stale). Per-connection auth is
// the unix filesystem (operators chmod the socket).
func (s *Server) Listen(socketPath string) error {
	if socketPath == "" {
		return errors.New("grpc: empty socket path")
	}
	_ = os.Remove(socketPath)
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("grpc: listen: %w", err)
	}
	srv := grpc.NewServer()
	s.mu.Lock()
	s.srv = srv
	s.socket = socketPath
	s.mu.Unlock()
	return srv.Serve(lis)
}

// Stop gracefully shuts down the gRPC server.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		s.srv.GracefulStop()
	}
	if s.socket != "" {
		_ = os.Remove(s.socket)
	}
}
