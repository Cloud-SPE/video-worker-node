// Package metrics is the daemon's Prometheus listener (off by default).
//
// Per docs/conventions/metrics.md, the listener binds TCP and exposes
// /metrics + /healthz on `--metrics-listen=:9095` (default). Metric names
// follow the livepeer_videoworker_* prefix.
//
// At v1 the implementation provides only a NoOp Recorder + a stub HTTP
// listener so the daemon's --help / --dev paths boot without pulling in
// prometheus/client_golang. A future plan can swap in the real
// Prometheus-backed Recorder following the protocol-daemon pattern.
package metrics

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/metrics"
)

// SchedulerSnapshot is the runtime-visible GPU scheduler state exposed to
// /metrics without coupling this package to a specific scheduler impl.
type SchedulerSnapshot struct {
	TotalSlots        int
	LiveReservedSlots int
	TotalCost         int
	LiveReservedCost  int
	ActiveSlots       int
	ActiveCost        int
	QueuedBatch       int
}

// Snapshot is the current daemon state rendered by /metrics.
type Snapshot struct {
	GPUVendor      string
	GPUMaxSessions int
	ActiveJobs     int
	ActiveStreams  int
	Scheduler      SchedulerSnapshot
}

// Server wraps the listener.
type Server struct {
	addr       string
	maxSeries  int
	srv        *http.Server
	boundAddr  string
	snapshotFn func() Snapshot
	mu         sync.Mutex
}

// New constructs a Server. Returns nil + nil error if addr is empty
// (listener disabled, the default).
func New(addr string, maxSeries int) (*Server, error) {
	if addr == "" {
		return nil, nil
	}
	return &Server{addr: addr, maxSeries: maxSeries}, nil
}

// SetSnapshotFunc installs a callback used to render runtime gauges on
// /metrics. Nil means render only build info.
func (s *Server) SetSnapshotFunc(fn func() Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshotFn = fn
}

// Recorder returns the daemon's metrics Recorder. At v1 NoOp; a future
// plan plumbs Prometheus.
func (s *Server) Recorder() metrics.Recorder { return metrics.NoOp() }

// Listen binds + serves /metrics + /healthz. Returns when ctx is cancelled
// or the listener errors fatally.
func (s *Server) Listen(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprintf(w, "# HELP %s daemon build info\n", metrics.NameBuildInfo)
		_, _ = fmt.Fprintf(w, "# TYPE %s gauge\n", metrics.NameBuildInfo)
		_, _ = fmt.Fprintf(w, "%s 1\n", metrics.NameBuildInfo)
		for _, line := range s.metricLines() {
			_, _ = fmt.Fprintln(w, line)
		}
	})
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("metrics: listen: %w", err)
	}
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.mu.Lock()
	s.srv = srv
	s.boundAddr = lis.Addr().String()
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(lis) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// Stop shuts the listener down (idempotent).
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		_ = s.srv.Shutdown(context.Background())
	}
}

// Addr returns the bound listener address once Listen has successfully
// started serving. Empty means the listener has not bound yet.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.boundAddr
}

func (s *Server) metricLines() []string {
	s.mu.Lock()
	snapshotFn := s.snapshotFn
	s.mu.Unlock()
	if snapshotFn == nil {
		return nil
	}
	snap := snapshotFn()
	labels := map[string]string{"gpu_vendor": snap.GPUVendor}
	if labels["gpu_vendor"] == "" {
		labels["gpu_vendor"] = "none"
	}
	lines := []string{
		helpType(metrics.NameGPUMaxSessions, "detected maximum GPU sessions", "gauge"),
		metric(metrics.NameGPUMaxSessions, labels, float64(snap.GPUMaxSessions)),
		helpType(metrics.NameGPUSlotsTotal, "configured total scheduler slots", "gauge"),
		metric(metrics.NameGPUSlotsTotal, labels, float64(snap.Scheduler.TotalSlots)),
		helpType(metrics.NameGPUSessionsInflight, "active GPU sessions admitted by scheduler", "gauge"),
		metric(metrics.NameGPUSessionsInflight, labels, float64(snap.Scheduler.ActiveSlots)),
		helpType(metrics.NameGPUCostCapacity, "configured total scheduler cost units", "gauge"),
		metric(metrics.NameGPUCostCapacity, labels, float64(snap.Scheduler.TotalCost)),
		helpType(metrics.NameGPUCostInflight, "active scheduler cost units in use", "gauge"),
		metric(metrics.NameGPUCostInflight, labels, float64(snap.Scheduler.ActiveCost)),
		helpType(metrics.NameGPULiveReserved, "scheduler slots reserved for live workloads", "gauge"),
		metric(metrics.NameGPULiveReserved, labels, float64(snap.Scheduler.LiveReservedSlots)),
		helpType(metrics.NameGPULiveReservedCost, "scheduler cost units reserved for live workloads", "gauge"),
		metric(metrics.NameGPULiveReservedCost, labels, float64(snap.Scheduler.LiveReservedCost)),
		helpType(metrics.NameGPUBatchQueued, "batch jobs waiting on scheduler admission", "gauge"),
		metric(metrics.NameGPUBatchQueued, labels, float64(snap.Scheduler.QueuedBatch)),
		helpType(metrics.NameJobActive, "active batch jobs across VOD and ABR runners", "gauge"),
		metric(metrics.NameJobActive, labels, float64(snap.ActiveJobs)),
		helpType(metrics.NameStreamsActive, "active live streams", "gauge"),
		metric(metrics.NameStreamsActive, labels, float64(snap.ActiveStreams)),
	}
	return lines
}

func helpType(name, help, typ string) string {
	return fmt.Sprintf("# HELP %s %s\n# TYPE %s %s", name, help, name, typ)
}

func metric(name string, labels map[string]string, value float64) string {
	return fmt.Sprintf("%s%s %g", name, renderLabels(labels), value)
}

func renderLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf(`%s="%s"`, key, escapeLabel(labels[key])))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return strings.ReplaceAll(v, `"`, `\"`)
}

// ErrAddrEmpty is returned when New is called with an empty addr (listener
// disabled). Exposed for test legibility.
var ErrAddrEmpty = errors.New("metrics: listener disabled (empty addr)")
