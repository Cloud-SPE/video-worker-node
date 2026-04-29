package gpu

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

type fakeRunner struct {
	out []byte
	err error
}

func (f fakeRunner) Run(_ context.Context, _ string, _ ...string) ([]byte, []byte, error) {
	return f.out, nil, f.err
}

func TestParseNVIDIAOutput(t *testing.T) {
	t.Parallel()
	out := []byte("NVIDIA L40, 555.42.06, 49152\n")
	p, err := ParseNVIDIAOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if p.Vendor != types.GPUVendorNVIDIA {
		t.Errorf("vendor=%v", p.Vendor)
	}
	if p.Model != "NVIDIA L40" {
		t.Errorf("model=%q", p.Model)
	}
	if p.DriverVer != "555.42.06" {
		t.Errorf("driver=%q", p.DriverVer)
	}
	if !p.SupportsH264 || !p.SupportsHEVC || !p.SupportsAV1 {
		t.Errorf("L40 should support h264/hevc/av1: %+v", p)
	}
	if p.MaxSessions != 0 {
		t.Errorf("L40 should be unlimited sessions: %d", p.MaxSessions)
	}
	if p.VRAMBytes != 49152*1024*1024 {
		t.Errorf("vram bytes = %d", p.VRAMBytes)
	}
}

func TestParseNVIDIAConsumerCard(t *testing.T) {
	t.Parallel()
	p, err := ParseNVIDIAOutput([]byte("NVIDIA GeForce RTX 3060, 555.42, 12288\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.MaxSessions != 8 {
		t.Errorf("3060 expected MaxSessions=8 (consumer cap), got %d", p.MaxSessions)
	}
	if p.SupportsAV1 {
		t.Errorf("3060 should NOT support AV1 NVENC")
	}
	pAda, _ := ParseNVIDIAOutput([]byte("NVIDIA GeForce RTX 4090, 555.42, 24576\n"))
	if !pAda.SupportsAV1 {
		t.Errorf("4090 (Ada) should support AV1")
	}
}

func TestParseNVIDIAErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseNVIDIAOutput([]byte("")); err == nil {
		t.Fatal("expected error on empty")
	}
	if _, err := ParseNVIDIAOutput([]byte("only-one-field")); err == nil {
		t.Fatal("expected error on malformed")
	}
}

func TestParseVAAPIOutput(t *testing.T) {
	t.Parallel()
	out := []byte(`vainfo: VA-API version: 1.16.0
Driver version: Intel iHD driver - 23.1.0
VAProfileH264Main
VAProfileH264High
VAProfileHEVCMain
VAProfileAV1Profile0
`)
	p, err := ParseVAAPIOutput(out, types.GPUVendorIntel)
	if err != nil {
		t.Fatal(err)
	}
	if !p.SupportsH264 || !p.SupportsHEVC || !p.SupportsAV1 {
		t.Errorf("expected all codecs, got %+v", p)
	}
	if p.Vendor != types.GPUVendorIntel {
		t.Errorf("vendor=%v", p.Vendor)
	}
}

func TestParseVAAPIErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseVAAPIOutput([]byte("nope"), types.GPUVendorIntel); err == nil {
		t.Fatal("expected error")
	}
}

func TestSystemDetectorAuto(t *testing.T) {
	t.Parallel()
	d := &SystemDetector{Run: fakeRunner{out: []byte("NVIDIA L40, 555.42, 49152\n")}}
	p, err := d.Detect(context.Background(), types.GPUVendorAuto)
	if err != nil {
		t.Fatal(err)
	}
	if p.Vendor != types.GPUVendorNVIDIA {
		t.Errorf("vendor=%v", p.Vendor)
	}
}

func TestSystemDetectorNoneFound(t *testing.T) {
	t.Parallel()
	d := &SystemDetector{Run: fakeRunner{err: errors.New("not installed")}}
	_, err := d.Detect(context.Background(), types.GPUVendorAuto)
	if err == nil || !strings.Contains(err.Error(), "PREFLIGHT_NO_GPU") {
		t.Fatalf("err=%v want PREFLIGHT_NO_GPU", err)
	}
}

func TestSystemDetectorIntel(t *testing.T) {
	t.Parallel()
	out := []byte(`Driver version: 23.1.0
VAProfileH264Main`)
	d := &SystemDetector{Run: fakeRunner{out: out}}
	p, err := d.Detect(context.Background(), types.GPUVendorIntel)
	if err != nil {
		t.Fatal(err)
	}
	if p.Vendor != types.GPUVendorIntel || !p.SupportsH264 {
		t.Errorf("got %+v", p)
	}
}

func TestSystemDetectorNoneVendor(t *testing.T) {
	t.Parallel()
	d := &SystemDetector{Run: fakeRunner{}}
	p, err := d.Detect(context.Background(), types.GPUVendorNone)
	if err != nil {
		t.Fatal(err)
	}
	if p.Vendor != types.GPUVendorNone {
		t.Errorf("vendor=%v", p.Vendor)
	}
	d2 := &SystemDetector{Run: fakeRunner{}}
	if _, err := d2.Detect(context.Background(), types.GPUVendor("bad")); err == nil {
		t.Fatal("expected unsupported error")
	}
}

func TestNewSystemDetector(t *testing.T) {
	t.Parallel()
	d := NewSystemDetector()
	if d == nil || d.Run == nil {
		t.Fatal("nil")
	}
}

func TestFakeDetector(t *testing.T) {
	t.Parallel()
	f := FakeNVIDIA()
	p, err := f.Detect(context.Background(), types.GPUVendorAuto)
	if err != nil {
		t.Fatal(err)
	}
	if p.Vendor != types.GPUVendorNVIDIA {
		t.Errorf("vendor=%v", p.Vendor)
	}
	if p.DetectedAt.IsZero() {
		t.Error("DetectedAt should be set")
	}
	bad := FakeDetector{Err: errors.New("boom")}
	if _, err := bad.Detect(context.Background(), types.GPUVendorAuto); err == nil {
		t.Fatal("expected error")
	}
}
