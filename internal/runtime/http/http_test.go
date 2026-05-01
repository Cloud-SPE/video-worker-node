// Package http tests.
//
// These tests cover the wiring + middleware + Trickle-free /stream/start
// shape. Deeper VOD/ABR/Live behavioral tests live in their respective
// runner packages (jobrunner / abrrunner / liverunner).
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func newTestServer(t *testing.T, mode types.Mode) *Server {
	t.Helper()
	repo := jobs.New(store.Memory())
	presetYAML := []byte(`presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2800
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "preset.yaml")
	if err := os.WriteFile(path, presetYAML, 0o644); err != nil {
		t.Fatalf("preset write: %v", err)
	}
	pl, err := presetloader.New(path)
	if err != nil {
		t.Fatalf("presets: %v", err)
	}
	lr, err := liverunner.New(liverunner.Config{Repo: repo, Presets: pl})
	if err != nil {
		t.Fatalf("liverunner: %v", err)
	}
	srv, err := New(Config{
		Mode: mode, Dev: true, Repo: repo, Presets: pl, LiveRunner: lr,
		APIVersion:       7,
		ProtocolVersion:  11,
		WorkerEthAddress: "0x1234567890abcdef1234567890abcdef12345678",
		PublicRTMPURL:    "rtmp://localhost:1935/live",
		MaxConcurrent:    4,
		RegistryCapabilities: []config.RegistryCapability{
			{
				Name:     "video:transcode.vod",
				WorkUnit: "video_frame_megapixel",
				Extra:    map[string]any{"vendor": "nvenc"},
				Offerings: []config.RegistryOffering{
					{
						ID:                  "h264-1080p",
						PricePerWorkUnitWei: "1250000",
						BackendURL:          "http://127.0.0.1:9000",
						Constraints:         map[string]any{"preset": "h264-1080p"},
					},
				},
			},
			{
				Name:     "video:live.rtmp",
				WorkUnit: "video_frame_megapixel",
				Offerings: []config.RegistryOffering{
					{
						ID:                  "live-h264",
						PricePerWorkUnitWei: "2500000",
						BackendURL:          "http://127.0.0.1:1935",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	return srv
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(t, types.ModeLive)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status: %v", body["status"])
	}
	if body["api_version"] != float64(7) {
		t.Fatalf("api_version: %v", body["api_version"])
	}
	if body["protocol_version"] != float64(11) {
		t.Fatalf("protocol_version: %v", body["protocol_version"])
	}
}

func TestHandleRegistryOfferings(t *testing.T) {
	srv := newTestServer(t, types.ModeLive)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/registry/offerings", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["worker_eth_address"] != "0x1234567890abcdef1234567890abcdef12345678" {
		t.Fatalf("worker_eth_address: %v", body["worker_eth_address"])
	}
	caps, ok := body["capabilities"].([]any)
	if !ok || len(caps) != 2 {
		t.Fatalf("capabilities: %v", body["capabilities"])
	}
	first, ok := caps[0].(map[string]any)
	if !ok {
		t.Fatalf("first capability: %T", caps[0])
	}
	if first["name"] != "video:transcode.vod" {
		t.Fatalf("name: %v", first["name"])
	}
	if _, hasBackendURL := first["backend_url"]; hasBackendURL {
		t.Fatal("capability should not expose backend_url")
	}
	offerings, ok := first["offerings"].([]any)
	if !ok || len(offerings) != 1 {
		t.Fatalf("offerings: %v", first["offerings"])
	}
	offering, ok := offerings[0].(map[string]any)
	if !ok {
		t.Fatalf("offering: %T", offerings[0])
	}
	if _, hasBackendURL := offering["backend_url"]; hasBackendURL {
		t.Fatal("offering should not expose backend_url")
	}
}

func TestHandleStreamStartTrickleFreeShape(t *testing.T) {
	srv := newTestServer(t, types.ModeLive)
	body := `{"work_id":"w-1","preset":"720p"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/stream/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp StreamStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkID != "w-1" {
		t.Fatalf("work_id=%s", resp.WorkID)
	}
	if resp.RTMPURL != "rtmp://localhost:1935/live" {
		t.Fatalf("rtmp_url=%s", resp.RTMPURL)
	}
}

func TestHandleStreamStartRejectsLegacyChannelFields(t *testing.T) {
	// The new shape has no SubscribeURL/PublishURL. Sending them is harmless
	// (they're ignored as unknown JSON keys) but the request must still
	// pass minimum validation (work_id + preset).
	srv := newTestServer(t, types.ModeLive)
	body := `{"work_id":"w-2","preset":"720p","subscribe_url":"ignored","publish_url":"ignored"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/stream/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleStreamStartMissingFields(t *testing.T) {
	srv := newTestServer(t, types.ModeLive)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/stream/start", strings.NewReader(`{}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleStreamStartWrongMode(t *testing.T) {
	srv := newTestServer(t, types.ModeVOD)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/stream/start", strings.NewReader(`{"work_id":"w","preset":"x"}`))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestRegistryOfferingsAuthToken(t *testing.T) {
	srv := newTestServer(t, types.ModeLive)
	srv.offeringsAuthToken = "secret"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/registry/offerings", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/registry/offerings", nil)
	req.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorized status=%d", rec.Code)
	}
}

// silence "imported and not used" if context becomes unused.
var _ = context.Background
