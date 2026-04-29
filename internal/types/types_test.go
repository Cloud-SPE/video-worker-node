package types

import (
	"strings"
	"testing"
)

func TestModeValidate(t *testing.T) {
	t.Parallel()
	for _, m := range []Mode{ModeVOD, ModeABR, ModeLive} {
		if err := m.Validate(); err != nil {
			t.Errorf("%v: unexpected error %v", m, err)
		}
	}
	if err := Mode("bogus").Validate(); err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !ModeVOD.IsVOD() || ModeVOD.IsABR() || ModeVOD.IsLive() {
		t.Fatal("ModeVOD predicates wrong")
	}
	if !ModeABR.IsABR() {
		t.Fatal("ModeABR.IsABR")
	}
	if !ModeLive.IsLive() {
		t.Fatal("ModeLive.IsLive")
	}
	if got := ModeVOD.String(); got != "vod" {
		t.Errorf("String=%q want vod", got)
	}
}

func TestJobPhaseTerminal(t *testing.T) {
	t.Parallel()
	for _, p := range []JobPhase{PhaseComplete, PhaseError} {
		if !p.IsTerminal() {
			t.Errorf("phase %q must be terminal", p)
		}
	}
	for _, p := range []JobPhase{PhaseQueued, PhaseDownloading, PhaseProbing, PhaseEncoding, PhaseUploading} {
		if p.IsTerminal() {
			t.Errorf("phase %q must NOT be terminal", p)
		}
	}
}

func TestStreamPhaseTerminal(t *testing.T) {
	t.Parallel()
	terminals := []StreamPhase{StreamPhaseClosed, StreamPhaseEncoderFailed, StreamPhaseBalanceExhausted}
	for _, p := range terminals {
		if !p.IsTerminal() {
			t.Errorf("stream phase %q must be terminal", p)
		}
	}
	non := []StreamPhase{StreamPhaseStarting, StreamPhaseStreaming, StreamPhaseLowBalance, StreamPhasePaymentLost, StreamPhaseClosing}
	for _, p := range non {
		if p.IsTerminal() {
			t.Errorf("stream phase %q must NOT be terminal", p)
		}
	}
}

func TestGPUVendorValidate(t *testing.T) {
	t.Parallel()
	for _, v := range []GPUVendor{GPUVendorAuto, GPUVendorNVIDIA, GPUVendorIntel, GPUVendorAMD, GPUVendorNone} {
		if err := v.Validate(); err != nil {
			t.Errorf("%v: %v", v, err)
		}
	}
	if err := GPUVendor("bogus").Validate(); err == nil {
		t.Fatal("expected error for unknown vendor")
	}
	if got := GPUVendorNVIDIA.String(); got != "nvidia" {
		t.Errorf("String=%q want nvidia", got)
	}
}

func TestPresetValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		p       Preset
		wantErr string
	}{
		{name: "ok", p: Preset{Name: "p", Codec: "h264", WidthMax: 1280, HeightMax: 720, BitrateKbps: 2500}},
		{name: "empty name", p: Preset{Codec: "h264", WidthMax: 1, HeightMax: 1, BitrateKbps: 1}, wantErr: "name"},
		{name: "bad codec", p: Preset{Name: "p", Codec: "wmv", WidthMax: 1, HeightMax: 1, BitrateKbps: 1}, wantErr: "codec"},
		{name: "zero w", p: Preset{Name: "p", Codec: "h264", WidthMax: 0, HeightMax: 1, BitrateKbps: 1}, wantErr: "width_max"},
		{name: "zero br", p: Preset{Name: "p", Codec: "h264", WidthMax: 1, HeightMax: 1, BitrateKbps: 0}, wantErr: "bitrate"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestPresetSupportedBy(t *testing.T) {
	t.Parallel()
	g := GPUProfile{SupportsH264: true, SupportsHEVC: false, SupportsAV1: true}
	if !(Preset{Codec: "h264"}).SupportedBy(g) {
		t.Error("h264 should be supported")
	}
	if (Preset{Codec: "hevc"}).SupportedBy(g) {
		t.Error("hevc should not be supported")
	}
	if !(Preset{Codec: "av1"}).SupportedBy(g) {
		t.Error("av1 should be supported")
	}
	if (Preset{Codec: "junk"}).SupportedBy(g) {
		t.Error("junk codec should not be supported")
	}
}

func TestPresetCatalogueLookupAndFilter(t *testing.T) {
	t.Parallel()
	cat := PresetCatalogue{Presets: []Preset{
		{Name: "p1", Codec: "h264", WidthMax: 1, HeightMax: 1, BitrateKbps: 1},
		{Name: "p2", Codec: "hevc", WidthMax: 1, HeightMax: 1, BitrateKbps: 1},
	}}
	if _, ok := cat.Lookup("p1"); !ok {
		t.Fatal("Lookup p1")
	}
	if _, ok := cat.Lookup("nope"); ok {
		t.Fatal("Lookup nope should fail")
	}
	g := GPUProfile{SupportsH264: true}
	got := cat.Filter(g)
	if len(got) != 1 || got[0].Name != "p1" {
		t.Fatalf("Filter=%+v want [p1]", got)
	}
}

func TestJobError(t *testing.T) {
	t.Parallel()
	e := &JobError{Code: ErrCodeEncodingFailed, Message: "boom"}
	if !strings.Contains(e.Error(), "ENCODING_FAILED") {
		t.Fatalf("missing code: %v", e)
	}
	e2 := &JobError{Code: ErrCodeEncodingFailed, Message: "boom", ExitCode: 7}
	if !strings.Contains(e2.Error(), "exit 7") {
		t.Fatalf("missing exit code: %v", e2)
	}
}
