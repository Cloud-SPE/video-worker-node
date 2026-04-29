package presetloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

const goodYAML = `
presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
  - name: 1080p
    codec: h264
    width_max: 1920
    height_max: 1080
    bitrate_kbps: 5000
  - name: 4k-hevc
    codec: hevc
    width_max: 3840
    height_max: 2160
    bitrate_kbps: 15000
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "presets.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewAndCatalogue(t *testing.T) {
	t.Parallel()
	l, err := New(writeTemp(t, goodYAML))
	if err != nil {
		t.Fatal(err)
	}
	c := l.Catalogue()
	if len(c.Presets) != 3 {
		t.Fatalf("len=%d", len(c.Presets))
	}
	if _, ok := l.Lookup("720p"); !ok {
		t.Fatal("missing 720p")
	}
	if _, ok := l.Lookup("ghost"); ok {
		t.Fatal("ghost should be missing")
	}
}

func TestFilterByGPU(t *testing.T) {
	t.Parallel()
	l, err := New(writeTemp(t, goodYAML))
	if err != nil {
		t.Fatal(err)
	}
	got := l.FilterByGPU(types.GPUProfile{SupportsH264: true})
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	got = l.FilterByGPU(types.GPUProfile{SupportsH264: true, SupportsHEVC: true})
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
}

func TestReload(t *testing.T) {
	t.Parallel()
	path := writeTemp(t, goodYAML)
	l, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	newYAML := `
presets:
  - name: only
    codec: av1
    width_max: 1280
    height_max: 720
    bitrate_kbps: 1500
`
	if err := os.WriteFile(path, []byte(newYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Reload(); err != nil {
		t.Fatal(err)
	}
	c := l.Catalogue()
	if len(c.Presets) != 1 || c.Presets[0].Name != "only" {
		t.Fatalf("after reload: %+v", c.Presets)
	}
}

func TestParseBytesErrors(t *testing.T) {
	t.Parallel()
	if _, err := ParseBytes([]byte("not: valid: yaml: garbage:")); err == nil {
		t.Fatal("expected yaml error")
	}
	if _, err := ParseBytes([]byte("presets: []")); err == nil {
		t.Fatal("expected empty catalogue error")
	}
	bad := `
presets:
  - name: bad
    codec: h264
    width_max: 0
    height_max: 720
    bitrate_kbps: 1
`
	if _, err := ParseBytes([]byte(bad)); err == nil || !strings.Contains(err.Error(), "preset[0]") {
		t.Fatalf("expected preset[0] error, got %v", err)
	}
}

func TestNewBadPath(t *testing.T) {
	t.Parallel()
	if _, err := New("/no/such/file.yaml"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := New(""); err == nil {
		t.Fatal("expected empty-path error")
	}
}
