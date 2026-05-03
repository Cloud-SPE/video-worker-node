package workloadcost

import (
	"testing"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func TestForPresetOrdersByPresetWeight(t *testing.T) {
	t.Parallel()
	small := types.Preset{Name: "480p", Codec: "h264", WidthMax: 854, HeightMax: 480, BitrateKbps: 1200}
	medium := types.Preset{Name: "720p", Codec: "h264", WidthMax: 1280, HeightMax: 720, BitrateKbps: 2500}
	heavy := types.Preset{Name: "1080p", Codec: "h264", WidthMax: 1920, HeightMax: 1080, BitrateKbps: 5000}

	smallCost := ForPreset(small)
	mediumCost := ForPreset(medium)
	heavyCost := ForPreset(heavy)

	if !(smallCost < mediumCost && mediumCost < heavyCost) {
		t.Fatalf("cost ordering wrong: small=%d medium=%d heavy=%d", smallCost, mediumCost, heavyCost)
	}
}

func TestForLivePresetCostsMoreThanBatch(t *testing.T) {
	t.Parallel()
	p := types.Preset{Name: "720p", Codec: "h264", WidthMax: 1280, HeightMax: 720, BitrateKbps: 2500}
	if ForLivePreset(p) <= ForPreset(p) {
		t.Fatalf("live cost=%d batch cost=%d", ForLivePreset(p), ForPreset(p))
	}
}

func TestModelScalesCosts(t *testing.T) {
	t.Parallel()
	p := types.Preset{Name: "720p", Codec: "h264", WidthMax: 1280, HeightMax: 720, BitrateKbps: 2500}
	base := DefaultModel()
	scaled := Model{BatchScale: 1.5, LiveScale: 1.75}
	if scaled.ForPreset(p) <= base.ForPreset(p) {
		t.Fatalf("scaled batch cost=%d base=%d", scaled.ForPreset(p), base.ForPreset(p))
	}
	if scaled.ForLivePreset(p) <= base.ForLivePreset(p) {
		t.Fatalf("scaled live cost=%d base=%d", scaled.ForLivePreset(p), base.ForLivePreset(p))
	}
}

func TestModelNormalizedDefaultsInvalidScales(t *testing.T) {
	t.Parallel()
	got := (Model{}).Normalized()
	want := DefaultModel()
	if got != want {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestDefaultForInvalidPresetIsConservative(t *testing.T) {
	t.Parallel()
	if got := ForPreset(types.Preset{}); got != Default() {
		t.Fatalf("got=%d want=%d", got, Default())
	}
}
