// Package probe is a thin wrapper around `ffprobe`. It runs as a
// subprocess (per the no-cgo rule) and parses JSON output into a Result.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// Result captures the bits of ffprobe output we care about.
type Result struct {
	DurationSeconds float64 `json:"-"`
	Width           int     `json:"-"`
	Height          int     `json:"-"`
	Container       string  `json:"-"`
}

// Prober runs ffprobe and parses results.
type Prober interface {
	Probe(ctx context.Context, path string) (Result, error)
}

// SystemProber invokes ffprobe via os/exec.
type SystemProber struct {
	Bin string
}

// NewSystem returns a SystemProber using ffprobe in PATH.
func NewSystem() *SystemProber { return &SystemProber{Bin: "ffprobe"} }

// Probe shells out to ffprobe -show_format -show_streams.
func (p *SystemProber) Probe(ctx context.Context, path string) (Result, error) {
	bin := p.Bin
	if bin == "" {
		bin = "ffprobe"
	}
	cmd := exec.CommandContext(ctx, bin, //nolint:gosec
		"-v", "error",
		"-print_format", "json",
		"-show_format", "-show_streams",
		path)
	out, err := cmd.Output()
	if err != nil {
		return Result{}, fmt.Errorf("ffprobe: %w", err)
	}
	return ParseProbeJSON(out)
}

// ParseProbeJSON parses the JSON shape ffprobe emits with -show_format
// -show_streams. Exposed for tests.
func ParseProbeJSON(b []byte) (Result, error) {
	var v struct {
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return Result{}, err
	}
	dur, _ := strconv.ParseFloat(v.Format.Duration, 64)
	r := Result{DurationSeconds: dur, Container: v.Format.FormatName}
	for _, s := range v.Streams {
		if s.CodecType == "video" {
			r.Width = s.Width
			r.Height = s.Height
			break
		}
	}
	return r, nil
}

// FakeProber returns a preconfigured Result.
type FakeProber struct {
	R Result
}

// Probe returns the preconfigured result.
func (f FakeProber) Probe(_ context.Context, _ string) (Result, error) { return f.R, nil }
