package livecdn

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMirrorUploadsNewSegments(t *testing.T) {
	dir := t.TempDir()
	sink := NewInMemorySink()
	m := NewMirror(dir, "live/abc", sink)

	writeFile(t, dir, "h264/720p/segment_00001.ts", "TS1")
	writeFile(t, dir, "h264/720p/playlist.m3u8", "#EXTM3U\n")

	if err := m.ScanOnce(context.Background()); err != nil {
		t.Fatalf("scan: %v", err)
	}

	keys := sink.Keys()
	sort.Strings(keys)
	want := []string{"live/abc/h264/720p/playlist.m3u8", "live/abc/h264/720p/segment_00001.ts"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("keys=%v want %v", keys, want)
	}
}

func TestMirrorDeletesRotatedSegments(t *testing.T) {
	dir := t.TempDir()
	sink := NewInMemorySink()
	m := NewMirror(dir, "", sink)

	writeFile(t, dir, "seg_001.ts", "A")
	writeFile(t, dir, "seg_002.ts", "B")
	if err := m.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.Keys()) != 2 {
		t.Fatalf("expected 2 segments uploaded, got %v", sink.Keys())
	}

	// Simulate FFmpeg rotating out seg_001 and adding seg_003.
	if err := os.Remove(filepath.Join(dir, "seg_001.ts")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "seg_003.ts", "C")
	if err := m.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	keys := sink.Keys()
	sort.Strings(keys)
	if strings.Join(keys, ",") != "seg_002.ts,seg_003.ts" {
		t.Errorf("keys=%v after rotation", keys)
	}
	if len(sink.DeleteLog()) != 1 || sink.DeleteLog()[0] != "seg_001.ts" {
		t.Errorf("delete log=%v", sink.DeleteLog())
	}
}

func TestMirrorReuploadsPlaylistOnSizeChange(t *testing.T) {
	dir := t.TempDir()
	sink := NewInMemorySink()
	m := NewMirror(dir, "", sink)

	writeFile(t, dir, "playlist.m3u8", "#EXTM3U\n#EXTINF:4.0,\nseg_001.ts\n")
	if err := m.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "playlist.m3u8", "#EXTM3U\n#EXTINF:4.0,\nseg_001.ts\n#EXTINF:4.0,\nseg_002.ts\n")
	if err := m.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	puts := sink.PutLog()
	c := 0
	for _, k := range puts {
		if k == "playlist.m3u8" {
			c++
		}
	}
	if c < 2 {
		t.Errorf("playlist should be re-uploaded on size change; put log=%v", puts)
	}
}

func TestMirrorTracksSegmentList(t *testing.T) {
	dir := t.TempDir()
	sink := NewInMemorySink()
	m := NewMirror(dir, "live/x", sink)

	writeFile(t, dir, "h264/720p/segment_00001.ts", "A")
	writeFile(t, dir, "h264/720p/segment_00002.ts", "B")
	writeFile(t, dir, "h264/720p/playlist.m3u8", "#EXTM3U\n")
	if err := m.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	segs := m.Segments()
	sort.Strings(segs)
	want := []string{"live/x/h264/720p/segment_00001.ts", "live/x/h264/720p/segment_00002.ts"}
	if strings.Join(segs, ",") != strings.Join(want, ",") {
		t.Errorf("segments=%v want %v", segs, want)
	}
}

func TestRunHonorsContextCancel(t *testing.T) {
	dir := t.TempDir()
	sink := NewInMemorySink()
	m := NewMirror(dir, "x", sink)
	m.PollInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned err: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestWriteMasterEmitsAllRenditions(t *testing.T) {
	sink := NewInMemorySink()
	m := NewMirror(t.TempDir(), "live/m", sink)
	ladder := []types.Preset{
		{Name: "240p", Codec: "h264", WidthMax: 426, HeightMax: 240, BitrateKbps: 400},
		{Name: "1080p", Codec: "h264", WidthMax: 1920, HeightMax: 1080, BitrateKbps: 5000},
	}
	if err := WriteMaster(context.Background(), m, ladder); err != nil {
		t.Fatalf("WriteMaster: %v", err)
	}
	body := sink.Object("live/m/master.m3u8")
	if body == nil {
		t.Fatal("master not written to sink")
	}
	s := string(body)
	for _, sub := range []string{"#EXTM3U", "h264/240p/playlist.m3u8", "h264/1080p/playlist.m3u8", "BANDWIDTH=400000", "BANDWIDTH=5000000"} {
		if !strings.Contains(s, sub) {
			t.Errorf("master missing %q in:\n%s", sub, s)
		}
	}
}

func TestLocalFSSinkPutDelete(t *testing.T) {
	dir := t.TempDir()
	sink, err := NewLocalFSSink(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Put(context.Background(), "a/b/c.ts", "video/mp2t", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Errorf("got=%q", b)
	}
	if err := sink.Delete(context.Background(), "a/b/c.ts"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "b", "c.ts")); !os.IsNotExist(err) {
		t.Errorf("expected file removed; stat err=%v", err)
	}
	// Idempotent on missing.
	if err := sink.Delete(context.Background(), "a/b/c.ts"); err != nil {
		t.Errorf("delete on missing should be no-op, got %v", err)
	}
}
