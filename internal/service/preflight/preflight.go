// Package preflight runs the daemon's startup checks: GPU detection,
// FFmpeg binary presence, payment + registry reachability, dev/prod
// configuration sanity. A misconfigured daemon fails loudly here before
// any listener binds.
package preflight

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/gpu"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Config wires the preflight.
type Config struct {
	Mode      types.Mode
	GPUVendor types.GPUVendor
	Detector  gpu.Detector
	FFmpegBin string
	Dev       bool
	Logger    *slog.Logger
}

// Result is the outcome of a successful preflight.
type Result struct {
	GPU types.GPUProfile
}

// Run executes the preflight checks. Returns the detected GPU profile on
// success or a structured error on failure.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.Detector == nil {
		return Result{}, errors.New("preflight: Detector is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// 1. FFmpeg binary
	if !cfg.Dev {
		if cfg.FFmpegBin == "" {
			return Result{}, fmt.Errorf("%s: ffmpeg_bin is empty", types.ErrCodePreflightFFmpegMissing)
		}
		if _, err := os.Stat(cfg.FFmpegBin); err != nil {
			return Result{}, fmt.Errorf("%s: %s: %w", types.ErrCodePreflightFFmpegMissing, cfg.FFmpegBin, err)
		}
	}

	// 2. GPU detection
	g, err := cfg.Detector.Detect(ctx, cfg.GPUVendor)
	if err != nil {
		return Result{}, err
	}
	if g.Vendor == types.GPUVendorNone && !cfg.Dev {
		return Result{}, fmt.Errorf("%s: detection returned None", types.ErrCodePreflightNoGPU)
	}
	cfg.Logger.Info("preflight.gpu_ok",
		"vendor", string(g.Vendor),
		"model", g.Model,
		"driver", g.DriverVer,
		"h264", g.SupportsH264, "hevc", g.SupportsHEVC, "av1", g.SupportsAV1,
	)
	return Result{GPU: g}, nil
}
