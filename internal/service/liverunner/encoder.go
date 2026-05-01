package liverunner

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
)

// Encoder is the seam between the live state-machine + payment loop and
// the actual FFmpeg subprocess that produces HLS. Plan §D fills in the
// real implementation (`ffmpeg.LiveEncoder`); this interface is what
// the runner depends on so unit tests can substitute a fake.
type Encoder interface {
	// Start spawns the encoder and begins consuming the input stream.
	// MediaFormat is the FFmpeg `-f` flag value (e.g., "flv" for RTMP).
	// Returns when the encoder has terminated (graceful close, fatal
	// error, or ctx cancellation). Implementations are expected to be
	// long-running.
	Start(ctx context.Context, in EncoderInput) error

	// EncodedSeconds returns how many wall-clock seconds of media the
	// encoder has processed. Monotonically non-decreasing within a
	// single Start() call. Used by the runner's tick loop.
	EncodedSeconds() float64
}

// EncoderInput bundles everything an encoder impl needs from the runner.
type EncoderInput struct {
	StreamID      string
	Reader        io.Reader
	MediaFormat   string // "flv" for RTMP
	Preset        string
	StoragePrefix string // e.g. "live/{stream_id}"
}

// ErrEncoderExited signals graceful encoder termination (broadcaster
// disconnect, EOF, SIGTERM). Treated as a graceful close, not a failure.
var ErrEncoderExited = errors.New("liverunner: encoder exited")

// drainEncoder is a no-op encoder that drains the input until EOF or
// ctx cancellation, ticking EncodedSeconds at a configurable wall-clock
// rate. Used as the default at §C — the real FFmpeg encoder lands in §D.
type drainEncoder struct {
	wallClockTickHz uint64
	processed       atomic.Uint64 // tenths of a second
}

// NewDrainEncoder returns an Encoder that simply consumes its input and
// reports wall-clock time as EncodedSeconds. Used as a placeholder until
// §D's real FFmpeg encoder ships and as a test fake.
func NewDrainEncoder() Encoder {
	return &drainEncoder{}
}

func (d *drainEncoder) Start(ctx context.Context, in EncoderInput) error {
	if in.Reader == nil {
		return errors.New("drainEncoder: nil reader")
	}
	// Drain in a goroutine so we can also tick wall-clock seconds.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, in.Reader)
		done <- err
	}()
	// Tick every 100ms; bump processed by 100ms units so EncodedSeconds
	// approximates wall clock. The runner can substitute a clock fake
	// for deterministic tests via TickEncodedSeconds().
	ticker := newTicker()
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
				return ErrEncoderExited
			}
			return err
		case <-ticker.C():
			d.processed.Add(1)
		}
	}
}

func (d *drainEncoder) EncodedSeconds() float64 {
	return float64(d.processed.Load()) / 10.0
}

// TickEncodedSeconds is a test helper that advances the drain encoder's
// reported time without waiting for wall clock.
func TickEncodedSeconds(e Encoder, seconds float64) {
	if d, ok := e.(*drainEncoder); ok {
		d.processed.Add(uint64(seconds * 10))
	}
}
