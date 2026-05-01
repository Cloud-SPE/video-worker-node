// Package http is the public HTTP TCP surface for job submission.
//
// Lifted from livepeer-modules/transcode-worker-node with the
// Trickle-specific Channel field removed from /stream/start (live ingest
// is RTMP at MVP per plan 0002 — broadcasters connect directly to
// rtmp://host:1935/live/{stream_key}). The /stream/* HTTP endpoints exist
// for operator pre-registration + status / topup / stop.
//
// VOD + ABR endpoints are byoc-compatible and unchanged from source.
package http

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/probe"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/abrrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/jobrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Server is the HTTP entry layer.
type Server struct {
	mode               types.Mode
	dev                bool
	repo               *jobs.Repo
	jobRunner          *jobrunner.Runner
	abrRunner          *abrrunner.Runner
	liveRunner         *liverunner.Runner
	payment            paymentbroker.Broker
	presets            *presetloader.Loader
	prober             probe.Prober
	apiVersion         int32
	protocolVersion    int32
	workerEthAddress   string
	offeringsAuthToken string
	logger             *slog.Logger
	publicRTMP         string
	maxConc            int
	registryCaps       []config.RegistryCapability
}

// Config wires the Server.
type Config struct {
	Mode                 types.Mode
	Dev                  bool
	Repo                 *jobs.Repo
	JobRunner            *jobrunner.Runner
	ABRRunner            *abrrunner.Runner
	LiveRunner           *liverunner.Runner
	Payment              paymentbroker.Broker
	Presets              *presetloader.Loader
	Prober               probe.Prober
	APIVersion           int32
	ProtocolVersion      int32
	WorkerEthAddress     string
	RegistryCapabilities []config.RegistryCapability
	AuthToken            string
	Logger               *slog.Logger
	PublicRTMPURL        string // e.g., rtmp://ingest.example.com:1935/live
	MaxConcurrent        int    // for /health reporting
}

// New constructs a Server.
func New(cfg Config) (*Server, error) {
	if cfg.Repo == nil {
		return nil, errors.New("http: Repo is required")
	}
	if cfg.Presets == nil {
		return nil, errors.New("http: Presets is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Server{
		mode: cfg.Mode, dev: cfg.Dev, repo: cfg.Repo,
		jobRunner: cfg.JobRunner, abrRunner: cfg.ABRRunner, liveRunner: cfg.LiveRunner,
		payment: cfg.Payment, presets: cfg.Presets, prober: cfg.Prober,
		apiVersion:         cfg.APIVersion,
		protocolVersion:    cfg.ProtocolVersion,
		workerEthAddress:   cfg.WorkerEthAddress,
		offeringsAuthToken: cfg.AuthToken,
		logger:             cfg.Logger,
		publicRTMP:         cfg.PublicRTMPURL,
		maxConc:            cfg.MaxConcurrent,
		registryCaps:       cloneRegistryCapabilities(cfg.RegistryCapabilities),
	}, nil
}

// Handler returns the multiplexed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /registry/offerings", s.handleRegistryOfferings)
	mux.HandleFunc("GET /v1/video/transcode/presets", s.handleListPresets)
	mux.HandleFunc("POST /v1/video/transcode/probe", s.handleProbe)
	mux.HandleFunc("POST /v1/video/transcode", s.handleVODSubmit)
	mux.HandleFunc("POST /v1/video/transcode/status", s.handleVODStatus)
	mux.HandleFunc("POST /v1/video/transcode/abr", s.handleABRSubmit)
	mux.HandleFunc("POST /v1/video/transcode/abr/status", s.handleABRStatus)
	mux.HandleFunc("POST /stream/start", s.handleStreamStart)
	mux.HandleFunc("POST /stream/stop", s.handleStreamStop)
	mux.HandleFunc("POST /stream/topup", s.handleStreamTopup)
	mux.HandleFunc("POST /stream/status", s.handleStreamStatus)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]any{
		"status":           "ok",
		"mode":             string(s.mode),
		"dev":              s.dev,
		"api_version":      s.apiVersion,
		"protocol_version": s.protocolVersion,
		"max_concurrent":   s.maxConc,
	}
	if s.liveRunner != nil {
		resp["active_streams"] = s.liveRunner.ActiveCount()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRegistryOfferings(w http.ResponseWriter, r *http.Request) {
	if s.offeringsAuthToken != "" {
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.offeringsAuthToken
		if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
			return
		}
	}
	caps := make([]map[string]any, 0, len(s.registryCaps))
	for _, capability := range s.registryCaps {
		projectedCapability := map[string]any{
			"name":      capability.Name,
			"work_unit": capability.WorkUnit,
			"offerings": make([]map[string]any, 0, len(capability.Offerings)),
		}
		if capability.Extra != nil {
			projectedCapability["extra"] = capability.Extra
		}
		for _, offering := range capability.Offerings {
			projectedOffering := map[string]any{
				"id":                      offering.ID,
				"price_per_work_unit_wei": offering.PricePerWorkUnitWei,
			}
			if offering.Constraints != nil {
				projectedOffering["constraints"] = offering.Constraints
			}
			projectedCapability["offerings"] = append(projectedCapability["offerings"].([]map[string]any), projectedOffering)
		}
		caps = append(caps, projectedCapability)
	}
	resp := map[string]any{"capabilities": caps}
	if s.workerEthAddress != "" {
		resp["worker_eth_address"] = s.workerEthAddress
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleListPresets(w http.ResponseWriter, _ *http.Request) {
	cat := s.presets.Catalogue()
	writeJSON(w, http.StatusOK, cat)
}

// ProbeRequest is the body for POST /v1/video/transcode/probe.
type ProbeRequest struct {
	InputURL string `json:"input_url"`
	JobID    string `json:"job_id,omitempty"`
}

// ProbeResponse mirrors the engine's ProbeResult shape (snake_case JSON
// keys). The caller — the shell's orchestrator — maps to camelCase.
type ProbeResponse struct {
	DurationSec float64 `json:"duration_sec"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FrameRate   float64 `json:"frame_rate"`
	AudioCodec  string  `json:"audio_codec"`
	VideoCodec  string  `json:"video_codec"`
	Raw         any     `json:"raw,omitempty"`
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	var req ProbeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.InputURL == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "input_url is required")
		return
	}
	if s.prober == nil {
		writeError(w, http.StatusServiceUnavailable, "no_prober", "probe is not wired")
		return
	}
	res, err := s.prober.Probe(r.Context(), req.InputURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "probe_failed", err.Error())
		return
	}
	// The probe.Result shape doesn't currently carry frameRate /
	// audioCodec / videoCodec — those are tracked as tech-debt. For now
	// the worker reports best-effort defaults; plan 0007 hardens.
	writeJSON(w, http.StatusOK, ProbeResponse{
		DurationSec: res.DurationSeconds,
		Width:       res.Width,
		Height:      res.Height,
		FrameRate:   30,     // placeholder; ffprobe -show_streams gives us avg_frame_rate
		AudioCodec:  "aac",  // placeholder
		VideoCodec:  "h264", // placeholder
		Raw:         res,
	})
}

// VODSubmitRequest is the public request body for POST /v1/video/transcode.
type VODSubmitRequest struct {
	JobID         string `json:"job_id"`
	InputURL      string `json:"input_url"`
	OutputURL     string `json:"output_url"`
	Preset        string `json:"preset"`
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	WorkID        string `json:"work_id,omitempty"`
	UnitsPer      int64  `json:"units_per_segment,omitempty"`
	PaymentTicket string `json:"payment_ticket,omitempty"` // base64
}

func (s *Server) handleVODSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.mode.IsVOD() {
		writeError(w, http.StatusNotImplemented, "wrong_mode", "daemon is not in vod mode")
		return
	}
	var req VODSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.JobID == "" || req.InputURL == "" || req.OutputURL == "" || req.Preset == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "job_id, input_url, output_url, preset are required")
		return
	}
	sender, balance, code := s.maybeProcessPayment(r.Context(), req.PaymentTicket, req.WorkID)
	if code != 0 {
		writeError(w, code, "payment", "ticket validation failed")
		return
	}
	if s.jobRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "no_runner", "vod runner is not wired")
		return
	}
	job, err := s.jobRunner.Submit(r.Context(), types.Job{
		ID: req.JobID, Mode: types.ModeVOD,
		InputURL: req.InputURL, OutputURL: req.OutputURL, Preset: req.Preset,
		WebhookURL: req.WebhookURL, WebhookSecret: req.WebhookSecret,
		WorkID: req.WorkID, Sender: sender, UnitsPer: req.UnitsPer,
	})
	if err != nil {
		var je *types.JobError
		if errors.As(err, &je) && je.Code == types.ErrCodeJobInvalidPreset {
			writeError(w, http.StatusBadRequest, je.Code, je.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, "submit_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":  job.ID,
		"phase":   string(job.Phase),
		"work_id": req.WorkID,
		"balance": balance,
	})
}

// VODStatusRequest is the public request body for /v1/video/transcode/status.
type VODStatusRequest struct {
	JobID string `json:"job_id"`
}

func (s *Server) handleVODStatus(w http.ResponseWriter, r *http.Request) {
	var req VODStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	job, err := s.repo.Get(r.Context(), req.JobID)
	if err != nil {
		writeError(w, http.StatusNotFound, types.ErrCodeJobNotFound, req.JobID)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// ABRSubmitRequest is the body for POST /v1/video/transcode/abr.
type ABRSubmitRequest struct {
	JobID            string            `json:"job_id"`
	InputURL         string            `json:"input_url"`
	MasterOutputURL  string            `json:"master_output_url"`
	PresetNames      []string          `json:"presets"`
	RenditionOutputs map[string]string `json:"rendition_outputs"`
	WebhookURL       string            `json:"webhook_url,omitempty"`
	WebhookSecret    string            `json:"webhook_secret,omitempty"`
	WorkID           string            `json:"work_id,omitempty"`
	UnitsPerRend     int64             `json:"units_per_rendition,omitempty"`
	PaymentTicket    string            `json:"payment_ticket,omitempty"`
}

func (s *Server) handleABRSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.mode.IsABR() {
		writeError(w, http.StatusNotImplemented, "wrong_mode", "daemon is not in abr mode")
		return
	}
	var req ABRSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.JobID == "" || req.InputURL == "" || len(req.PresetNames) == 0 {
		writeError(w, http.StatusBadRequest, "missing_fields", "job_id, input_url, presets are required")
		return
	}
	sender, balance, code := s.maybeProcessPayment(r.Context(), req.PaymentTicket, req.WorkID)
	if code != 0 {
		writeError(w, code, "payment", "ticket validation failed")
		return
	}
	if s.abrRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "no_runner", "abr runner is not wired")
		return
	}
	plan := abrrunner.ABRJob{
		JobID: req.JobID, InputURL: req.InputURL, MasterOutputURL: req.MasterOutputURL,
		WebhookURL: req.WebhookURL, WebhookSecret: req.WebhookSecret,
		WorkID: req.WorkID, Sender: sender, UnitsPerRend: req.UnitsPerRend,
		PresetNames: req.PresetNames, RenditionOutputs: req.RenditionOutputs,
	}
	if err := s.abrRunner.Submit(r.Context(), plan); err != nil {
		var je *types.JobError
		if errors.As(err, &je) && je.Code == types.ErrCodeJobInvalidPreset {
			writeError(w, http.StatusBadRequest, je.Code, je.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, "submit_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":  req.JobID,
		"balance": balance,
	})
}

func (s *Server) handleABRStatus(w http.ResponseWriter, r *http.Request) {
	var req VODStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	job, err := s.repo.Get(r.Context(), req.JobID)
	if err != nil {
		writeError(w, http.StatusNotFound, types.ErrCodeJobNotFound, req.JobID)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// StreamStartRequest is the body for POST /stream/start.
//
// New (Trickle-free) shape: the broadcaster does NOT supply Channel /
// SubscribeURL / PublishURL — they connect to the public RTMP URL the
// worker advertises, with the stream key in the URL path.
//
// /stream/start exists for operator pre-registration: tell the worker
// to expect a stream with this work_id + payment ticket. Plan 0006 may
// further refine this shape (or move it shell-side entirely).
type StreamStartRequest struct {
	WorkID        string `json:"work_id"`
	Preset        string `json:"preset"`
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	PaymentTicket string `json:"payment_ticket,omitempty"`
}

// StreamStartResponse returns the RTMP URL the broadcaster should connect to.
type StreamStartResponse struct {
	WorkID  string       `json:"work_id"`
	RTMPURL string       `json:"rtmp_url"`
	Stream  types.Stream `json:"stream"`
}

func (s *Server) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	if !s.mode.IsLive() {
		writeError(w, http.StatusNotImplemented, "wrong_mode", "daemon is not in live mode")
		return
	}
	var req StreamStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.WorkID == "" || req.Preset == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "work_id, preset required")
		return
	}
	if s.liveRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "no_runner", "live runner is not wired")
		return
	}
	ticket, err := decodeTicket(req.PaymentTicket)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_ticket", err.Error())
		return
	}
	stream, err := s.liveRunner.Start(r.Context(), liverunner.StartRequest{
		WorkID: req.WorkID, PaymentTicket: ticket, Preset: req.Preset,
		WebhookURL: req.WebhookURL, WebhookSecret: req.WebhookSecret,
	})
	if err != nil {
		var je *types.JobError
		if errors.As(err, &je) {
			switch je.Code {
			case types.ErrCodeInvalidPayment, types.ErrCodeInsufficientBalance:
				writeError(w, http.StatusPaymentRequired, je.Code, je.Message)
				return
			case types.ErrCodeJobInvalidPreset:
				writeError(w, http.StatusBadRequest, je.Code, je.Message)
				return
			}
		}
		writeError(w, http.StatusInternalServerError, "start_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, StreamStartResponse{
		WorkID:  req.WorkID,
		RTMPURL: s.publicRTMP,
		Stream:  stream,
	})
}

// StreamStopRequest is the body for POST /stream/stop.
type StreamStopRequest struct {
	WorkID string `json:"work_id"`
}

func (s *Server) handleStreamStop(w http.ResponseWriter, r *http.Request) {
	if !s.mode.IsLive() {
		writeError(w, http.StatusNotImplemented, "wrong_mode", "daemon is not in live mode")
		return
	}
	var req StreamStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if err := s.liveRunner.Stop(r.Context(), req.WorkID); err != nil {
		var je *types.JobError
		if errors.As(err, &je) && je.Code == types.ErrCodeStreamNotFound {
			writeError(w, http.StatusNotFound, je.Code, je.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, "stop_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StreamTopupRequest is the body for POST /stream/topup.
type StreamTopupRequest struct {
	WorkID        string `json:"work_id"`
	PaymentTicket string `json:"payment_ticket"`
}

func (s *Server) handleStreamTopup(w http.ResponseWriter, r *http.Request) {
	if !s.mode.IsLive() {
		writeError(w, http.StatusNotImplemented, "wrong_mode", "daemon is not in live mode")
		return
	}
	var req StreamTopupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	ticket, err := decodeTicket(req.PaymentTicket)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_ticket", err.Error())
		return
	}
	if err := s.liveRunner.Topup(r.Context(), req.WorkID, ticket); err != nil {
		var je *types.JobError
		if errors.As(err, &je) {
			switch je.Code {
			case types.ErrCodeStreamNotFound:
				writeError(w, http.StatusNotFound, je.Code, je.Message)
			case types.ErrCodeTopupRateLimited:
				writeError(w, http.StatusTooManyRequests, je.Code, je.Message)
			case types.ErrCodeInvalidPayment:
				writeError(w, http.StatusPaymentRequired, je.Code, je.Message)
			default:
				writeError(w, http.StatusInternalServerError, je.Code, je.Message)
			}
			return
		}
		writeError(w, http.StatusInternalServerError, "topup_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// StreamStatusRequest is the body for POST /stream/status.
type StreamStatusRequest struct {
	WorkID string `json:"work_id"`
}

func (s *Server) handleStreamStatus(w http.ResponseWriter, r *http.Request) {
	var req StreamStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	stream, err := s.repo.GetStream(r.Context(), req.WorkID)
	if err != nil {
		writeError(w, http.StatusNotFound, types.ErrCodeStreamNotFound, req.WorkID)
		return
	}
	writeJSON(w, http.StatusOK, stream)
}

// maybeProcessPayment runs ProcessPayment if a ticket is present and
// payment is wired. Returns (sender, balance, statusCode). Returns
// statusCode=0 for "ok / skipped".
func (s *Server) maybeProcessPayment(ctx context.Context, ticketB64, workID string) ([]byte, []byte, int) {
	if ticketB64 == "" || s.payment == nil || s.dev {
		return nil, nil, 0
	}
	ticket, err := decodeTicket(ticketB64)
	if err != nil {
		return nil, nil, http.StatusBadRequest
	}
	r, err := s.payment.ProcessPayment(ctx, ticket, workID)
	if err != nil {
		return nil, nil, http.StatusPaymentRequired
	}
	return r.Sender, r.BalanceWei, 0
}

func decodeTicket(ticketB64 string) ([]byte, error) {
	if ticketB64 == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(ticketB64)
	if err != nil {
		return nil, fmt.Errorf("bad base64: %w", err)
	}
	return b, nil
}

// writeJSON sends a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends a JSON error body with the given status and code.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"code": code, "message": message,
	})
}

func cloneRegistryCapabilities(in []config.RegistryCapability) []config.RegistryCapability {
	if len(in) == 0 {
		return nil
	}
	out := make([]config.RegistryCapability, 0, len(in))
	for _, capability := range in {
		out = append(out, capability.Clone())
	}
	return out
}
