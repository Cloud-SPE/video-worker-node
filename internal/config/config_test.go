package config

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestLoadSharedWorker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yaml")
	body := []byte(`
protocol_version: 1
worker_eth_address: "0x1234567890abcdef1234567890abcdef12345678"
auth_token: "orch-token"
payment_daemon:
  recipient_eth_address: "0x1234567890abcdef1234567890abcdef12345678"
  broker:
    mode: fake
    fake_sender_balances_wei:
      "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "1000000000000000000"
worker:
  http_listen: ":8081"
  payment_daemon_socket: "/var/run/payment.sock"
capabilities:
  - capability: "video:transcode.vod"
    work_unit: video_frame_megapixel
    offerings:
      - id: "h264-1080p"
        price_per_work_unit_wei: "1250000"
        backend_url: "http://127.0.0.1:9000"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write worker.yaml: %v", err)
	}
	cfg, err := LoadSharedWorker(path)
	if err != nil {
		t.Fatalf("LoadSharedWorker: %v", err)
	}
	if cfg.ProtocolVersion != CurrentProtocolVersion {
		t.Fatalf("protocol_version=%d", cfg.ProtocolVersion)
	}
	if cfg.APIVersion != CurrentAPIVersion {
		t.Fatalf("api_version=%d", cfg.APIVersion)
	}
	if cfg.AuthToken != "orch-token" {
		t.Fatalf("auth_token=%q", cfg.AuthToken)
	}
	if cfg.Worker.PaymentDaemonSocket != "/var/run/payment.sock" {
		t.Fatalf("payment socket=%q", cfg.Worker.PaymentDaemonSocket)
	}
	if len(cfg.Capabilities) != 1 {
		t.Fatalf("capabilities=%d", len(cfg.Capabilities))
	}
	if cfg.Capabilities[0].Offerings[0].BackendURL != "http://127.0.0.1:9000" {
		t.Fatalf("backend_url=%q", cfg.Capabilities[0].Offerings[0].BackendURL)
	}
}

func TestLoadSharedWorkerRejectsLegacyCapabilityName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yaml")
	body := []byte(`
protocol_version: 1
payment_daemon:
  recipient_eth_address: "0x1234567890abcdef1234567890abcdef12345678"
worker:
  http_listen: ":8081"
  payment_daemon_socket: "/var/run/payment.sock"
capabilities:
  - capability: "video.transcode.vod"
    work_unit: video_frame_megapixel
    offerings:
      - id: "h264-1080p"
        price_per_work_unit_wei: "1250000"
        backend_url: "http://127.0.0.1:9000"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write worker.yaml: %v", err)
	}
	_, err := LoadSharedWorker(path)
	if err == nil || !strings.Contains(err.Error(), "must match ^video:") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadSharedWorkerRejectsServiceRegistryPublisher(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yaml")
	body := []byte(`
protocol_version: 1
payment_daemon:
  recipient_eth_address: "0x1234567890abcdef1234567890abcdef12345678"
worker:
  http_listen: ":8081"
  payment_daemon_socket: "/var/run/payment.sock"
capabilities:
  - capability: "video:transcode.vod"
    work_unit: video_frame_megapixel
    offerings:
      - id: "h264-1080p"
        price_per_work_unit_wei: "1250000"
        backend_url: "http://127.0.0.1:9000"
service_registry_publisher:
  enabled: true
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write worker.yaml: %v", err)
	}
	_, err := LoadSharedWorker(path)
	if err == nil || !strings.Contains(err.Error(), "service_registry_publisher") {
		t.Fatalf("err=%v", err)
	}
}

func TestLoadSharedWorkerRejectsUnsupportedProtocolVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "worker.yaml")
	body := []byte(`
protocol_version: 2
payment_daemon:
  recipient_eth_address: "0x1234567890abcdef1234567890abcdef12345678"
worker:
  http_listen: ":8081"
  payment_daemon_socket: "/var/run/payment.sock"
capabilities:
  - capability: "video:transcode.vod"
    work_unit: video_frame_megapixel
    offerings:
      - id: "h264-1080p"
        price_per_work_unit_wei: "1250000"
        backend_url: "http://127.0.0.1:9000"
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write worker.yaml: %v", err)
	}
	_, err := LoadSharedWorker(path)
	if err == nil || !strings.Contains(err.Error(), "protocol_version=2") {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckBearerToken(t *testing.T) {
	t.Parallel()
	if !CheckBearerToken("Bearer secret", "secret") {
		t.Fatal("expected matching bearer token")
	}
	if CheckBearerToken("Bearer wrong", "secret") {
		t.Fatal("expected mismatch for wrong token")
	}
	if CheckBearerToken("secret", "secret") {
		t.Fatal("expected mismatch for missing bearer prefix")
	}
}

func TestParseReaderRejectsSecondDocument(t *testing.T) {
	t.Parallel()
	_, err := parseReader(bytes.NewBufferString(`
protocol_version: 1
payment_daemon: {}
worker:
  http_listen: ":8081"
  payment_daemon_socket: "/var/run/payment.sock"
capabilities:
  - capability: "video:transcode.vod"
    work_unit: video_frame_megapixel
    offerings:
      - id: "h264-1080p"
        price_per_work_unit_wei: "1250000"
        backend_url: "http://127.0.0.1:9000"
---
protocol_version: 1
`))
	if err == nil || !strings.Contains(err.Error(), "unexpected second YAML document") {
		t.Fatalf("err=%v", err)
	}
}

func TestRegistryCapabilityCloneCopiesNestedMaps(t *testing.T) {
	t.Parallel()
	original := RegistryCapability{
		Name:     "video:transcode.vod",
		WorkUnit: "video_frame_megapixel",
		Extra:    map[string]any{"vendor": "nvenc"},
		Offerings: []RegistryOffering{
			{
				ID:                  "h264-1080p",
				PricePerWorkUnitWei: "1250000",
				BackendURL:          "http://127.0.0.1:9000",
				Constraints:         map[string]any{"preset": "h264-1080p"},
			},
		},
	}
	cloned := original.Clone()
	cloned.Extra["vendor"] = "qsv"
	cloned.Offerings[0].Constraints["preset"] = "hevc-1080p"
	if original.Extra["vendor"] != "nvenc" {
		t.Fatalf("original extra mutated: %v", original.Extra)
	}
	if original.Offerings[0].Constraints["preset"] != "h264-1080p" {
		t.Fatalf("original constraints mutated: %v", original.Offerings[0].Constraints)
	}
}
