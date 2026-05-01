// Package config holds the daemon's validated configuration.
//
// One Config covers all three modes; mode-specific knobs are present on
// every Config but only consulted when the mode demands them.
package config

import (
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// CurrentProtocolVersion is the shared worker.yaml schema version
// accepted by this worker build.
const CurrentProtocolVersion int32 = 1

// CurrentAPIVersion is the HTTP surface version advertised on /health.
const CurrentAPIVersion int32 = 1

// RegistryCapability is one capability row projected from worker.yaml
// for /registry/offerings.
type RegistryCapability struct {
	Name      string
	WorkUnit  string
	Extra     map[string]any
	Offerings []RegistryOffering
}

// RegistryOffering is one offering row under a capability.
type RegistryOffering struct {
	ID                  string
	PricePerWorkUnitWei string
	BackendURL          string
	Constraints         map[string]any
}

// Clone returns a deep-enough copy for safe read-only projection.
func (c RegistryCapability) Clone() RegistryCapability {
	out := RegistryCapability{
		Name:      c.Name,
		WorkUnit:  c.WorkUnit,
		Extra:     maps.Clone(c.Extra),
		Offerings: make([]RegistryOffering, 0, len(c.Offerings)),
	}
	for _, offering := range c.Offerings {
		out.Offerings = append(out.Offerings, RegistryOffering{
			ID:                  offering.ID,
			PricePerWorkUnitWei: offering.PricePerWorkUnitWei,
			BackendURL:          offering.BackendURL,
			Constraints:         maps.Clone(offering.Constraints),
		})
	}
	return out
}

// Config is the validated daemon config.
type Config struct {
	// Identity / mode
	Mode    types.Mode
	Version string
	Dev     bool
	NodeID  string

	// Shared worker contract
	ProtocolVersion  int32
	APIVersion       int32
	WorkerEthAddress string
	Capabilities     []RegistryCapability

	// FFmpeg + GPU
	FFmpegBin      string
	GPUVendor      types.GPUVendor
	GPUDevice      string // override for /dev/dri/renderD128 etc.
	IgnoreGPULimit bool

	// Job pool
	MaxQueueSize int
	TempDir      string
	JobTTL       time.Duration

	// Presets
	PresetsFile string

	// Surfaces
	HTTPListen       string
	GRPCSocket       string
	MetricsListen    string
	MetricsMaxSeries int
	StorePath        string

	// Sibling daemons
	PaymentSocket   string
	RegistrySocket  string
	RegistryRefresh time.Duration

	// Webhook
	WebhookRetryBackoffs []time.Duration

	// Public TCP advertised URL (for capability registration)
	PublicURL string

	// Streaming session knobs (Live mode)
	DebitCadence            time.Duration
	StreamPreCreditSeconds  int
	StreamRunwaySeconds     int
	StreamGraceSeconds      int
	StreamDebitRetryBackoff time.Duration
	StreamRestartLimit      int
	StreamTopupMinInterval  time.Duration

	// Worker → shell internal callback API (Live mode).
	ShellInternalURL    string // e.g., "http://api:8080"
	ShellInternalSecret string // matches WORKER_SHELL_SECRET in the shell

	// Live ingest (RTMP).
	IngestRTMPListen        string // e.g., ":1935"
	IngestRTMPMaxConcurrent int    // 0 = unlimited
	LivePreset              string // e.g., "h264-live"
	LiveStoragePrefix       string // template; {stream_id} substituted

	// Auth (optional bearer token tier on top of payment)
	AuthToken string

	// Pricing (per work unit, decimal wei string)
	PriceWeiPerUnit string

	// Resource limits applied to FFmpeg via prlimit (0 = unlimited).
	FFmpegMaxCPUSeconds uint64
	FFmpegMaxRSSBytes   uint64
	FFmpegMaxOpenFDs    uint64

	// Cancellation grace
	CancelGrace time.Duration

	// Job-log capture cap.
	MaxJobLogBytes int
}

// Default returns a Config with safe production defaults. Operators
// override via flags (`cmd/.../run.go`).
func Default() Config {
	return Config{
		Mode:                    types.ModeVOD,
		Version:                 "dev",
		Dev:                     false,
		NodeID:                  "transcode-worker-node",
		ProtocolVersion:         CurrentProtocolVersion,
		APIVersion:              CurrentAPIVersion,
		FFmpegBin:               "/usr/local/bin/ffmpeg",
		GPUVendor:               types.GPUVendorAuto,
		MaxQueueSize:            5,
		TempDir:                 "/tmp/livepeer-transcode",
		JobTTL:                  24 * time.Hour,
		PresetsFile:             "presets/h264-streaming.yaml",
		HTTPListen:              ":8080",
		GRPCSocket:              "",
		MetricsListen:           "",
		MetricsMaxSeries:        10000,
		StorePath:               "",
		PaymentSocket:           "",
		RegistrySocket:          "",
		RegistryRefresh:         15 * time.Minute,
		WebhookRetryBackoffs:    []time.Duration{1 * time.Second, 5 * time.Second, 25 * time.Second},
		PublicURL:               "",
		DebitCadence:            5 * time.Second,
		StreamPreCreditSeconds:  1,
		StreamRunwaySeconds:     30,
		StreamGraceSeconds:      60,
		StreamDebitRetryBackoff: 1 * time.Second,
		StreamRestartLimit:      3,
		StreamTopupMinInterval:  5 * time.Second,
		ShellInternalURL:        "",
		ShellInternalSecret:     "",
		IngestRTMPListen:        ":1935",
		IngestRTMPMaxConcurrent: 4,
		LivePreset:              "h264-live",
		LiveStoragePrefix:       "live/{stream_id}",
		PriceWeiPerUnit:         "0",
		FFmpegMaxCPUSeconds:     0,
		FFmpegMaxRSSBytes:       0,
		FFmpegMaxOpenFDs:        0,
		CancelGrace:             5 * time.Second,
		MaxJobLogBytes:          128 * 1024,
	}
}

// Validate runs every cross-field check. Returns the first error.
func (c Config) Validate() error {
	if err := c.Mode.Validate(); err != nil {
		return fmt.Errorf("mode: %w", err)
	}
	if err := c.GPUVendor.Validate(); err != nil {
		return fmt.Errorf("gpu_vendor: %w", err)
	}
	if c.FFmpegBin == "" {
		return errors.New("ffmpeg_bin must be set")
	}
	if c.MaxQueueSize <= 0 {
		return errors.New("max_queue_size must be > 0")
	}
	if c.TempDir == "" {
		return errors.New("temp_dir must be set")
	}
	if c.HTTPListen == "" && !c.Dev {
		return errors.New("http_listen must be set in non-dev mode")
	}
	if c.PresetsFile == "" {
		return errors.New("presets_file must be set")
	}
	// Streaming-session knobs
	if c.Mode.IsLive() {
		if c.DebitCadence < 1*time.Second {
			return errors.New("debit_cadence must be >= 1s (gRPC capacity protection)")
		}
		if c.StreamPreCreditSeconds <= 0 {
			return errors.New("stream_pre_credit_seconds must be > 0")
		}
		if c.StreamRunwaySeconds <= 0 {
			return errors.New("stream_runway_seconds must be > 0")
		}
		if c.StreamGraceSeconds <= 0 {
			return errors.New("stream_grace_seconds must be > 0")
		}
		if c.StreamRestartLimit <= 0 {
			return errors.New("stream_restart_limit must be > 0")
		}
	}
	// Catch operator misconfigs early per plan 0007 §I.
	if c.Dev && c.PaymentSocket != "" {
		return fmt.Errorf("%s: --dev set but --payment-socket-path also configured", types.ErrCodePreflightDevConflict)
	}
	return nil
}
