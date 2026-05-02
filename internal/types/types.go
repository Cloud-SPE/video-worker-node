// Package types holds the pure data types shared across the daemon: Mode,
// Job, Phase, Preset, GPUProfile, Capability, error codes. No imports of
// any other internal package — types/ is the leaf.
package types

import (
	"errors"
	"fmt"
	"time"
)

// Mode controls which job runner is wired up. Mode-specific RPCs return
// Unimplemented when called on the wrong mode.
type Mode string

const (
	// ModeVOD: single-shot transcode of a finite input to a single output.
	ModeVOD Mode = "vod"
	// ModeABR: ladder of renditions from a single input + master HLS manifest.
	ModeABR Mode = "abr"
	// ModeLive: long-lived FFmpeg subprocess feeding Trickle channels.
	ModeLive Mode = "live"
)

// String renders the mode for logs / metrics labels.
func (m Mode) String() string { return string(m) }

// Validate returns nil iff m is one of the three known modes.
func (m Mode) Validate() error {
	switch m {
	case ModeVOD, ModeABR, ModeLive:
		return nil
	default:
		return fmt.Errorf("unknown mode %q (want vod|abr|live)", m)
	}
}

// IsVOD reports whether this is VOD mode.
func (m Mode) IsVOD() bool { return m == ModeVOD }

// IsABR reports whether this is ABR mode.
func (m Mode) IsABR() bool { return m == ModeABR }

// IsLive reports whether this is Live mode.
func (m Mode) IsLive() bool { return m == ModeLive }

// JobPhase is the durable lifecycle state of a job.
type JobPhase string

const (
	PhaseQueued      JobPhase = "queued"
	PhaseDownloading JobPhase = "downloading"
	PhaseProbing     JobPhase = "probing"
	PhaseEncoding    JobPhase = "encoding"
	PhaseUploading   JobPhase = "uploading"
	PhaseComplete    JobPhase = "complete"
	PhaseError       JobPhase = "error"
)

// IsTerminal reports whether the phase is a final state.
func (p JobPhase) IsTerminal() bool {
	return p == PhaseComplete || p == PhaseError
}

// PhaseTiming records when a phase started and ended. End is the zero time
// while the phase is still active.
type PhaseTiming struct {
	Phase JobPhase  `json:"phase"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitempty"`
}

// Job is the durable record. Each non-terminal job has exactly one entry
// in the BoltDB store; on daemon restart, `repo/jobs.Resume()` walks them
// and re-runs from the last persisted phase.
type Job struct {
	ID            string        `json:"id"`
	Mode          Mode          `json:"mode"`
	Phase         JobPhase      `json:"phase"`
	Progress      float64       `json:"progress"` // 0..1
	InputURL      string        `json:"input_url"`
	OutputURL     string        `json:"output_url"`
	Preset        string        `json:"preset"`
	WebhookURL    string        `json:"webhook_url,omitempty"`
	WebhookSecret string        `json:"-"` // never serialized — secret
	WorkID        string        `json:"work_id"`
	Sender        []byte        `json:"sender,omitempty"` // ETH address bytes
	PriceWei      string        `json:"price_wei,omitempty"`
	UnitsPer      int64         `json:"units_per_segment"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
	Phases        []PhaseTiming `json:"phases,omitempty"`
	Logs          []string      `json:"logs,omitempty"`
	ErrorCode     string        `json:"error_code,omitempty"`
	ErrorMessage  string        `json:"error_message,omitempty"`
}

// Capability is a registry-facing capability advertisement.
type Capability struct {
	Name     string  `json:"name"`
	WorkUnit string  `json:"work_unit"`
	Models   []Model `json:"models,omitempty"`
	Capacity int32   `json:"capacity"`
	PriceWei string  `json:"price_wei,omitempty"`
}

// Model is a single supported model under a capability.
type Model struct {
	ID              string `json:"id"`
	PriceWeiPerUnit string `json:"price_wei_per_unit"`
	Warm            bool   `json:"warm"`
}

// GPUVendor identifies the host's installed GPU vendor.
type GPUVendor string

const (
	GPUVendorAuto   GPUVendor = "auto"
	GPUVendorNVIDIA GPUVendor = "nvidia"
	GPUVendorIntel  GPUVendor = "intel"
	GPUVendorAMD    GPUVendor = "amd"
	GPUVendorNone   GPUVendor = "none"
)

// String renders the vendor. Used as a metric label and in logs.
func (v GPUVendor) String() string { return string(v) }

// Validate accepts auto, nvidia, intel, amd, or none.
func (v GPUVendor) Validate() error {
	switch v {
	case GPUVendorAuto, GPUVendorNVIDIA, GPUVendorIntel, GPUVendorAMD, GPUVendorNone:
		return nil
	default:
		return fmt.Errorf("unknown gpu vendor %q (want auto|nvidia|intel|amd|none)", v)
	}
}

// GPUProfile is the detected hardware capabilities for one GPU. Filled in
// by `internal/providers/gpu/` at startup and exposed via the Health RPC.
type GPUProfile struct {
	Vendor       GPUVendor `json:"vendor"`
	Model        string    `json:"model,omitempty"`
	DriverVer    string    `json:"driver_version,omitempty"`
	VRAMBytes    uint64    `json:"vram_bytes,omitempty"`
	MaxSessions  int32     `json:"max_sessions,omitempty"`
	SupportsH264 bool      `json:"supports_h264"`
	SupportsHEVC bool      `json:"supports_hevc"`
	SupportsAV1  bool      `json:"supports_av1"`
	DetectedAt   time.Time `json:"detected_at"`
}

// Preset describes a single encoding configuration. Loaded from YAML.
type Preset struct {
	Name        string         `yaml:"name" json:"name"`
	Codec       string         `yaml:"codec" json:"codec"` // "h264", "hevc", "av1"
	WidthMax    int            `yaml:"width_max" json:"width_max"`
	HeightMax   int            `yaml:"height_max" json:"height_max"`
	BitrateKbps int            `yaml:"bitrate_kbps" json:"bitrate_kbps"`
	Profile     string         `yaml:"profile,omitempty" json:"profile,omitempty"`
	GOPSeconds  float64        `yaml:"gop_seconds,omitempty" json:"gop_seconds,omitempty"`
	Extra       map[string]any `yaml:"extra,omitempty" json:"extra,omitempty"`
}

// Validate verifies that mandatory fields are present.
func (p Preset) Validate() error {
	if p.Name == "" {
		return errors.New("preset name is empty")
	}
	switch p.Codec {
	case "h264", "hevc", "av1":
	default:
		return fmt.Errorf("preset %q: unknown codec %q (want h264|hevc|av1)", p.Name, p.Codec)
	}
	if p.WidthMax <= 0 || p.HeightMax <= 0 {
		return fmt.Errorf("preset %q: width_max/height_max must be > 0", p.Name)
	}
	if p.BitrateKbps <= 0 {
		return fmt.Errorf("preset %q: bitrate_kbps must be > 0", p.Name)
	}
	return nil
}

// SupportedBy returns true iff the GPU has the codec engine for this preset.
func (p Preset) SupportedBy(g GPUProfile) bool {
	switch p.Codec {
	case "h264":
		return g.SupportsH264
	case "hevc":
		return g.SupportsHEVC
	case "av1":
		return g.SupportsAV1
	}
	return false
}

// PresetCatalogue is a parsed preset YAML file.
type PresetCatalogue struct {
	Presets []Preset `yaml:"presets" json:"presets"`
}

// Lookup returns the preset by name; ok==false if missing.
func (c PresetCatalogue) Lookup(name string) (Preset, bool) {
	for _, p := range c.Presets {
		if p.Name == name {
			return p, true
		}
	}
	return Preset{}, false
}

// Filter returns presets that the given GPU supports.
func (c PresetCatalogue) Filter(g GPUProfile) []Preset {
	out := make([]Preset, 0, len(c.Presets))
	for _, p := range c.Presets {
		if p.SupportedBy(g) {
			out = append(out, p)
		}
	}
	return out
}

// JobError is an error with a structured code, suitable for both logs and
// the public HTTP response.
type JobError struct {
	Code     string
	Message  string
	ExitCode int
	Stderr   string
}

// Error renders the JobError; satisfies error.
func (e *JobError) Error() string {
	if e.ExitCode != 0 {
		return fmt.Sprintf("%s: %s (exit %d)", e.Code, e.Message, e.ExitCode)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Error codes used across the daemon. Use these as `error_code` values in
// structured logs and as `code` fields in HTTP error bodies.
const (
	ErrCodePreflightNoGPU         = "PREFLIGHT_NO_GPU"
	ErrCodePreflightFFmpegMissing = "PREFLIGHT_FFMPEG_MISSING"
	ErrCodePreflightDevConflict   = "PREFLIGHT_DEV_CONFLICT"
	ErrCodePreflightConfigInvalid = "PREFLIGHT_CONFIG_INVALID"
	ErrCodeInvalidPayment         = "INVALID_PAYMENT"
	ErrCodeInsufficientBalance    = "INSUFFICIENT_BALANCE"
	ErrCodeJobNotFound            = "JOB_NOT_FOUND"
	ErrCodeJobInvalidPreset       = "JOB_INVALID_PRESET"
	ErrCodeDownloadFailed         = "DOWNLOAD_FAILED"
	ErrCodeProbeFailed            = "PROBE_FAILED"
	ErrCodeEncodingFailed         = "ENCODING_FAILED"
	ErrCodeUploadFailed           = "UPLOAD_FAILED"
	ErrCodePaymentFailed          = "PAYMENT_FAILED"
	ErrCodeWebhookFailed          = "WEBHOOK_FAILED"
	ErrCodeStreamEncoderFailed    = "STREAM_ENCODER_FAILED"
	ErrCodeBalanceExhausted       = "BALANCE_EXHAUSTED"
	ErrCodePaymentUnreachable     = "PAYMENT_UNREACHABLE"
	ErrCodeStreamNotFound         = "STREAM_NOT_FOUND"
	ErrCodeTopupRateLimited       = "TOPUP_RATE_LIMITED"
)

// StreamPhase is the durable lifecycle state of a live stream.
type StreamPhase string

const (
	StreamPhaseStarting         StreamPhase = "starting"
	StreamPhaseStreaming        StreamPhase = "streaming"
	StreamPhaseLowBalance       StreamPhase = "low_balance"
	StreamPhasePaymentLost      StreamPhase = "payment_unreachable"
	StreamPhaseClosing          StreamPhase = "closing"
	StreamPhaseClosed           StreamPhase = "closed"
	StreamPhaseEncoderFailed    StreamPhase = "encoder_failed"
	StreamPhaseBalanceExhausted StreamPhase = "balance_exhausted"
)

// IsTerminal reports whether the phase is final.
func (p StreamPhase) IsTerminal() bool {
	switch p {
	case StreamPhaseClosed, StreamPhaseEncoderFailed, StreamPhaseBalanceExhausted:
		return true
	}
	return false
}

// Stream is the durable record of one live streaming session.
type Stream struct {
	WorkID           string      `json:"work_id"`
	GatewaySessionID string      `json:"gateway_session_id,omitempty"`
	WorkerSessionID  string      `json:"worker_session_id,omitempty"`
	PaymentWorkID    string      `json:"payment_work_id,omitempty"`
	Sender           []byte      `json:"sender,omitempty"`
	Phase            StreamPhase `json:"phase"`
	SubscribeURL     string      `json:"subscribe_url"`
	PublishURL       string      `json:"publish_url"`
	Preset           string      `json:"preset"`
	StartedAt        time.Time   `json:"started_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
	ClosedAt         time.Time   `json:"closed_at,omitempty"`
	DebitSeq         uint64      `json:"debit_seq"`
	UnitsDebited     int64       `json:"units_debited"`
	WebhookURL       string      `json:"webhook_url,omitempty"`
	WebhookSecret    string      `json:"-"`
	LastTopupAt      time.Time   `json:"last_topup_at,omitempty"`
	LowBalance       bool        `json:"low_balance"`
	GraceUntil       time.Time   `json:"grace_until,omitempty"`
	CloseReason      string      `json:"close_reason,omitempty"`
	ErrorCode        string      `json:"error_code,omitempty"`
}

// CapacityReport summarizes the worker's current load.
type CapacityReport struct {
	Mode          Mode       `json:"mode"`
	GPU           GPUProfile `json:"gpu"`
	MaxQueueSize  int        `json:"max_queue_size"`
	ActiveJobs    int        `json:"active_jobs"`
	QueuedJobs    int        `json:"queued_jobs"`
	ActiveStreams int        `json:"active_streams"`
}
