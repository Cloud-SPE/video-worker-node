package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunHelpFlag(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	exit := run(context.Background(), []string{"-h"}, &buf)
	if exit != 2 {
		t.Errorf("exit=%d", exit)
	}
	if !strings.Contains(buf.String(), "mode") {
		t.Errorf("expected --mode in help, got %q", buf.String())
	}
}

func TestRunInvalidConfig(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	exit := run(context.Background(), []string{"--mode=garbage"}, &buf)
	if exit != 2 {
		t.Errorf("exit=%d", exit)
	}
}

func TestRunDevModeBootsAndShutsDown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	presets := filepath.Join(dir, "presets.yaml")
	os.WriteFile(presets, []byte(`presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
`), 0o600)
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	exit := run(ctx, []string{
		"--mode=vod", "--dev",
		"--http-listen=", // disable
		"--presets-file=" + presets,
		"--temp-dir=" + dir,
		"--ffmpeg-bin=ffmpeg",
	}, &buf)
	if exit != 0 {
		t.Errorf("exit=%d output=%s", exit, buf.String())
	}
}

func TestRunDevModeABR(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	presets := filepath.Join(dir, "presets.yaml")
	os.WriteFile(presets, []byte(`presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
`), 0o600)
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	exit := run(ctx, []string{
		"--mode=abr", "--dev",
		"--http-listen=",
		"--presets-file=" + presets,
		"--temp-dir=" + dir,
	}, &buf)
	if exit != 0 {
		t.Errorf("exit=%d output=%s", exit, buf.String())
	}
}

func TestRunDevModeLive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	presets := filepath.Join(dir, "presets.yaml")
	os.WriteFile(presets, []byte(`presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
`), 0o600)
	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	exit := run(ctx, []string{
		"--mode=live", "--dev",
		"--http-listen=",
		"--presets-file=" + presets,
		"--temp-dir=" + dir,
	}, &buf)
	if exit != 0 {
		t.Errorf("exit=%d output=%s", exit, buf.String())
	}
}

func TestRunDevConflict(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	exit := run(context.Background(), []string{
		"--mode=vod", "--dev",
		"--payment-socket=/tmp/p.sock",
	}, &buf)
	if exit != 2 {
		t.Errorf("exit=%d", exit)
	}
}

func TestEnsureDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "a/b/c")
	if err := ensureDir(p); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir(""); err != nil {
		t.Fatal(err)
	}
	if err := ensureDir("."); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRegistry(t *testing.T) {
	t.Parallel()
	r := newPlanRegistry()
	if _, ok := r.Get("x"); ok {
		t.Fatal("ghost should be missing")
	}
}
