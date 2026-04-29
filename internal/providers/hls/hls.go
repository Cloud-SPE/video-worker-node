// Package hls assembles HLS master + per-rendition manifests after ABR
// encoding. We deliberately keep this small — fMP4 byte-range HLS is the
// v1 format; CMAF / DASH live elsewhere.
package hls

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Rendition is one ABR variant in the master manifest.
type Rendition struct {
	Name        string
	BitrateBps  int
	ResolutionW int
	ResolutionH int
	Codec       string // "h264" / "hevc" / "av1"
	URI         string // playlist relative path
}

// CodecsAttribute returns the comma-separated CODECS string for the
// EXT-X-STREAM-INF tag. Heuristic — covers the v1 codec set.
func (r Rendition) CodecsAttribute() string {
	switch r.Codec {
	case "h264":
		return "avc1.640028,mp4a.40.2"
	case "hevc":
		return "hvc1.1.6.L93.B0,mp4a.40.2"
	case "av1":
		return "av01.0.05M.08,mp4a.40.2"
	}
	return "mp4a.40.2"
}

// BuildMaster constructs the master playlist text from a set of renditions.
//
// Renditions are sorted by ascending BitrateBps for deterministic output.
func BuildMaster(rends []Rendition) (string, error) {
	if len(rends) == 0 {
		return "", errors.New("hls: no renditions")
	}
	sorted := make([]Rendition, len(rends))
	copy(sorted, rends)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BitrateBps < sorted[j].BitrateBps })
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	for _, r := range sorted {
		if r.URI == "" {
			return "", fmt.Errorf("hls: rendition %q has empty URI", r.Name)
		}
		fmt.Fprintf(&b, `#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS="%s"`+"\n",
			r.BitrateBps, r.ResolutionW, r.ResolutionH, r.CodecsAttribute())
		fmt.Fprintf(&b, "%s\n", r.URI)
	}
	return b.String(), nil
}
