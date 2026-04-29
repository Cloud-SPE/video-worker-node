// Package presetloader reads YAML preset catalogues and validates them
// against a detected GPU profile.
package presetloader

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Loader holds the most recently parsed catalogue. ReloadPresets
// reparses from disk under the lock; concurrent readers see a consistent
// snapshot at all times.
type Loader struct {
	path string
	mu   sync.RWMutex
	cat  types.PresetCatalogue
}

// New returns a Loader backed by `path`. Returns an error if the path
// cannot be parsed.
func New(path string) (*Loader, error) {
	l := &Loader{path: path}
	if err := l.Reload(); err != nil {
		return nil, err
	}
	return l, nil
}

// Reload reparses the YAML catalogue. Returns an error if the file is
// missing, malformed, or contains an invalid preset.
func (l *Loader) Reload() error {
	if l.path == "" {
		return errors.New("presetloader: empty path")
	}
	b, err := os.ReadFile(l.path)
	if err != nil {
		return fmt.Errorf("read %s: %w", l.path, err)
	}
	cat, err := ParseBytes(b)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.cat = cat
	l.mu.Unlock()
	return nil
}

// Catalogue returns the current parsed catalogue (a copy of the slice
// header — the underlying Preset structs are immutable in practice).
func (l *Loader) Catalogue() types.PresetCatalogue {
	l.mu.RLock()
	defer l.mu.RUnlock()
	cp := make([]types.Preset, len(l.cat.Presets))
	copy(cp, l.cat.Presets)
	return types.PresetCatalogue{Presets: cp}
}

// Lookup returns the preset by name; ok==false if missing.
func (l *Loader) Lookup(name string) (types.Preset, bool) {
	return l.Catalogue().Lookup(name)
}

// FilterByGPU returns the subset of presets supported by g.
func (l *Loader) FilterByGPU(g types.GPUProfile) []types.Preset {
	return l.Catalogue().Filter(g)
}

// ParseBytes parses a YAML preset catalogue from a byte buffer and
// validates every entry.
func ParseBytes(b []byte) (types.PresetCatalogue, error) {
	var c types.PresetCatalogue
	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("yaml: %w", err)
	}
	if len(c.Presets) == 0 {
		return c, errors.New("presetloader: empty catalogue")
	}
	for i, p := range c.Presets {
		if err := p.Validate(); err != nil {
			return c, fmt.Errorf("preset[%d]: %w", i, err)
		}
	}
	return c, nil
}
