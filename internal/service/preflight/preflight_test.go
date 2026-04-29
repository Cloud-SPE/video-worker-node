package preflight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/gpu"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/logger"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func TestRunDevSkipsFFmpegBin(t *testing.T) {
	t.Parallel()
	res, err := Run(context.Background(), Config{
		Mode: types.ModeVOD, Detector: gpu.FakeNVIDIA(),
		Dev: true, Logger: logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GPU.Vendor != types.GPUVendorNVIDIA {
		t.Errorf("vendor=%v", res.GPU.Vendor)
	}
}

func TestRunMissingFFmpegBin(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Config{
		Mode: types.ModeVOD, Detector: gpu.FakeNVIDIA(),
		FFmpegBin: "/nonexistent/ffmpeg", Logger: logger.Discard(),
	})
	if err == nil || !strings.Contains(err.Error(), "PREFLIGHT_FFMPEG_MISSING") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunFFmpegBinPresent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ffmpeg")
	os.WriteFile(bin, []byte("#!/bin/sh\nexit 0"), 0o755)
	_, err := Run(context.Background(), Config{
		Mode: types.ModeVOD, Detector: gpu.FakeNVIDIA(),
		FFmpegBin: bin, Logger: logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunDetectorErr(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Config{
		Mode: types.ModeVOD, Detector: gpu.FakeDetector{Err: errors.New("no gpu")},
		Dev: true, Logger: logger.Discard(),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunGPUNoneIsFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "ffmpeg")
	os.WriteFile(bin, []byte(""), 0o755)
	_, err := Run(context.Background(), Config{
		Mode: types.ModeVOD,
		Detector: gpu.FakeDetector{Profile: types.GPUProfile{Vendor: types.GPUVendorNone}},
		FFmpegBin: bin, Logger: logger.Discard(),
	})
	if err == nil || !strings.Contains(err.Error(), "PREFLIGHT_NO_GPU") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunValidations(t *testing.T) {
	t.Parallel()
	if _, err := Run(context.Background(), Config{Mode: types.ModeVOD}); err == nil {
		t.Fatal("expected detector error")
	}
}

func TestRunFFmpegEmpty(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), Config{
		Mode: types.ModeVOD, Detector: gpu.FakeNVIDIA(),
		FFmpegBin: "", Dev: false, Logger: logger.Discard(),
	})
	if err == nil {
		t.Fatal("expected ffmpeg error")
	}
}
