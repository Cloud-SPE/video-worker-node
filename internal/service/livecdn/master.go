package livecdn

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/hls"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// WriteMaster builds the live master.m3u8 from the encode ladder and
// uploads it via the Mirror's sink. Called once at session-active time
// since the rendition set is fixed for the duration of the live stream.
func WriteMaster(ctx context.Context, m *Mirror, ladder []types.Preset) error {
	if m == nil {
		return nil
	}
	rends := make([]hls.Rendition, 0, len(ladder))
	for _, p := range ladder {
		rends = append(rends, hls.Rendition{
			Name:        p.Name,
			BitrateBps:  p.BitrateKbps * 1000,
			ResolutionW: p.WidthMax,
			ResolutionH: p.HeightMax,
			Codec:       p.Codec,
			URI:         filepath.ToSlash(filepath.Join(p.Codec, p.Name, "playlist.m3u8")),
		})
	}
	body, err := hls.BuildMaster(rends)
	if err != nil {
		return err
	}
	return m.Sink.Put(ctx, m.MasterKey(), "application/vnd.apple.mpegurl", strings.NewReader(body))
}

// MasterBytes is exported as a convenience for tests that want to assert
// the master content without going through a sink.
func MasterBytes(ladder []types.Preset) ([]byte, error) {
	rends := make([]hls.Rendition, 0, len(ladder))
	for _, p := range ladder {
		rends = append(rends, hls.Rendition{
			Name:        p.Name,
			BitrateBps:  p.BitrateKbps * 1000,
			ResolutionW: p.WidthMax,
			ResolutionH: p.HeightMax,
			Codec:       p.Codec,
			URI:         filepath.ToSlash(filepath.Join(p.Codec, p.Name, "playlist.m3u8")),
		})
	}
	s, err := hls.BuildMaster(rends)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(s)
	return buf.Bytes(), nil
}
