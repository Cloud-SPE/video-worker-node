package hls

import (
	"strings"
	"testing"
)

func TestBuildMaster(t *testing.T) {
	t.Parallel()
	rends := []Rendition{
		{Name: "1080p", BitrateBps: 5_000_000, ResolutionW: 1920, ResolutionH: 1080, Codec: "h264", URI: "1080p/playlist.m3u8"},
		{Name: "360p", BitrateBps: 800_000, ResolutionW: 640, ResolutionH: 360, Codec: "h264", URI: "360p/playlist.m3u8"},
		{Name: "720p", BitrateBps: 2_500_000, ResolutionW: 1280, ResolutionH: 720, Codec: "h264", URI: "720p/playlist.m3u8"},
	}
	out, err := BuildMaster(rends)
	if err != nil {
		t.Fatal(err)
	}
	// Should be ascending by bitrate.
	idxLow := strings.Index(out, "BANDWIDTH=800000")
	idxMid := strings.Index(out, "BANDWIDTH=2500000")
	idxHi := strings.Index(out, "BANDWIDTH=5000000")
	if !(idxLow < idxMid && idxMid < idxHi) {
		t.Errorf("not sorted: %s", out)
	}
	if !strings.Contains(out, "#EXTM3U") || !strings.Contains(out, "#EXT-X-VERSION:7") {
		t.Errorf("missing manifest headers: %s", out)
	}
	if !strings.Contains(out, "avc1.640028") {
		t.Errorf("missing h264 codecs: %s", out)
	}
}

func TestBuildMasterEmpty(t *testing.T) {
	t.Parallel()
	if _, err := BuildMaster(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildMasterEmptyURI(t *testing.T) {
	t.Parallel()
	_, err := BuildMaster([]Rendition{{Name: "x", BitrateBps: 1, Codec: "h264"}})
	if err == nil {
		t.Fatal("expected URI error")
	}
}

func TestCodecsAttribute(t *testing.T) {
	t.Parallel()
	for _, c := range []string{"h264", "hevc", "av1", "wat"} {
		got := (Rendition{Codec: c}).CodecsAttribute()
		if got == "" {
			t.Errorf("codec %q empty", c)
		}
	}
}
