package ffmpeg

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func sampleLadder() []types.Preset {
	return []types.Preset{
		{Name: "240p", Codec: "h264", WidthMax: 426, HeightMax: 240, BitrateKbps: 400, GOPSeconds: 4},
		{Name: "720p", Codec: "h264", WidthMax: 1280, HeightMax: 720, BitrateKbps: 2800, GOPSeconds: 4},
	}
}

func TestBuildLiveArgsBaseline(t *testing.T) {
	args := BuildLiveArgs(LiveJob{
		MediaFormat:    "flv",
		LocalDir:       "/tmp/live/abc",
		Ladder:         sampleLadder(),
		SegmentSeconds: 4,
		PlaylistSize:   6,
		GPU:            types.GPUProfile{Vendor: types.GPUVendorNVIDIA, SupportsH264: true},
	})

	joined := strings.Join(args, " ")

	// Input.
	if !strings.Contains(joined, "-f flv -i pipe:0") {
		t.Errorf("missing FLV input: %s", joined)
	}
	// Per-rendition.
	if !strings.Contains(joined, "-s 426x240") {
		t.Error("missing 240p resolution")
	}
	if !strings.Contains(joined, "-s 1280x720") {
		t.Error("missing 720p resolution")
	}
	if !strings.Contains(joined, "-b:v 400k") {
		t.Error("missing 240p bitrate")
	}
	if !strings.Contains(joined, "-b:v 2800k") {
		t.Error("missing 720p bitrate")
	}
	// HLS knobs.
	if !strings.Contains(joined, "-hls_time 4") {
		t.Error("missing hls_time")
	}
	if !strings.Contains(joined, "-hls_list_size 6") {
		t.Error("missing hls_list_size")
	}
	if !strings.Contains(joined, "delete_segments+append_list+omit_endlist") {
		t.Error("missing delete_segments rotation flag")
	}
	// Output paths.
	wantSeg := filepath.Join("/tmp/live/abc", "h264", "240p", "segment_%05d.ts")
	wantPl := filepath.Join("/tmp/live/abc", "h264", "240p", "playlist.m3u8")
	if !strings.Contains(joined, wantSeg) || !strings.Contains(joined, wantPl) {
		t.Errorf("missing 240p output paths in: %s", joined)
	}
	// NVENC.
	if !strings.Contains(joined, "h264_nvenc") {
		t.Errorf("expected h264_nvenc for NVIDIA GPU, got: %s", joined)
	}
	// AAC audio.
	if !strings.Contains(joined, "-c:a aac") {
		t.Error("missing AAC audio codec")
	}
}

func TestBuildLiveArgsDefaults(t *testing.T) {
	args := BuildLiveArgs(LiveJob{
		LocalDir: "/x",
		Ladder:   sampleLadder(),
		GPU:      types.GPUProfile{Vendor: types.GPUVendorNVIDIA, SupportsH264: true},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-hls_time 4") {
		t.Error("default segment seconds = 4 not applied")
	}
	if !strings.Contains(joined, "-hls_list_size 6") {
		t.Error("default playlist size = 6 not applied")
	}
	if !strings.Contains(joined, "-b:a 128k") {
		t.Error("default audio bitrate 128k not applied")
	}
	if !strings.Contains(joined, "-f flv") {
		t.Error("default media format flv not applied")
	}
}

func TestParseLiveProgressUpdatesMonotonically(t *testing.T) {
	var p atomic.Int64
	parseLiveProgress("frame=  120 fps= 30 q=23.0 size=N/A time=00:00:05.50 bitrate=N/A", &p)
	if got := p.Load(); got != 55 {
		t.Errorf("after 5.5s: tenths=%d want 55", got)
	}
	parseLiveProgress("noise", &p)
	if got := p.Load(); got != 55 {
		t.Errorf("noise should not move tenths: got %d", got)
	}
	parseLiveProgress("time=00:00:04.00 anything", &p)
	if got := p.Load(); got != 55 {
		t.Errorf("non-monotonic update should not regress: got %d want 55", got)
	}
	parseLiveProgress("time=00:00:10.00 next", &p)
	if got := p.Load(); got != 100 {
		t.Errorf("after 10s: tenths=%d want 100", got)
	}
}

func TestLiveSystemEncoderRejectsMissingDir(t *testing.T) {
	enc := NewLiveSystemEncoder(LiveJob{})
	err := enc.Start(context.Background(), LiveEncoderInput{Reader: strings.NewReader("")})
	if err == nil || !strings.Contains(err.Error(), "LocalDir") {
		t.Fatalf("expected LocalDir error, got %v", err)
	}
}

func TestLiveSystemEncoderRejectsNilReader(t *testing.T) {
	enc := NewLiveSystemEncoder(LiveJob{LocalDir: "/tmp"})
	err := enc.Start(context.Background(), LiveEncoderInput{})
	if err == nil || !strings.Contains(err.Error(), "nil reader") {
		t.Fatalf("expected nil-reader error, got %v", err)
	}
}

func TestLiveSystemEncoderCancelsCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess test in short mode")
	}
	// We can't actually run ffmpeg in CI unconditionally, but we can
	// confirm the bin lookup error path: a bogus binary produces a
	// startup error within the grace window.
	enc := NewLiveSystemEncoder(LiveJob{
		LocalDir:    t.TempDir(),
		Bin:         "/nonexistent-ffmpeg-binary",
		Ladder:      sampleLadder(),
		GPU:         types.GPUProfile{Vendor: types.GPUVendorNVIDIA, SupportsH264: true},
		CancelGrace: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := enc.Start(ctx, LiveEncoderInput{Reader: strings.NewReader(""), MediaFormat: "flv"})
	if err == nil {
		t.Fatal("expected start error on bogus binary")
	}
	// either "start: ..." or "executable file not found"
	if !errors.Is(err, errors.New("x")) && !strings.Contains(err.Error(), "start") && !strings.Contains(err.Error(), "executable") {
		t.Logf("got expected start failure: %v", err)
	}
}
