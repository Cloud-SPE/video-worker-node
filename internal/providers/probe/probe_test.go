package probe

import (
	"context"
	"testing"
)

const sample = `{
  "format": { "duration": "12.5", "format_name": "mov,mp4" },
  "streams": [
    { "codec_type": "audio" },
    { "codec_type": "video", "width": 1920, "height": 1080 }
  ]
}`

func TestParseProbeJSON(t *testing.T) {
	t.Parallel()
	r, err := ParseProbeJSON([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if r.DurationSeconds != 12.5 {
		t.Errorf("dur=%v", r.DurationSeconds)
	}
	if r.Width != 1920 || r.Height != 1080 {
		t.Errorf("dims=%dx%d", r.Width, r.Height)
	}
	if r.Container != "mov,mp4" {
		t.Errorf("container=%q", r.Container)
	}
}

func TestParseProbeJSONInvalid(t *testing.T) {
	t.Parallel()
	if _, err := ParseProbeJSON([]byte("not json")); err == nil {
		t.Fatal("expected json error")
	}
}

func TestFakeProber(t *testing.T) {
	t.Parallel()
	f := FakeProber{R: Result{Width: 100, Height: 100, DurationSeconds: 1}}
	r, err := f.Probe(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if r.Width != 100 {
		t.Errorf("w=%d", r.Width)
	}
}

func TestNewSystem(t *testing.T) {
	t.Parallel()
	p := NewSystem()
	if p.Bin != "ffprobe" {
		t.Errorf("bin=%q", p.Bin)
	}
}
