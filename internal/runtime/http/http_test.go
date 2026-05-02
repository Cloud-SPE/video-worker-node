// Package http tests.
//
// These tests cover the wiring + middleware + Trickle-free /stream/start
// shape. Deeper VOD/ABR/Live behavioral tests live in their respective
// runner packages (jobrunner / abrrunner / liverunner).
package http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/ingest"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/paymentclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/shellclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeTicketParamsClient struct {
	params paymentclient.TicketParams
	err    error
	last   paymentclient.GetTicketParamsRequest
}

type fakeIngestSession struct {
	streamKey string
	reader    io.Reader
}

func (s *fakeIngestSession) Protocol() ingest.Protocol { return ingest.ProtocolRTMP }
func (s *fakeIngestSession) StreamKey() string         { return s.streamKey }
func (*fakeIngestSession) MediaFormat() string         { return "flv" }
func (s *fakeIngestSession) Reader() io.Reader         { return s.reader }
func (*fakeIngestSession) RemoteAddr() string          { return "127.0.0.1:54321" }
func (*fakeIngestSession) Close() error                { return nil }

func (f *fakeTicketParamsClient) GetTicketParams(_ context.Context, req paymentclient.GetTicketParamsRequest) (paymentclient.TicketParams, error) {
	f.last = req
	if f.err != nil {
		return paymentclient.TicketParams{}, f.err
	}
	return f.params, nil
}

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

func newPaidLiveTestServer(t *testing.T) (*Server, *paymentbroker.Fake) {
	t.Helper()
	repo := jobs.New(store.Memory())
	pl := mustTestPresets(t)
	lr, err := liverunner.New(liverunner.Config{Repo: repo, Presets: pl})
	if err != nil {
		t.Fatalf("liverunner: %v", err)
	}
	payment := paymentbroker.NewFake()
	srv, err := New(Config{
		Mode:          types.ModeLive,
		Dev:           false,
		Repo:          repo,
		Presets:       pl,
		LiveRunner:    lr,
		Payment:       payment,
		PublicRTMPURL: "rtmp://localhost:1935/live",
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	return srv, payment
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

func TestHandleSessionStartAcceptsPaymentAndReturnsCorrelationFields(t *testing.T) {
	srv, payment := newPaidLiveTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/start", strings.NewReader(`{"gateway_session_id":"gw_123","preset":"720p"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-123")))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp SessionStartResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.GatewaySessionID != "gw_123" {
		t.Fatalf("gateway_session_id=%q", resp.GatewaySessionID)
	}
	if resp.WorkID == "" {
		t.Fatal("empty work_id")
	}
	if resp.WorkerSessionID == "" {
		t.Fatal("empty worker_session_id")
	}
	if resp.RTMPURL != "rtmp://localhost:1935/live" {
		t.Fatalf("rtmp_url=%q", resp.RTMPURL)
	}
	if payment.Balance(resp.WorkID) == 0 {
		t.Fatalf("expected credited balance for work_id %q", resp.WorkID)
	}
	if resp.Stream.GatewaySessionID != "gw_123" {
		t.Fatalf("stream.gateway_session_id=%q", resp.Stream.GatewaySessionID)
	}
	if resp.Stream.WorkerSessionID != resp.WorkerSessionID {
		t.Fatalf("stream.worker_session_id=%q want %q", resp.Stream.WorkerSessionID, resp.WorkerSessionID)
	}
	if resp.Stream.PaymentWorkID != resp.WorkID {
		t.Fatalf("stream.payment_work_id=%q want %q", resp.Stream.PaymentWorkID, resp.WorkID)
	}
}

func TestHandleSessionTopupCreditsExistingWorkID(t *testing.T) {
	srv, payment := newPaidLiveTestServer(t)
	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest("POST", "/api/sessions/start", strings.NewReader(`{"gateway_session_id":"gw_123","preset":"720p"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-123")))
	srv.Handler().ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	var startResp SessionStartResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	before := payment.Balance(startResp.WorkID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/gw_123/topup", nil)
	req.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-topup")))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp SessionTopupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.WorkID != startResp.WorkID {
		t.Fatalf("work_id=%q want %q", resp.WorkID, startResp.WorkID)
	}
	if payment.Balance(startResp.WorkID) <= before {
		t.Fatalf("expected topup to increase balance for %q", startResp.WorkID)
	}
}

func TestHandleSessionEndClosesPaymentSession(t *testing.T) {
	srv, payment := newPaidLiveTestServer(t)
	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest("POST", "/api/sessions/start", strings.NewReader(`{"gateway_session_id":"gw_123","preset":"720p"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-123")))
	srv.Handler().ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	var startResp SessionStartResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/gw_123/end", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !payment.IsClosed(startResp.WorkID) {
		t.Fatalf("expected CloseSession for work_id %q", startResp.WorkID)
	}
	if payment.CloseCount(startResp.WorkID) != 1 {
		t.Fatalf("CloseCount=%d want 1", payment.CloseCount(startResp.WorkID))
	}
}

func TestHandleSessionTopupFallsBackToPersistedSessionInfo(t *testing.T) {
	srv, payment := newPaidLiveTestServer(t)
	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest("POST", "/api/sessions/start", strings.NewReader(`{"gateway_session_id":"gw_123","preset":"720p"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-123")))
	srv.Handler().ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	var startResp SessionStartResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	srv.liveSessions.Delete("gw_123")
	before := payment.Balance(startResp.WorkID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/gw_123/topup", nil)
	req.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-topup")))
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if payment.Balance(startResp.WorkID) <= before {
		t.Fatalf("expected fallback topup to increase balance for %q", startResp.WorkID)
	}
}

func TestHandleSessionEndFallsBackToPersistedSessionInfo(t *testing.T) {
	srv, payment := newPaidLiveTestServer(t)
	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest("POST", "/api/sessions/start", strings.NewReader(`{"gateway_session_id":"gw_123","preset":"720p"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-123")))
	srv.Handler().ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	var startResp SessionStartResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	srv.liveSessions.Delete("gw_123")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/gw_123/end", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !payment.IsClosed(startResp.WorkID) {
		t.Fatalf("expected fallback CloseSession for work_id %q", startResp.WorkID)
	}
}

func TestHandleSessionTopupRejectsTerminalPersistedSession(t *testing.T) {
	srv, _ := newPaidLiveTestServer(t)
	if err := srv.repo.SaveStream(context.Background(), types.Stream{
		WorkID:           "gw_123",
		GatewaySessionID: "gw_123",
		WorkerSessionID:  "worker_123",
		PaymentWorkID:    "work_123",
		Phase:            types.StreamPhaseClosed,
	}); err != nil {
		t.Fatalf("save stream: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/gw_123/topup", nil)
	req.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-topup")))
	srv.liveSessions.Upsert(liveSessionInfo{
		GatewaySessionID: "gw_123",
		WorkerSessionID:  "worker_123",
		WorkID:           "work_123",
		StreamID:         "gw_123",
	})
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := srv.liveSessions.Get("gw_123"); ok {
		t.Fatal("expected stale in-memory session mapping to be removed")
	}
}

func TestHandleSessionEndRejectsTerminalPersistedSession(t *testing.T) {
	srv, _ := newPaidLiveTestServer(t)
	if err := srv.repo.SaveStream(context.Background(), types.Stream{
		WorkID:           "gw_123",
		GatewaySessionID: "gw_123",
		WorkerSessionID:  "worker_123",
		PaymentWorkID:    "work_123",
		Phase:            types.StreamPhaseClosed,
	}); err != nil {
		t.Fatalf("save stream: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/sessions/gw_123/end", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSessionEndAcceptedPatternBClosesPaymentOnce(t *testing.T) {
	repo := jobs.New(store.Memory())
	pl := mustTestPresets(t)
	payment := paymentbroker.NewFake()
	shell := shellclient.NewFake()
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: true, StreamID: "gw_123", ProjectID: "proj_fake", RecordingEnabled: false}, nil
	}
	lr, err := liverunner.New(liverunner.Config{
		Repo:           repo,
		Presets:        pl,
		Payment:        payment,
		Shell:          shell,
		EncoderFactory: liverunner.NewDrainEncoder,
		WorkerURL:      "http://worker:8080",
		LivePreset:     "h264-live",
		DebitCadence:   20 * time.Millisecond,
		RunwaySeconds:  30,
		GraceSeconds:   1,
	})
	if err != nil {
		t.Fatalf("liverunner: %v", err)
	}
	srv, err := New(Config{
		Mode:          types.ModeLive,
		Dev:           false,
		Repo:          repo,
		Presets:       pl,
		LiveRunner:    lr,
		Payment:       payment,
		PublicRTMPURL: "rtmp://localhost:1935/live",
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	startRec := httptest.NewRecorder()
	startReq := httptest.NewRequest("POST", "/api/sessions/start", strings.NewReader(`{"gateway_session_id":"gw_123","preset":"720p"}`))
	startReq.Header.Set("Content-Type", "application/json")
	startReq.Header.Set("livepeer-payment", base64.StdEncoding.EncodeToString([]byte("ticket-123")))
	srv.Handler().ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", startRec.Code, startRec.Body.String())
	}
	var startResp SessionStartResponse
	if err := json.Unmarshal(startRec.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	pr, _ := io.Pipe()
	sess := &fakeIngestSession{streamKey: "sk_live_pattern_b", reader: pr}
	if _, err := lr.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}

	endRec := httptest.NewRecorder()
	endReq := httptest.NewRequest("POST", "/api/sessions/gw_123/end", nil)
	srv.Handler().ServeHTTP(endRec, endReq)
	if endRec.Code != http.StatusOK {
		t.Fatalf("end status=%d body=%s", endRec.Code, endRec.Body.String())
	}
	if !payment.IsClosed(startResp.WorkID) {
		t.Fatalf("expected CloseSession for work_id %q", startResp.WorkID)
	}
	if payment.CloseCount(startResp.WorkID) != 1 {
		t.Fatalf("CloseCount=%d want 1", payment.CloseCount(startResp.WorkID))
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

func TestHandleVODSubmitAcceptsPaymentHeader(t *testing.T) {
	repo := jobs.New(store.Memory())
	pl := mustTestPresets(t)
	srv, err := New(Config{
		Mode:    types.ModeVOD,
		Dev:     false,
		Repo:    repo,
		Presets: pl,
		Payment: paymentbroker.NewFake(),
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/video/transcode", strings.NewReader(`{
		"job_id":"job_1",
		"input_url":"https://example.com/in.mp4",
		"output_url":"s3://bucket/out.m3u8",
		"preset":"720p",
		"work_id":"job_1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(paymentHeaderName, "AQID")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no_runner") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestHandleVODSubmitRequiresPaymentWhenBrokerWired(t *testing.T) {
	repo := jobs.New(store.Memory())
	pl := mustTestPresets(t)
	srv, err := New(Config{
		Mode:    types.ModeVOD,
		Dev:     false,
		Repo:    repo,
		Presets: pl,
		Payment: paymentbroker.NewFake(),
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/video/transcode", strings.NewReader(`{
		"job_id":"job_1",
		"input_url":"https://example.com/in.mp4",
		"output_url":"s3://bucket/out.m3u8",
		"preset":"720p",
		"work_id":"job_1"
	}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleStreamStatusAcceptsStreamID(t *testing.T) {
	srv := newTestServer(t, types.ModeLive)
	if err := srv.repo.SaveStream(context.Background(), types.Stream{
		WorkID:           "live_1",
		GatewaySessionID: "gw_123",
		WorkerSessionID:  "worker_123",
		PaymentWorkID:    "work_123",
		Phase:            types.StreamPhaseLowBalance,
		LowBalance:       true,
	}); err != nil {
		t.Fatalf("save stream: %v", err)
	}

	statusRec := httptest.NewRecorder()
	statusReq := httptest.NewRequest("POST", "/stream/status", strings.NewReader(`{"stream_id":"live_1"}`))
	statusReq.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", statusRec.Code, statusRec.Body.String())
	}
	var resp types.Stream
	if err := json.Unmarshal(statusRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.GatewaySessionID != "gw_123" || resp.WorkerSessionID != "worker_123" || resp.PaymentWorkID != "work_123" {
		t.Fatalf("unexpected correlation fields: %+v", resp)
	}
	if resp.Phase != types.StreamPhaseLowBalance || !resp.LowBalance {
		t.Fatalf("unexpected runtime state: %+v", resp)
	}
}

func TestHandleTicketParamsProxy(t *testing.T) {
	payee := &fakeTicketParamsClient{
		params: paymentclient.TicketParams{
			Recipient:         []byte{0xaa, 0xbb},
			FaceValueWei:      []byte{0x7b},
			WinProb:           []byte{0x10},
			RecipientRandHash: []byte{0x20},
			Seed:              []byte{0x30},
			ExpirationBlock:   []byte{0x01, 0x00},
			ExpirationParams: paymentclient.TicketExpirationParams{
				CreationRound:          42,
				CreationRoundBlockHash: []byte{0xcc},
			},
		},
	}

	repo := jobs.New(store.Memory())
	pl := mustTestPresets(t)
	srv, err := New(Config{
		Mode:      types.ModeVOD,
		Dev:       false,
		Repo:      repo,
		Presets:   pl,
		Payee:     payee,
		AuthToken: "secret",
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/payment/ticket-params", strings.NewReader(`{
		"sender_eth_address":"0x1111111111111111111111111111111111111111",
		"recipient_eth_address":"0x2222222222222222222222222222222222222222",
		"face_value_wei":"123",
		"capability":"video:transcode.vod",
		"offering":"h264-720p"
	}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if payee.last.Capability != "video:transcode.vod" || payee.last.Offering != "h264-720p" {
		t.Fatalf("unexpected request: %+v", payee.last)
	}
	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ticket := body["ticket_params"]
	if ticket["recipient"] != "0xaabb" {
		t.Fatalf("recipient=%v", ticket["recipient"])
	}
	if ticket["face_value"] != "123" {
		t.Fatalf("face_value=%v", ticket["face_value"])
	}
}

func TestHandleTicketParamsProxyMapsDaemonUnavailable(t *testing.T) {
	repo := jobs.New(store.Memory())
	pl := mustTestPresets(t)
	srv, err := New(Config{
		Mode:    types.ModeVOD,
		Dev:     false,
		Repo:    repo,
		Presets: pl,
		Payee: &fakeTicketParamsClient{
			err: status.Error(codes.Unavailable, "daemon down"),
		},
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/payment/ticket-params", strings.NewReader(`{
		"sender_eth_address":"0x1111111111111111111111111111111111111111",
		"recipient_eth_address":"0x2222222222222222222222222222222222222222",
		"face_value_wei":"123",
		"capability":"video:transcode.vod",
		"offering":"h264-720p"
	}`))
	req.Header.Set("Content-Type", "application/json")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "payment_daemon_unavailable") {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func mustTestPresets(t *testing.T) *presetloader.Loader {
	t.Helper()
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
	return pl
}

// silence "imported and not used" if context becomes unused.
var _ = context.Background
