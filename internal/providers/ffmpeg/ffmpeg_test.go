package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func TestBuildArgsNVIDIA(t *testing.T) {
	t.Parallel()
	j := Job{
		InputURL:  "in.mp4",
		OutputURL: "out.mp4",
		Preset: types.Preset{
			Name: "p", Codec: "h264", WidthMax: 1280, HeightMax: 720,
			BitrateKbps: 2500, Profile: "main", GOPSeconds: 2,
		},
		GPU: types.GPUProfile{Vendor: types.GPUVendorNVIDIA},
	}
	args := BuildArgs(j)
	joined := strings.Join(args, " ")
	for _, want := range []string{"-hwaccel cuda", "-c:v h264_nvenc", "-b:v 2500k", "in.mp4", "out.mp4"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %q", want, joined)
		}
	}
}

func TestBuildArgsIntelAndAMD(t *testing.T) {
	t.Parallel()
	for _, vendor := range []types.GPUVendor{types.GPUVendorIntel, types.GPUVendorAMD} {
		j := Job{
			InputURL: "in", OutputURL: "out",
			Preset: types.Preset{Name: "p", Codec: "hevc", WidthMax: 1, HeightMax: 1, BitrateKbps: 1},
			GPU:    types.GPUProfile{Vendor: vendor},
		}
		args := BuildArgs(j)
		joined := strings.Join(args, " ")
		if vendor == types.GPUVendorIntel && !strings.Contains(joined, "hevc_qsv") {
			t.Errorf("intel: missing qsv codec in %q", joined)
		}
		if vendor == types.GPUVendorAMD && !strings.Contains(joined, "hevc_vaapi") {
			t.Errorf("amd: missing vaapi codec in %q", joined)
		}
	}
}

func TestBuildArgsExtra(t *testing.T) {
	t.Parallel()
	j := Job{
		InputURL: "in", OutputURL: "out",
		Preset: types.Preset{Name: "p", Codec: "h264", WidthMax: 1, HeightMax: 1, BitrateKbps: 1},
		GPU:    types.GPUProfile{Vendor: types.GPUVendorNVIDIA},
		Extra:  []string{"-extra-1", "value-1"},
	}
	args := BuildArgs(j)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-extra-1 value-1") {
		t.Errorf("missing extras: %q", joined)
	}
}

func TestParseProgressStream(t *testing.T) {
	t.Parallel()
	input := strings.NewReader(strings.Join([]string{
		"frame=30",
		"fps=30.0",
		"bitrate= 2500.4kbits/s",
		"out_time_us=1000000",
		"speed=1.0x",
		"progress=continue",
		"frame=60",
		"out_time_us=2000000",
		"progress=end",
		"",
	}, "\n"))
	out := make(chan Progress, 4)
	cap := &bytes.Buffer{}
	if err := ParseProgressStream(input, out, cap); err != nil {
		t.Fatal(err)
	}
	close(out)
	var got []Progress
	for p := range out {
		got = append(got, p)
	}
	if len(got) != 2 {
		t.Fatalf("got=%v", got)
	}
	if got[0].Frame != 30 || got[0].OutTimeSeconds != 1.0 || got[1].OutTimeSeconds != 2.0 {
		t.Fatalf("progress=%+v", got)
	}
	if cap.Len() == 0 {
		t.Fatal("capture should have content")
	}
}

func TestParseProgressBlankAndJunk(t *testing.T) {
	t.Parallel()
	in := strings.NewReader("garbage line\nanother no equals\n")
	if err := ParseProgressStream(in, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRingBuffer(t *testing.T) {
	t.Parallel()
	r := newRingBuffer(5)
	r.Write([]byte("abcdef")) // 6 bytes — should retain last 5
	if got := r.String(); got != "bcdef" {
		t.Fatalf("got=%q", got)
	}
	r.Write([]byte("g"))
	if got := r.String(); got != "cdefg" {
		t.Fatalf("got=%q", got)
	}
}

func TestFakeRunnerSuccess(t *testing.T) {
	t.Parallel()
	f := &FakeRunner{Steps: 3}
	prog := make(chan Progress, 4)
	res, err := f.Run(context.Background(), Job{
		Preset: types.Preset{BitrateKbps: 1000, Codec: "h264", WidthMax: 1, HeightMax: 1, Name: "p"},
	}, prog)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d", res.ExitCode)
	}
	count := 0
	for range prog {
		count++
	}
	if count != 3 {
		t.Errorf("emitted=%d want 3", count)
	}
}

func TestFakeRunnerFailure(t *testing.T) {
	t.Parallel()
	f := &FakeRunner{Steps: 2, FailWithExit: 7, FailedStderr: "broken"}
	prog := make(chan Progress, 4)
	_, err := f.Run(context.Background(), Job{Preset: types.Preset{Name: "p", Codec: "h264", WidthMax: 1, HeightMax: 1, BitrateKbps: 1}}, prog)
	for range prog {
	}
	var je *types.JobError
	if !errors.As(err, &je) {
		t.Fatalf("want JobError, got %T %v", err, err)
	}
	if je.ExitCode != 7 {
		t.Errorf("exit=%d", je.ExitCode)
	}
	if !strings.Contains(je.Stderr, "broken") {
		t.Errorf("stderr=%q", je.Stderr)
	}
}

func TestFakeRunnerCancellation(t *testing.T) {
	t.Parallel()
	f := &FakeRunner{Steps: 5, PerStep: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prog := make(chan Progress, 8)
	_, err := f.Run(ctx, Job{Preset: types.Preset{Name: "p", Codec: "h264", WidthMax: 1, HeightMax: 1, BitrateKbps: 1}}, prog)
	for range prog {
	}
	if err == nil {
		t.Fatal("expected error on cancel")
	}
	if !f.Cancelled.Load() {
		t.Error("expected Cancelled flag")
	}
}

func TestCodecFlagFallback(t *testing.T) {
	t.Parallel()
	if got := codecFlag("h264", types.GPUVendorNone); got != "libh264" {
		t.Errorf("got=%q", got)
	}
}
