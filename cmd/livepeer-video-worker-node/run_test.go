package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/paymentclient"
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

func TestVerifyPaymentDaemonCatalogIgnoresOrdering(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Capabilities = []config.RegistryCapability{
		{
			Name:     "video:live.rtmp",
			WorkUnit: "video_frame_megapixel",
			Offerings: []config.RegistryOffering{
				{ID: "live-b", PricePerWorkUnitWei: "2"},
				{ID: "live-a", PricePerWorkUnitWei: "1"},
			},
		},
		{
			Name:     "video:transcode.vod",
			WorkUnit: "video_frame_megapixel",
			Offerings: []config.RegistryOffering{
				{ID: "vod-z", PricePerWorkUnitWei: "3"},
			},
		},
	}
	daemon := paymentclient.ListCapabilitiesResult{
		Capabilities: []paymentclient.Capability{
			{
				Capability: "video:transcode.vod",
				WorkUnit:   "video_frame_megapixel",
				Offerings: []paymentclient.OfferingPrice{
					{ID: "vod-z", PricePerWorkUnitWei: "3"},
				},
			},
			{
				Capability: "video:live.rtmp",
				WorkUnit:   "video_frame_megapixel",
				Offerings: []paymentclient.OfferingPrice{
					{ID: "live-a", PricePerWorkUnitWei: "1"},
					{ID: "live-b", PricePerWorkUnitWei: "2"},
				},
			},
		},
	}
	if err := verifyPaymentDaemonCatalog(cfg, daemon); err != nil {
		t.Fatalf("verifyPaymentDaemonCatalog: %v", err)
	}
}

func TestVerifyPaymentDaemonCatalogDetectsMismatch(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	cfg.Capabilities = []config.RegistryCapability{
		{
			Name:     "video:transcode.vod",
			WorkUnit: "video_frame_megapixel",
			Offerings: []config.RegistryOffering{
				{ID: "h264-1080p", PricePerWorkUnitWei: "1250000"},
			},
		},
	}
	daemon := paymentclient.ListCapabilitiesResult{
		Capabilities: []paymentclient.Capability{
			{
				Capability: "video:transcode.vod",
				WorkUnit:   "video_frame_megapixel",
				Offerings: []paymentclient.OfferingPrice{
					{ID: "h264-1080p", PricePerWorkUnitWei: "777"},
				},
			},
		},
	}
	err := verifyPaymentDaemonCatalog(cfg, daemon)
	if err == nil || !strings.Contains(err.Error(), "price worker=") {
		t.Fatalf("err=%v", err)
	}
}
