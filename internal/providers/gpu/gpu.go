// Package gpu detects the host's GPU vendor and capabilities at startup.
//
// Detection is deliberately pluggable through the Detector interface so
// tests can inject fake responses. The default implementations probe via
// nvidia-smi (NVIDIA) and vainfo on /dev/dri/renderD128 (Intel/AMD).
//
// Per plan 0007 §D, a failed detection in a vendor-required configuration
// is a fatal preflight error — no CPU fallback.
package gpu

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Detector probes the host for GPUs and returns a profile. Implementations
// must not panic on missing tooling — return an empty profile + error so
// the caller can decide whether the missing GPU is fatal.
type Detector interface {
	Detect(ctx context.Context, vendor types.GPUVendor) (types.GPUProfile, error)
}

// CommandRunner is the subprocess shim used by the system Detector. Tests
// substitute their own.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

type execRunner struct{ timeout time.Duration }

func (r execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec
	out, err := cmd.CombinedOutput()
	return out, nil, err
}

// SystemDetector probes the host using exec'd subprocess commands.
type SystemDetector struct {
	Run CommandRunner
}

// NewSystemDetector returns a SystemDetector with a 5s subprocess timeout.
func NewSystemDetector() *SystemDetector {
	return &SystemDetector{Run: execRunner{timeout: 5 * time.Second}}
}

// Detect resolves which vendor to probe (auto walks all three) and runs
// the matching probe.
func (d *SystemDetector) Detect(ctx context.Context, vendor types.GPUVendor) (types.GPUProfile, error) {
	if d.Run == nil {
		d.Run = execRunner{timeout: 5 * time.Second}
	}
	switch vendor {
	case types.GPUVendorAuto:
		// Try NVIDIA, then Intel, then AMD. First success wins.
		if p, err := d.probeNVIDIA(ctx); err == nil {
			return p, nil
		}
		if p, err := d.probeVAAPI(ctx, types.GPUVendorIntel); err == nil {
			return p, nil
		}
		if p, err := d.probeVAAPI(ctx, types.GPUVendorAMD); err == nil {
			return p, nil
		}
		return types.GPUProfile{Vendor: types.GPUVendorNone}, fmt.Errorf("%s: no supported GPU detected", types.ErrCodePreflightNoGPU)
	case types.GPUVendorNVIDIA:
		return d.probeNVIDIA(ctx)
	case types.GPUVendorIntel:
		return d.probeVAAPI(ctx, types.GPUVendorIntel)
	case types.GPUVendorAMD:
		return d.probeVAAPI(ctx, types.GPUVendorAMD)
	case types.GPUVendorNone:
		return types.GPUProfile{Vendor: types.GPUVendorNone, DetectedAt: time.Now()}, nil
	}
	return types.GPUProfile{}, fmt.Errorf("unsupported vendor: %s", vendor)
}

func (d *SystemDetector) probeNVIDIA(ctx context.Context) (types.GPUProfile, error) {
	out, _, err := d.Run.Run(ctx, "nvidia-smi", "--query-gpu=name,driver_version,memory.total", "--format=csv,noheader,nounits")
	if err != nil {
		return types.GPUProfile{}, fmt.Errorf("%s: nvidia-smi: %w", types.ErrCodePreflightNoGPU, err)
	}
	return ParseNVIDIAOutput(out)
}

func (d *SystemDetector) probeVAAPI(ctx context.Context, vendor types.GPUVendor) (types.GPUProfile, error) {
	out, _, err := d.Run.Run(ctx, "vainfo")
	if err != nil {
		return types.GPUProfile{}, fmt.Errorf("%s: vainfo: %w", types.ErrCodePreflightNoGPU, err)
	}
	return ParseVAAPIOutput(out, vendor)
}

// ParseNVIDIAOutput parses the comma-separated `nvidia-smi --query-gpu=...`
// output and produces a GPUProfile. Exposed for tests.
func ParseNVIDIAOutput(b []byte) (types.GPUProfile, error) {
	line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if line == "" {
		return types.GPUProfile{}, errors.New("empty nvidia-smi output")
	}
	parts := strings.Split(line, ",")
	if len(parts) < 3 {
		return types.GPUProfile{}, fmt.Errorf("malformed nvidia-smi line: %q", line)
	}
	name := strings.TrimSpace(parts[0])
	driver := strings.TrimSpace(parts[1])
	vramMB, _ := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64)

	maxSessions := int32(8) // NVENC consumer cap circa 2024 — operator overrides via flag
	if strings.Contains(strings.ToLower(name), "data center") || strings.Contains(strings.ToLower(name), "tesla") || strings.Contains(strings.ToLower(name), "a100") || strings.Contains(strings.ToLower(name), "h100") || strings.Contains(strings.ToLower(name), "l40") || strings.Contains(strings.ToLower(name), "l4") {
		maxSessions = 0 // unlimited (unrestricted on data center cards)
	}

	return types.GPUProfile{
		Vendor:       types.GPUVendorNVIDIA,
		Model:        name,
		DriverVer:    driver,
		VRAMBytes:    vramMB * 1024 * 1024,
		MaxSessions:  maxSessions,
		SupportsH264: true,
		SupportsHEVC: true,
		SupportsAV1:  isNVIDIAAV1Capable(name),
		DetectedAt:   time.Now(),
	}, nil
}

// isNVIDIAAV1Capable returns true for cards with AV1 NVENC (Ada Lovelace +).
func isNVIDIAAV1Capable(model string) bool {
	m := strings.ToLower(model)
	for _, tag := range []string{"rtx 40", "rtx 50", "l4", "l40", "h100", "h200", "ada"} {
		if strings.Contains(m, tag) {
			return true
		}
	}
	return false
}

// ParseVAAPIOutput parses `vainfo` text output and produces a GPUProfile
// for the Intel or AMD vendor as instructed.
func ParseVAAPIOutput(b []byte, vendor types.GPUVendor) (types.GPUProfile, error) {
	text := string(b)
	if !strings.Contains(text, "VAProfile") {
		return types.GPUProfile{}, fmt.Errorf("vainfo output missing VAProfile lines: %q", text)
	}
	lower := strings.ToLower(text)
	prof := types.GPUProfile{
		Vendor:       vendor,
		SupportsH264: strings.Contains(lower, "h264"),
		SupportsHEVC: strings.Contains(lower, "hevc"),
		SupportsAV1:  strings.Contains(lower, "av1"),
		MaxSessions:  16, // generous default; overridden by operator
		DetectedAt:   time.Now(),
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Driver version:") {
			prof.DriverVer = strings.TrimSpace(strings.TrimPrefix(line, "Driver version:"))
		}
		if strings.HasPrefix(line, "vainfo: Driver version:") {
			prof.DriverVer = strings.TrimSpace(strings.TrimPrefix(line, "vainfo: Driver version:"))
		}
	}
	return prof, nil
}

// FakeDetector is a Detector that returns a preconfigured profile. For
// tests and `--dev` mode.
type FakeDetector struct {
	Profile types.GPUProfile
	Err     error
}

// Detect returns the preconfigured profile / error.
func (f FakeDetector) Detect(_ context.Context, _ types.GPUVendor) (types.GPUProfile, error) {
	if f.Err != nil {
		return types.GPUProfile{}, f.Err
	}
	if f.Profile.DetectedAt.IsZero() {
		f.Profile.DetectedAt = time.Now()
	}
	return f.Profile, nil
}

// FakeNVIDIA returns a Detector that always reports a synthetic NVIDIA
// L40 profile. Used by `--dev`.
func FakeNVIDIA() FakeDetector {
	return FakeDetector{
		Profile: types.GPUProfile{
			Vendor:       types.GPUVendorNVIDIA,
			Model:        "fake-nvidia-l40",
			DriverVer:    "555.42.06",
			VRAMBytes:    48 * 1024 * 1024 * 1024,
			MaxSessions:  0,
			SupportsH264: true,
			SupportsHEVC: true,
			SupportsAV1:  true,
		},
	}
}
