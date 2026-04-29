// Package lifecycle coordinates daemon boot/shutdown — runs the
// configured runners + capability reporter + listeners as goroutines and
// blocks until ctx is cancelled.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Cloud-SPE/video-worker-node/internal/service/abrrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/jobrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Config wires the lifecycle.
//
// v3.0.0: Reporter (capability reporter) field removed — workers do
// not self-publish under archetype A.
type Config struct {
	Mode       types.Mode
	JobRunner  *jobrunner.Runner
	ABRRunner  *abrrunner.Runner
	LiveRunner *liverunner.Runner
	HTTPListen func(ctx context.Context) error
	GRPCListen func() error
	GRPCStop   func()
	MetricsListen func(ctx context.Context) error
	Logger     *slog.Logger

	// ABRPlanFn returns the ABR plan for an in-flight job ID; required
	// when ABRRunner is wired.
	ABRPlanFn func(string) (abrrunner.ABRJob, bool)
}

// Run starts every wired service and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.Mode.Validate(); err != nil {
		return err
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)

	if cfg.Mode.IsVOD() && cfg.JobRunner != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.JobRunner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errs <- fmt.Errorf("jobrunner: %w", err)
			}
		}()
	}
	if cfg.Mode.IsABR() && cfg.ABRRunner != nil {
		if cfg.ABRPlanFn == nil {
			return errors.New("lifecycle: ABRPlanFn required when ABRRunner wired")
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.ABRRunner.Run(ctx, cfg.ABRPlanFn); err != nil && !errors.Is(err, context.Canceled) {
				errs <- fmt.Errorf("abrrunner: %w", err)
			}
		}()
	}
	if cfg.HTTPListen != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.HTTPListen(ctx); err != nil && !errors.Is(err, context.Canceled) {
				errs <- fmt.Errorf("http: %w", err)
			}
		}()
	}
	if cfg.GRPCListen != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.GRPCListen(); err != nil {
				cfg.Logger.Warn("lifecycle.grpc_exit", "error", err)
			}
		}()
	}
	if cfg.MetricsListen != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.MetricsListen(ctx); err != nil && !errors.Is(err, context.Canceled) {
				cfg.Logger.Warn("lifecycle.metrics_exit", "error", err)
			}
		}()
	}

	cfg.Logger.Info("lifecycle.started", "mode", string(cfg.Mode))

	select {
	case <-ctx.Done():
	case err := <-errs:
		cfg.Logger.Error("lifecycle.fatal", "error", err)
		// Trigger shutdown of all peers via gRPC stop.
		if cfg.GRPCStop != nil {
			cfg.GRPCStop()
		}
		if cfg.LiveRunner != nil {
			cfg.LiveRunner.Shutdown(context.Background())
		}
		wg.Wait()
		return err
	}

	if cfg.GRPCStop != nil {
		cfg.GRPCStop()
	}
	if cfg.LiveRunner != nil {
		cfg.LiveRunner.Shutdown(context.Background())
	}
	wg.Wait()
	cfg.Logger.Info("lifecycle.stopped")
	return nil
}
