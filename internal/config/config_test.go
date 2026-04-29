package config

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func TestDefaultIsValidVOD(t *testing.T) {
	t.Parallel()
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestValidateLiveExtras(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Mode = types.ModeLive
	if err := c.Validate(); err != nil {
		t.Fatalf("default Live config should be valid: %v", err)
	}
	c.DebitCadence = 100 * time.Millisecond
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "debit_cadence") {
		t.Fatalf("expected debit_cadence error, got %v", err)
	}
	c = Default()
	c.Mode = types.ModeLive
	c.StreamPreCreditSeconds = 0
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "pre_credit") {
		t.Fatalf("expected pre_credit error, got %v", err)
	}
	c = Default()
	c.Mode = types.ModeLive
	c.StreamRunwaySeconds = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected runway error")
	}
	c = Default()
	c.Mode = types.ModeLive
	c.StreamGraceSeconds = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected grace error")
	}
	c = Default()
	c.Mode = types.ModeLive
	c.StreamRestartLimit = 0
	if err := c.Validate(); err == nil {
		t.Fatal("expected restart limit error")
	}
}

func TestValidateRequiredFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mutate  func(*Config)
		wantErr string
	}{
		{func(c *Config) { c.Mode = "wat" }, "mode"},
		{func(c *Config) { c.GPUVendor = "wat" }, "gpu_vendor"},
		{func(c *Config) { c.FFmpegBin = "" }, "ffmpeg_bin"},
		{func(c *Config) { c.MaxQueueSize = 0 }, "max_queue_size"},
		{func(c *Config) { c.TempDir = "" }, "temp_dir"},
		{func(c *Config) { c.HTTPListen = "" }, "http_listen"},
		{func(c *Config) { c.PresetsFile = "" }, "presets_file"},
	}
	for i, tc := range cases {
		c := Default()
		tc.mutate(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("case %d: err=%v want substring %q", i, err, tc.wantErr)
		}
	}
}

func TestDevConflict(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Dev = true
	c.PaymentSocket = "/tmp/payment.sock"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "PREFLIGHT_DEV_CONFLICT") {
		t.Fatalf("err=%v want dev conflict", err)
	}
}

func TestDevAllowsEmptyHTTP(t *testing.T) {
	t.Parallel()
	c := Default()
	c.Dev = true
	c.HTTPListen = ""
	if err := c.Validate(); err != nil {
		t.Fatalf("dev with empty http should pass: %v", err)
	}
}
