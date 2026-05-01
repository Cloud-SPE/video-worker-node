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
	"sync"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/metrics"
)

// Server wraps the listener.
type Server struct {
	addr      string
	maxSeries int
	srv       *http.Server
	boundAddr string
	mu        sync.Mutex
}

// New constructs a Server. Returns nil + nil error if addr is empty
// (listener disabled, the default).
func New(addr string, maxSeries int) (*Server, error) {
	if addr == "" {
		return nil, nil
	}
	return &Server{addr: addr, maxSeries: maxSeries}, nil
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

// ErrAddrEmpty is returned when New is called with an empty addr (listener
// disabled). Exposed for test legibility.
var ErrAddrEmpty = errors.New("metrics: listener disabled (empty addr)")
