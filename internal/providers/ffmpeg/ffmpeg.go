// Package ffmpeg is the single point of contact with the FFmpeg binary.
//
// Hard rule (enforced by lint/no-cgo): FFmpeg is invoked as a subprocess.
// Never imported via cgo / lpms / libav linking. Crash isolation, resource
// accounting, cancellation, output capture, and progress parsing all live
// here.
//
// The Runner interface is the testable surface; the SystemRunner is the
// production exec.Cmd-backed implementation. FakeRunner is used by VOD /
// ABR / Live runner tests and by the minimal-e2e example.
package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Job describes one FFmpeg subprocess invocation.
type Job struct {
	// InputURL is the resolved file path (or pipe: for stdin).
	InputURL string
	// OutputURL is the resolved output file path.
	OutputURL string
	// Preset is the validated encoding profile.
	Preset types.Preset
	// GPU is the detected GPU profile (drives codec selection).
	GPU types.GPUProfile
	// Extra args appended after the codec args (e.g. live mode trickle hooks).
	Extra []string
	// MaxLogBytes caps captured log bytes.
	MaxLogBytes int
}

// Runner is the testable surface. Run returns the captured stderr/stdout
// truncated to MaxLogBytes and the exit-code-bearing JobError when the
// process exited non-zero.
type Runner interface {
	Run(ctx context.Context, job Job, progress chan<- Progress) (RunResult, error)
}

// RunResult captures everything we know after the subprocess finishes.
type RunResult struct {
	Argv     []string
	ExitCode int
	Stdout   string
	Stderr   string
	Duration time.Duration
}

// Progress is one parsed progress event from FFmpeg's stderr.
type Progress struct {
	OutTimeSeconds float64
	FPS            float64
	Speed          float64
	BitrateKbps    float64
	Frame          int64
}

// BuildArgs constructs the FFmpeg argv for the given Job + GPU profile.
// Pure (no IO); exposed for tests.
func BuildArgs(j Job) []string {
	args := []string{"-y", "-loglevel", "info", "-progress", "pipe:2"}
	hwaccel := hwaccelFlags(j.GPU.Vendor)
	args = append(args, hwaccel...)
	args = append(args, "-i", j.InputURL)
	scale := fmt.Sprintf("scale=w=%d:h=%d:force_original_aspect_ratio=decrease",
		j.Preset.WidthMax, j.Preset.HeightMax)
	args = append(args, "-vf", scale)
	codec := codecFlag(j.Preset.Codec, j.GPU.Vendor)
	args = append(args, "-c:v", codec)
	args = append(args, "-b:v", fmt.Sprintf("%dk", j.Preset.BitrateKbps))
	if j.Preset.Profile != "" {
		args = append(args, "-profile:v", j.Preset.Profile)
	}
	if j.Preset.GOPSeconds > 0 {
		args = append(args, "-g", fmt.Sprintf("%g", j.Preset.GOPSeconds*30)) // 30fps assumption
	}
	args = append(args, "-c:a", "aac", "-b:a", "128k")
	args = append(args, j.Extra...)
	args = append(args, j.OutputURL)
	return args
}

func hwaccelFlags(vendor types.GPUVendor) []string {
	switch vendor {
	case types.GPUVendorNVIDIA:
		return []string{"-hwaccel", "cuda", "-hwaccel_output_format", "cuda"}
	case types.GPUVendorIntel:
		return []string{"-hwaccel", "qsv", "-hwaccel_output_format", "qsv"}
	case types.GPUVendorAMD:
		return []string{"-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi", "-vaapi_device", "/dev/dri/renderD128"}
	}
	return nil
}

func codecFlag(codec string, vendor types.GPUVendor) string {
	switch vendor {
	case types.GPUVendorNVIDIA:
		switch codec {
		case "h264":
			return "h264_nvenc"
		case "hevc":
			return "hevc_nvenc"
		case "av1":
			return "av1_nvenc"
		}
	case types.GPUVendorIntel:
		switch codec {
		case "h264":
			return "h264_qsv"
		case "hevc":
			return "hevc_qsv"
		case "av1":
			return "av1_qsv"
		}
	case types.GPUVendorAMD:
		switch codec {
		case "h264":
			return "h264_vaapi"
		case "hevc":
			return "hevc_vaapi"
		case "av1":
			return "av1_vaapi"
		}
	}
	// Software fallback should never happen in practice (preflight refuses to
	// start without a GPU), but keep it sane for unit tests.
	return "lib" + codec
}

// SystemRunner runs ffmpeg via os/exec.
type SystemRunner struct {
	Bin         string
	CancelGrace time.Duration
}

// Run invokes the ffmpeg subprocess. Honors ctx cancellation: SIGTERM
// followed by SIGKILL after CancelGrace.
func (r *SystemRunner) Run(ctx context.Context, j Job, progress chan<- Progress) (RunResult, error) {
	bin := r.Bin
	if bin == "" {
		bin = "ffmpeg"
	}
	grace := r.CancelGrace
	if grace == 0 {
		grace = 5 * time.Second
	}
	args := BuildArgs(j)
	start := time.Now()
	cmd := exec.Command(bin, args...) //nolint:gosec
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return RunResult{Argv: args}, err
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return RunResult{Argv: args}, err
	}
	if err := cmd.Start(); err != nil {
		return RunResult{Argv: args}, fmt.Errorf("start ffmpeg: %w", err)
	}
	maxLog := j.MaxLogBytes
	if maxLog <= 0 {
		maxLog = 128 * 1024
	}
	stderrBuf := newRingBuffer(maxLog)
	stdoutBuf := newRingBuffer(maxLog)

	var wg sync.WaitGroup
	wg.Add(2)
	// stderr — parse progress + capture
	go func() {
		defer wg.Done()
		_ = ParseProgressStream(stderrPipe, progress, stderrBuf)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stdoutBuf, stdoutPipe)
	}()

	// Cancellation: ctx.Done → SIGTERM → wait grace → SIGKILL.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case <-ctx.Done():
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case waitErr = <-done:
		case <-time.After(grace):
			_ = cmd.Process.Kill()
			waitErr = <-done
		}
	case waitErr = <-done:
	}
	wg.Wait()
	if progress != nil {
		close(progress)
	}
	res := RunResult{
		Argv:     append([]string{bin}, args...),
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		Duration: time.Since(start),
	}
	if waitErr == nil {
		return res, nil
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		res.ExitCode = exitErr.ExitCode()
	}
	return res, &types.JobError{
		Code:     types.ErrCodeEncodingFailed,
		Message:  "ffmpeg subprocess exited non-zero",
		ExitCode: res.ExitCode,
		Stderr:   res.Stderr,
	}
}

// ringBuffer is a write-only buffer that retains at most cap bytes from
// the most recent writes. Used to cap captured log size without growing
// the heap unboundedly during long encodes.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newRingBuffer(cap int) *ringBuffer { return &ringBuffer{cap: cap} }

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.cap {
		r.buf = r.buf[len(r.buf)-r.cap:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

// FakeRunner is a deterministic Runner used in tests. It emits a synthetic
// progress sequence then returns Result.
type FakeRunner struct {
	// Steps controls how many progress events to emit.
	Steps int
	// PerStep controls the simulated wall-time between events.
	PerStep time.Duration
	// FailWithExit, if non-zero, causes Run to return JobError with that
	// exit code instead of success.
	FailWithExit int
	// FailedStderr is written into RunResult.Stderr when FailWithExit != 0.
	FailedStderr string
	// Cancelled is set when ctx fired before Steps emission completed.
	Cancelled atomic.Bool
}

// Run emits Steps progress events, honors ctx cancellation, and returns
// either success or the configured failure.
func (f *FakeRunner) Run(ctx context.Context, j Job, progress chan<- Progress) (RunResult, error) {
	steps := f.Steps
	if steps <= 0 {
		steps = 5
	}
	per := f.PerStep
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			f.Cancelled.Store(true)
			if progress != nil {
				close(progress)
			}
			return RunResult{Argv: BuildArgs(j), ExitCode: 130, Stderr: "cancelled"},
				&types.JobError{Code: types.ErrCodeEncodingFailed, Message: "cancelled", ExitCode: 130}
		default:
		}
		if per > 0 {
			select {
			case <-ctx.Done():
				f.Cancelled.Store(true)
				if progress != nil {
					close(progress)
				}
				return RunResult{Argv: BuildArgs(j), ExitCode: 130, Stderr: "cancelled"},
					&types.JobError{Code: types.ErrCodeEncodingFailed, Message: "cancelled", ExitCode: 130}
			case <-time.After(per):
			}
		}
		if progress != nil {
			progress <- Progress{
				OutTimeSeconds: float64(i),
				FPS:            30,
				Speed:          1,
				BitrateKbps:    float64(j.Preset.BitrateKbps),
				Frame:          int64(i * 30),
			}
		}
	}
	if progress != nil {
		close(progress)
	}
	if f.FailWithExit != 0 {
		return RunResult{Argv: BuildArgs(j), ExitCode: f.FailWithExit, Stderr: f.FailedStderr},
			&types.JobError{Code: types.ErrCodeEncodingFailed, Message: "fake failure",
				ExitCode: f.FailWithExit, Stderr: f.FailedStderr}
	}
	return RunResult{Argv: BuildArgs(j), Stderr: "done"}, nil
}

// ParseProgressStream reads ffmpeg `-progress pipe:2` key=value lines from
// r and emits Progress events. Each frame block is delimited by a
// `progress=continue` or `progress=end` line.
func ParseProgressStream(r io.Reader, progress chan<- Progress, capture io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	cur := Progress{}
	for sc.Scan() {
		line := sc.Text()
		if capture != nil {
			_, _ = capture.Write([]byte(line + "\n"))
		}
		key, val, ok := splitKV(line)
		if !ok {
			continue
		}
		switch key {
		case "frame":
			fmt.Sscanf(val, "%d", &cur.Frame)
		case "fps":
			fmt.Sscanf(val, "%f", &cur.FPS)
		case "bitrate":
			s := strings.TrimSuffix(strings.TrimSpace(val), "kbits/s")
			fmt.Sscanf(s, "%f", &cur.BitrateKbps)
		case "out_time_us":
			var us int64
			fmt.Sscanf(val, "%d", &us)
			cur.OutTimeSeconds = float64(us) / 1_000_000.0
		case "speed":
			s := strings.TrimSuffix(strings.TrimSpace(val), "x")
			fmt.Sscanf(s, "%f", &cur.Speed)
		case "progress":
			if progress != nil {
				progress <- cur
			}
			if val == "end" {
				return nil
			}
			cur = Progress{}
		}
	}
	return sc.Err()
}

func splitKV(line string) (string, string, bool) {
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

// CancelError is the sentinel returned when a SystemRunner.Run is
// terminated via ctx cancellation.
var CancelError = errors.New("ffmpeg: cancelled")
