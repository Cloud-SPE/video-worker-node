package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// LiveJob describes one long-running live-encode subprocess.
//
// Layout: a single FFmpeg child reads FLV from stdin and writes a per-
// rendition HLS playlist + segments under
//
//	{LocalDir}/h264/{rendition.Name}/playlist.m3u8
//	{LocalDir}/h264/{rendition.Name}/segment_NNNNN.ts
//
// The audio track is encoded once + adaptation-set-shared across
// renditions via FFmpeg's `-map 0:a` (one AAC encode, mapped to all
// variant playlists).
type LiveJob struct {
	// MediaFormat is the FFmpeg `-f` flag for the input (e.g., "flv").
	MediaFormat string
	// LocalDir is the on-disk output directory; the encoder writes
	// `{LocalDir}/h264/{rendition.Name}/...`.
	LocalDir string
	// Ladder is the per-rendition spec set. NVENC-only at MVP.
	Ladder []types.Preset
	// SegmentSeconds is the target segment length. Plan §D default: 4.
	SegmentSeconds int
	// PlaylistSize is the rolling DVR window (segments). Default: 6.
	PlaylistSize int
	// AudioBitrateKbps. Default: 128.
	AudioBitrateKbps int
	// GPU drives codec name selection (h264_nvenc, etc.).
	GPU types.GPUProfile
	// MaxLogBytes caps captured stderr.
	MaxLogBytes int
	// Bin overrides the ffmpeg binary path. Default: "ffmpeg".
	Bin string
	// CancelGrace is how long to wait after SIGTERM before SIGKILL.
	CancelGrace time.Duration
}

// BuildLiveArgs assembles the FFmpeg argv for a live multi-rendition
// encode. Pure function (unit-testable, no subprocess). The encoder
// reads FLV from stdin (`-i pipe:0`) and writes one HLS variant per
// ladder entry.
func BuildLiveArgs(j LiveJob) []string {
	if j.SegmentSeconds <= 0 {
		j.SegmentSeconds = 4
	}
	if j.PlaylistSize <= 0 {
		j.PlaylistSize = 6
	}
	if j.AudioBitrateKbps <= 0 {
		j.AudioBitrateKbps = 128
	}
	if j.MediaFormat == "" {
		j.MediaFormat = "flv"
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		// Read at native rate; live ingest already paced. Helps when
		// ingest is faster than wall clock (rare but possible).
		"-re",
		"-f", j.MediaFormat,
		"-i", "pipe:0",
	}

	// One audio encode, mapped to every variant playlist via map_metadata.
	for _, rung := range j.Ladder {
		args = append(args,
			"-map", "0:v:0",
			"-map", "0:a:0",
			"-c:v", codecFlag(rung.Codec, j.GPU.Vendor),
			"-preset", "llhq", // low-latency-high-quality (NVENC). Software fallback ignores.
			"-tune", "ll",
			"-s", fmt.Sprintf("%dx%d", rung.WidthMax, rung.HeightMax),
			"-b:v", fmt.Sprintf("%dk", rung.BitrateKbps),
			"-maxrate", fmt.Sprintf("%dk", rung.BitrateKbps),
			"-bufsize", fmt.Sprintf("%dk", rung.BitrateKbps*2),
			"-g", strconv.Itoa(j.SegmentSeconds*30), // GOP = segment_secs * fps (assume 30)
			"-keyint_min", strconv.Itoa(j.SegmentSeconds*30),
			"-sc_threshold", "0",
			"-c:a", "aac",
			"-b:a", fmt.Sprintf("%dk", j.AudioBitrateKbps),
			"-f", "hls",
			"-hls_time", strconv.Itoa(j.SegmentSeconds),
			"-hls_list_size", strconv.Itoa(j.PlaylistSize),
			"-hls_flags", "delete_segments+append_list+omit_endlist+independent_segments",
			"-hls_segment_type", "mpegts",
			"-hls_segment_filename", filepath.Join(j.LocalDir, rung.Codec, rung.Name, "segment_%05d.ts"),
			filepath.Join(j.LocalDir, rung.Codec, rung.Name, "playlist.m3u8"),
		)
	}
	return args
}

// LiveEncoder is the runner-facing surface. It satisfies
// liverunner.Encoder (matches by structural signature — Go interfaces
// don't need an explicit declaration here, the liverunner package owns
// the contract).
type LiveEncoder interface {
	Start(ctx context.Context, in LiveEncoderInput) error
	EncodedSeconds() float64
}

// LiveEncoderInput is what the runner hands the encoder per session.
type LiveEncoderInput struct {
	StreamID    string
	Reader      io.Reader
	MediaFormat string
}

// LiveSystemEncoder spawns the actual FFmpeg subprocess.
type LiveSystemEncoder struct {
	Job LiveJob

	processed atomic.Int64 // tenths of a second
}

// NewLiveSystemEncoder constructs an encoder bound to a static LiveJob.
// The runner re-creates one per session (via EncoderFactory) so each
// session gets its own LocalDir.
func NewLiveSystemEncoder(j LiveJob) *LiveSystemEncoder {
	return &LiveSystemEncoder{Job: j}
}

// Start runs ffmpeg until the input pipe closes (graceful) or ctx is
// cancelled. Returns nil on EOF and a wrapped error on subprocess failure.
func (e *LiveSystemEncoder) Start(ctx context.Context, in LiveEncoderInput) error {
	if in.Reader == nil {
		return errors.New("ffmpeg.LiveSystemEncoder: nil reader")
	}
	if e.Job.LocalDir == "" {
		return errors.New("ffmpeg.LiveSystemEncoder: empty LocalDir")
	}
	if e.Job.MediaFormat == "" {
		e.Job.MediaFormat = in.MediaFormat
	}

	bin := e.Job.Bin
	if bin == "" {
		bin = "ffmpeg"
	}
	grace := e.Job.CancelGrace
	if grace == 0 {
		grace = 5 * time.Second
	}
	args := BuildLiveArgs(e.Job)
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Stdin = in.Reader

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg.LiveSystemEncoder: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg.LiveSystemEncoder: start: %w", err)
	}

	maxLog := e.Job.MaxLogBytes
	if maxLog <= 0 {
		maxLog = 128 * 1024
	}
	stderrBuf := newRingBuffer(maxLog)

	var wg sync.WaitGroup
	wg.Add(1)
	// Parse `out_time_us=` from stderr to advance EncodedSeconds. FFmpeg
	// emits this when invoked with `-progress pipe:2`, but we keep the
	// stderr path here too so non-progress builds still work.
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			_, _ = stderrBuf.Write(append(line, '\n'))
			parseLiveProgress(string(line), &e.processed)
		}
	}()

	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	var waitErr error
	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case waitErr = <-doneCh:
		case <-time.After(grace):
			_ = cmd.Process.Kill()
			waitErr = <-doneCh
		}
	case waitErr = <-doneCh:
	}
	wg.Wait()

	if waitErr == nil {
		return nil
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		return &types.JobError{
			Code:     types.ErrCodeEncodingFailed,
			Message:  "live ffmpeg exited non-zero",
			ExitCode: exitErr.ExitCode(),
			Stderr:   stderrBuf.String(),
		}
	}
	return waitErr
}

// EncodedSeconds returns the running total of media seconds processed
// (parsed from FFmpeg's stderr `time=HH:MM:SS.MS` lines).
func (e *LiveSystemEncoder) EncodedSeconds() float64 {
	return float64(e.processed.Load()) / 10.0
}

// parseLiveProgress scans an FFmpeg stderr line for `time=HH:MM:SS.MS`
// and updates `processed` (tenths of seconds) if found. We only ever
// monotonically increase to handle out-of-order stderr.
func parseLiveProgress(line string, processed *atomic.Int64) {
	idx := strings.Index(line, "time=")
	if idx < 0 {
		return
	}
	rest := line[idx+len("time="):]
	end := strings.IndexAny(rest, " \t")
	if end > 0 {
		rest = rest[:end]
	}
	parts := strings.Split(rest, ":")
	if len(parts) != 3 {
		return
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil {
		return
	}
	s, errS := strconv.ParseFloat(parts[2], 64)
	if errS != nil {
		return
	}
	totalSec := float64(h*3600+m*60) + s
	totalTenths := int64(totalSec * 10)
	for {
		cur := processed.Load()
		if totalTenths <= cur {
			return
		}
		if processed.CompareAndSwap(cur, totalTenths) {
			return
		}
	}
}
