package liverunner

import (
	"context"
	"path/filepath"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ffmpeg"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// FFmpegEncoderConfig is the static template that drives every per-session
// LiveSystemEncoder this runner spawns. The factory rewrites LocalDir per
// session so each stream lands in its own scratch directory.
type FFmpegEncoderConfig struct {
	// LocalDirRoot is the parent dir; per-session output goes to
	// `${LocalDirRoot}/${stream_id}`. Required.
	LocalDirRoot string
	// Ladder is the parsed live preset list (e.g., from
	// presets/h264-live.yaml). NVENC-only at MVP.
	Ladder []types.Preset
	// GPU drives codec name selection.
	GPU types.GPUProfile
	// FFmpegBin is the binary path. Default "ffmpeg".
	FFmpegBin string
	// Optional knob overrides; zero values use defaults from BuildLiveArgs.
	SegmentSeconds   int
	PlaylistSize     int
	AudioBitrateKbps int
}

// NewFFmpegEncoderFactory returns an EncoderFactory that produces a
// per-session ffmpeg-backed Encoder. Per-stream LocalDir is computed by
// joining LocalDirRoot with the stream id (so concurrent streams don't
// collide on segment filenames).
func NewFFmpegEncoderFactory(cfg FFmpegEncoderConfig) EncoderFactory {
	return func() Encoder {
		return &ffmpegEncoder{cfg: cfg}
	}
}

type ffmpegEncoder struct {
	cfg FFmpegEncoderConfig
	enc *ffmpeg.LiveSystemEncoder
}

func (e *ffmpegEncoder) Start(ctx context.Context, in EncoderInput) error {
	job := ffmpeg.LiveJob{
		MediaFormat:      in.MediaFormat,
		LocalDir:         filepath.Join(e.cfg.LocalDirRoot, in.StreamID),
		Ladder:           e.cfg.Ladder,
		SegmentSeconds:   e.cfg.SegmentSeconds,
		PlaylistSize:     e.cfg.PlaylistSize,
		AudioBitrateKbps: e.cfg.AudioBitrateKbps,
		GPU:              e.cfg.GPU,
		Bin:              e.cfg.FFmpegBin,
	}
	e.enc = ffmpeg.NewLiveSystemEncoder(job)
	err := e.enc.Start(ctx, ffmpeg.LiveEncoderInput{
		StreamID:    in.StreamID,
		Reader:      in.Reader,
		MediaFormat: in.MediaFormat,
	})
	if err == nil {
		// Graceful EOF — surface ErrEncoderExited so the runner treats
		// this as a clean close rather than a worker error.
		return ErrEncoderExited
	}
	return err
}

func (e *ffmpegEncoder) EncodedSeconds() float64 {
	if e.enc == nil {
		return 0
	}
	return e.enc.EncodedSeconds()
}
