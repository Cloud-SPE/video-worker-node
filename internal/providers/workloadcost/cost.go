// Package workloadcost estimates relative GPU admission cost for presets.
//
// The model is intentionally simple in the first cut: one scheduler slot
// maps to 100 cost units, and presets consume a fraction of that slot
// budget based on resolution, bitrate, codec, and workload class. This is
// not a full VRAM model; it is a stable stepping stone that lets the
// unified worker distinguish light and heavy jobs.
package workloadcost

import (
	"math"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

const (
	// UnitsPerSlot is the scheduler cost capacity assigned to one slot.
	UnitsPerSlot  = 100
	minPresetCost = 20
)

// Model tunes how preset cost estimates are scaled for one host.
type Model struct {
	BatchScale float64
	LiveScale  float64
}

// DefaultModel returns the built-in host-neutral cost model.
func DefaultModel() Model {
	return Model{
		BatchScale: 1.0,
		LiveScale:  1.25,
	}
}

// Normalized fills unset or invalid scales with defaults.
func (m Model) Normalized() Model {
	def := DefaultModel()
	if m.BatchScale <= 0 {
		m.BatchScale = def.BatchScale
	}
	if m.LiveScale <= 0 {
		m.LiveScale = def.LiveScale
	}
	return m
}

// ForPreset returns a batch-work cost estimate for one preset.
func ForPreset(p types.Preset) int {
	return DefaultModel().ForPreset(p)
}

// ForLivePreset returns a live-work cost estimate for one preset.
func ForLivePreset(p types.Preset) int {
	return DefaultModel().ForLivePreset(p)
}

// Default returns a conservative fallback when the preset is unknown.
func Default() int { return UnitsPerSlot }

// ForPreset returns a batch-work cost estimate for one preset.
func (m Model) ForPreset(p types.Preset) int {
	return estimate(p, m.Normalized().BatchScale)
}

// ForLivePreset returns a live-work cost estimate for one preset.
func (m Model) ForLivePreset(p types.Preset) int {
	return estimate(p, m.Normalized().LiveScale)
}

func estimate(p types.Preset, workloadMultiplier float64) int {
	if p.WidthMax <= 0 || p.HeightMax <= 0 || p.BitrateKbps <= 0 {
		return Default()
	}
	megapixels := float64(p.WidthMax*p.HeightMax) / 1_000_000.0
	bitrateMbps := float64(p.BitrateKbps) / 1000.0

	codecMultiplier := 1.0
	switch p.Codec {
	case "hevc":
		codecMultiplier = 1.15
	case "av1":
		codecMultiplier = 1.30
	}

	cost := (megapixels*32.0 + bitrateMbps*6.0 + 8.0) * codecMultiplier * workloadMultiplier
	return clampCost(int(math.Ceil(cost)))
}

func clampCost(v int) int {
	if v < minPresetCost {
		return minPresetCost
	}
	if v > UnitsPerSlot {
		return UnitsPerSlot
	}
	return v
}
