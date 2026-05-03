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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/paymentclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/probe"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/scheduler"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/abrrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/jobrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const paymentHeaderName = "livepeer-payment"
const maxTicketParamsBodyBytes = 8 << 10 // 8 KiB

type ticketParamsClient interface {
	GetTicketParams(context.Context, paymentclient.GetTicketParamsRequest) (paymentclient.TicketParams, error)
}

// Server is the HTTP entry layer.
type Server struct {
	mode               types.Mode
	dev                bool
	repo               *jobs.Repo
	jobRunner          *jobrunner.Runner
	abrRunner          *abrrunner.Runner
	liveRunner         *liverunner.Runner
	payment            paymentbroker.Broker
	payee              ticketParamsClient
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
	liveSessions       *liveSessionRegistry
	scheduler          scheduler.Controller
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
	Payee                ticketParamsClient
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
	Scheduler            scheduler.Controller
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
		payment: cfg.Payment, payee: cfg.Payee, presets: cfg.Presets, prober: cfg.Prober,
		apiVersion:         cfg.APIVersion,
		protocolVersion:    cfg.ProtocolVersion,
		workerEthAddress:   cfg.WorkerEthAddress,
		offeringsAuthToken: cfg.AuthToken,
		logger:             cfg.Logger,
		publicRTMP:         cfg.PublicRTMPURL,
		maxConc:            cfg.MaxConcurrent,
		registryCaps:       cloneRegistryCapabilities(cfg.RegistryCapabilities),
		liveSessions:       newLiveSessionRegistry(),
		scheduler:          cfg.Scheduler,
	}, nil
}

// Handler returns the multiplexed handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /registry/offerings", s.handleRegistryOfferings)
	mux.HandleFunc("POST /v1/payment/ticket-params", s.handleTicketParams)
	mux.HandleFunc("GET /v1/video/transcode/presets", s.handleListPresets)
	mux.HandleFunc("POST /v1/video/transcode/probe", s.handleProbe)
	mux.HandleFunc("POST /v1/video/transcode", s.handleVODSubmit)
	mux.HandleFunc("POST /v1/video/transcode/status", s.handleVODStatus)
	mux.HandleFunc("POST /v1/video/transcode/abr", s.handleABRSubmit)
	mux.HandleFunc("POST /v1/video/transcode/abr/status", s.handleABRStatus)
	mux.HandleFunc("POST /api/sessions/start", s.handleSessionStart)
	mux.HandleFunc("POST /api/sessions/{gateway_session_id}/topup", s.handleSessionTopup)
	mux.HandleFunc("POST /api/sessions/{gateway_session_id}/end", s.handleSessionEnd)
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
	if s.scheduler != nil {
		snap := s.scheduler.Snapshot()
		resp["gpu_scheduler"] = map[string]any{
			"total_slots":         snap.TotalSlots,
			"live_reserved_slots": snap.LiveReservedSlots,
			"total_cost":          snap.TotalCost,
			"live_reserved_cost":  snap.LiveReservedCost,
			"active_slots":        snap.ActiveSlots,
			"active_batch_slots":  snap.ActiveBatchSlots,
			"active_live_slots":   snap.ActiveLiveSlots,
			"active_cost":         snap.ActiveCost,
			"active_batch_cost":   snap.ActiveBatchCost,
			"active_live_cost":    snap.ActiveLiveCost,
			"queued_batch_jobs":   snap.QueuedBatch,
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRegistryOfferings(w http.ResponseWriter, r *http.Request) {
	if !s.requireBearerAuth(w, r) {
		return
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

type ticketParamsRequest struct {
	SenderETHAddress    string `json:"sender_eth_address"`
	RecipientETHAddress string `json:"recipient_eth_address"`
	FaceValueWei        string `json:"face_value_wei"`
	Capability          string `json:"capability"`
	Offering            string `json:"offering"`
}

type ticketParamsResponse struct {
	TicketParams ticketParamsJSON `json:"ticket_params"`
}

type ticketParamsJSON struct {
	Recipient         string                     `json:"recipient"`
	FaceValue         string                     `json:"face_value"`
	WinProb           string                     `json:"win_prob"`
	RecipientRandHash string                     `json:"recipient_rand_hash"`
	Seed              string                     `json:"seed"`
	ExpirationBlock   string                     `json:"expiration_block"`
	ExpirationParams  ticketExpirationParamsJSON `json:"expiration_params"`
}

type ticketExpirationParamsJSON struct {
	CreationRound          int64  `json:"creation_round"`
	CreationRoundBlockHash string `json:"creation_round_block_hash"`
}

func (s *Server) handleTicketParams(w http.ResponseWriter, r *http.Request) {
	if !s.requireBearerAuth(w, r) {
		return
	}
	if s.payee == nil {
		writeError(w, http.StatusServiceUnavailable, "payment_daemon_unavailable", "ticket params are not wired")
		return
	}
	defer func() { _ = r.Body.Close() }()

	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTicketParamsBodyBytes))
	dec.DisallowUnknownFields()

	var req ticketParamsRequest
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
		return
	}
	if err := ensureSingleJSONDocument(dec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	daemonReq, err := parseTicketParamsRequest(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	params, err := s.payee.GetTicketParams(r.Context(), daemonReq)
	if err != nil {
		s.writeTicketParamsProxyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ticketParamsResponse{
		TicketParams: renderTicketParamsJSON(params),
	})
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
	Offering      string `json:"offering,omitempty"`
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	WorkID        string `json:"work_id,omitempty"`
	UnitsPer      int64  `json:"units_per_segment,omitempty"`
	PaymentTicket string `json:"payment_ticket,omitempty"` // base64
}

func (s *Server) handleVODSubmit(w http.ResponseWriter, r *http.Request) {
	var req VODSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.JobID == "" || req.InputURL == "" || req.OutputURL == "" || req.Preset == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "job_id, input_url, output_url, preset are required")
		return
	}
	workID := firstNonEmpty(req.WorkID, req.JobID)
	if s.payment != nil && !s.dev {
		if _, code, err := s.decodeRequestPayment(r, req.PaymentTicket, true); err != nil {
			if code == http.StatusPaymentRequired {
				writeError(w, code, "payment", "missing payment ticket")
			} else {
				writeError(w, code, "bad_ticket", err.Error())
			}
			return
		}
	}
	if err := s.openSessionForRequest(r.Context(), paymentSessionRequest{
		WorkID:     workID,
		Capability: "video:transcode.vod",
		Preset:     req.Preset,
		Offering:   req.Offering,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "session_binding", err.Error())
		return
	}
	sender, balance, code := s.maybeProcessPayment(r.Context(), r, req.PaymentTicket, workID)
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
		WorkID: workID, Sender: sender, UnitsPer: req.UnitsPer,
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
		"work_id": workID,
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
	Offering         string            `json:"offering,omitempty"`
	RenditionOutputs map[string]string `json:"rendition_outputs"`
	WebhookURL       string            `json:"webhook_url,omitempty"`
	WebhookSecret    string            `json:"webhook_secret,omitempty"`
	WorkID           string            `json:"work_id,omitempty"`
	UnitsPerRend     int64             `json:"units_per_rendition,omitempty"`
	PaymentTicket    string            `json:"payment_ticket,omitempty"`
}

func (s *Server) handleABRSubmit(w http.ResponseWriter, r *http.Request) {
	var req ABRSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	if req.JobID == "" || req.InputURL == "" || len(req.PresetNames) == 0 {
		writeError(w, http.StatusBadRequest, "missing_fields", "job_id, input_url, presets are required")
		return
	}
	workID := firstNonEmpty(req.WorkID, req.JobID)
	if s.payment != nil && !s.dev {
		if _, code, err := s.decodeRequestPayment(r, req.PaymentTicket, true); err != nil {
			if code == http.StatusPaymentRequired {
				writeError(w, code, "payment", "missing payment ticket")
			} else {
				writeError(w, code, "bad_ticket", err.Error())
			}
			return
		}
	}
	abrPresetHint := ""
	if len(req.PresetNames) == 1 {
		abrPresetHint = req.PresetNames[0]
	}
	if err := s.openSessionForRequest(r.Context(), paymentSessionRequest{
		WorkID:     workID,
		Capability: "video:transcode.abr",
		Preset:     abrPresetHint,
		Offering:   req.Offering,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "session_binding", err.Error())
		return
	}
	sender, balance, code := s.maybeProcessPayment(r.Context(), r, req.PaymentTicket, workID)
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
		WorkID: workID, Sender: sender, UnitsPerRend: req.UnitsPerRend,
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
		"work_id": workID,
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
	WorkID        string `json:"work_id,omitempty"`
	StreamID      string `json:"stream_id,omitempty"`
	Preset        string `json:"preset"`
	Offering      string `json:"offering,omitempty"`
	WebhookURL    string `json:"webhook_url,omitempty"`
	WebhookSecret string `json:"webhook_secret,omitempty"`
	PaymentTicket string `json:"payment_ticket,omitempty"`
}

type SessionStartRequest struct {
	GatewaySessionID string `json:"gateway_session_id,omitempty"`
	SessionID        string `json:"session_id,omitempty"`
	Preset           string `json:"preset,omitempty"`
	Offering         string `json:"offering,omitempty"`
	WebhookURL       string `json:"webhook_url,omitempty"`
	WebhookSecret    string `json:"webhook_secret,omitempty"`
}

type SessionStartResponse struct {
	Status           string       `json:"status"`
	GatewaySessionID string       `json:"gateway_session_id"`
	WorkerSessionID  string       `json:"worker_session_id"`
	WorkID           string       `json:"work_id"`
	RTMPURL          string       `json:"rtmp_url,omitempty"`
	Stream           types.Stream `json:"stream"`
}

type SessionTopupResponse struct {
	Status           string `json:"status"`
	GatewaySessionID string `json:"gateway_session_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	WorkID           string `json:"work_id"`
}

type SessionEndResponse struct {
	Status           string `json:"status"`
	GatewaySessionID string `json:"gateway_session_id"`
	WorkerSessionID  string `json:"worker_session_id"`
	WorkID           string `json:"work_id"`
}

// StreamStartResponse returns the RTMP URL the broadcaster should connect to.
type StreamStartResponse struct {
	WorkID   string       `json:"work_id,omitempty"`
	StreamID string       `json:"stream_id,omitempty"`
	RTMPURL  string       `json:"rtmp_url"`
	Stream   types.Stream `json:"stream"`
}

func (s *Server) handleStreamStart(w http.ResponseWriter, r *http.Request) {
	var req StreamStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	workID := firstNonEmpty(req.WorkID, req.StreamID)
	if workID == "" || req.Preset == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "work_id or stream_id, and preset are required")
		return
	}
	if s.liveRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "no_runner", "live runner is not wired")
		return
	}
	ticket, code, err := s.decodeRequestPayment(r, req.PaymentTicket, s.payment != nil && !s.dev)
	if err != nil {
		if code == http.StatusPaymentRequired {
			writeError(w, code, "payment", "missing payment ticket")
			return
		}
		writeError(w, code, "bad_ticket", err.Error())
		return
	}
	var sender []byte
	workerSessionID := deriveWorkerSessionID(workID)
	if err := s.openSessionForRequest(r.Context(), paymentSessionRequest{
		WorkID:     workID,
		Capability: "video:live.rtmp",
		Preset:     req.Preset,
		Offering:   req.Offering,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "session_binding", err.Error())
		return
	}
	if s.payment != nil && !s.dev {
		receipt, err := s.payment.ProcessPayment(r.Context(), ticket, workID)
		if err != nil {
			writeError(w, http.StatusPaymentRequired, types.ErrCodeInvalidPayment, err.Error())
			return
		}
		sender = append([]byte(nil), receipt.Sender...)
	}
	stream, err := s.liveRunner.Start(r.Context(), liverunner.StartRequest{
		WorkID:          workID,
		Sender:          sender,
		PaymentTicket:   ticket,
		PaymentWorkID:   workID,
		WorkerSessionID: workerSessionID,
		Preset:          req.Preset,
		WebhookURL:      req.WebhookURL, WebhookSecret: req.WebhookSecret,
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
		WorkID:   workID,
		StreamID: workID,
		RTMPURL:  s.publicRTMP,
		Stream:   stream,
	})
}

func (s *Server) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	var req SessionStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	gatewaySessionID := firstNonEmpty(req.GatewaySessionID, req.SessionID)
	if gatewaySessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "gateway_session_id or session_id is required")
		return
	}
	if s.liveRunner == nil {
		writeError(w, http.StatusServiceUnavailable, "no_runner", "live runner is not wired")
		return
	}
	if s.payment == nil && !s.dev {
		writeError(w, http.StatusServiceUnavailable, "payment_unavailable", "payment broker is not wired")
		return
	}
	preset := req.Preset
	if preset == "" {
		preset = "h264-live"
	}
	ticket, code, err := s.decodeRequestPayment(r, "", s.payment != nil && !s.dev)
	if err != nil {
		if code == http.StatusPaymentRequired {
			writeError(w, code, "payment", "missing payment ticket")
			return
		}
		writeError(w, code, "bad_ticket", err.Error())
		return
	}
	workID := deriveSessionWorkID(ticket)
	workerSessionID := deriveWorkerSessionID(workID)
	if err := s.openSessionForRequest(r.Context(), paymentSessionRequest{
		WorkID:     workID,
		Capability: "video:live.rtmp",
		Preset:     preset,
		Offering:   req.Offering,
	}); err != nil {
		writeError(w, http.StatusBadRequest, "session_binding", err.Error())
		return
	}
	var sender []byte
	if s.payment != nil && !s.dev {
		receipt, err := s.payment.ProcessPayment(r.Context(), ticket, workID)
		if err != nil {
			writeError(w, http.StatusPaymentRequired, types.ErrCodeInvalidPayment, err.Error())
			return
		}
		sender = append([]byte(nil), receipt.Sender...)
	}
	stream, err := s.liveRunner.Start(r.Context(), liverunner.StartRequest{
		WorkID:          gatewaySessionID,
		Sender:          sender,
		PaymentTicket:   ticket,
		PaymentWorkID:   workID,
		WorkerSessionID: workerSessionID,
		Preset:          preset,
		WebhookURL:      req.WebhookURL,
		WebhookSecret:   req.WebhookSecret,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "already started") {
			status = http.StatusConflict
		}
		writeError(w, status, "start_failed", err.Error())
		return
	}
	s.liveSessions.Upsert(liveSessionInfo{
		GatewaySessionID: gatewaySessionID,
		WorkerSessionID:  workerSessionID,
		WorkID:           workID,
		StreamID:         gatewaySessionID,
		Sender:           sender,
	})
	writeJSON(w, http.StatusAccepted, SessionStartResponse{
		Status:           "starting",
		GatewaySessionID: gatewaySessionID,
		WorkerSessionID:  workerSessionID,
		WorkID:           workID,
		RTMPURL:          s.publicRTMP,
		Stream:           stream,
	})
}

// StreamStopRequest is the body for POST /stream/stop.
type StreamStopRequest struct {
	WorkID   string `json:"work_id,omitempty"`
	StreamID string `json:"stream_id,omitempty"`
}

func (s *Server) handleStreamStop(w http.ResponseWriter, r *http.Request) {
	var req StreamStopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	workID := firstNonEmpty(req.WorkID, req.StreamID)
	if workID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "work_id or stream_id is required")
		return
	}
	if err := s.liveRunner.Stop(r.Context(), workID); err != nil {
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
	WorkID        string `json:"work_id,omitempty"`
	StreamID      string `json:"stream_id,omitempty"`
	PaymentTicket string `json:"payment_ticket,omitempty"`
}

func (s *Server) handleStreamTopup(w http.ResponseWriter, r *http.Request) {
	var req StreamTopupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	workID := firstNonEmpty(req.WorkID, req.StreamID)
	if workID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "work_id or stream_id is required")
		return
	}
	ticket, code, err := s.decodeRequestPayment(r, req.PaymentTicket, s.payment != nil && !s.dev)
	if err != nil {
		if code == http.StatusPaymentRequired {
			writeError(w, code, "payment", "missing payment ticket")
			return
		}
		writeError(w, code, "bad_ticket", err.Error())
		return
	}
	if s.payment != nil && !s.dev {
		if _, _, code := s.maybeProcessPayment(r.Context(), r, req.PaymentTicket, workID); code != 0 {
			writeError(w, code, "payment", "ticket validation failed")
			return
		}
	}
	if err := s.liveRunner.Topup(r.Context(), workID, ticket); err != nil {
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

func (s *Server) handleSessionTopup(w http.ResponseWriter, r *http.Request) {
	gatewaySessionID := strings.TrimSpace(r.PathValue("gateway_session_id"))
	if gatewaySessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "gateway_session_id is required")
		return
	}
	info, ok, reason := s.activeSessionInfoForGatewaySession(r.Context(), gatewaySessionID)
	if !ok {
		if reason == "stream_not_active" {
			writeError(w, http.StatusConflict, reason, "session is not active")
			return
		}
		writeError(w, http.StatusNotFound, types.ErrCodeStreamNotFound, gatewaySessionID)
		return
	}
	if s.payment == nil && !s.dev {
		writeError(w, http.StatusServiceUnavailable, "payment_unavailable", "payment broker is not wired")
		return
	}
	ticket, code, err := s.decodeRequestPayment(r, "", s.payment != nil && !s.dev)
	if err != nil {
		if code == http.StatusPaymentRequired {
			writeError(w, code, "payment", "missing payment ticket")
			return
		}
		writeError(w, code, "bad_ticket", err.Error())
		return
	}
	if s.payment != nil && !s.dev {
		receipt, err := s.payment.ProcessPayment(r.Context(), ticket, info.WorkID)
		if err != nil {
			writeError(w, http.StatusPaymentRequired, types.ErrCodeInvalidPayment, err.Error())
			return
		}
		if len(info.Sender) > 0 && len(receipt.Sender) > 0 && subtle.ConstantTimeCompare(info.Sender, receipt.Sender) != 1 {
			writeError(w, http.StatusConflict, "sender_mismatch", "topup sender does not match session sender")
			return
		}
	}
	if err := s.liveRunner.Topup(r.Context(), info.StreamID, ticket); err != nil {
		writeError(w, http.StatusInternalServerError, "topup_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, SessionTopupResponse{
		Status:           "credited",
		GatewaySessionID: gatewaySessionID,
		WorkerSessionID:  info.WorkerSessionID,
		WorkID:           info.WorkID,
	})
}

// StreamStatusRequest is the body for POST /stream/status.
type StreamStatusRequest struct {
	WorkID   string `json:"work_id,omitempty"`
	StreamID string `json:"stream_id,omitempty"`
}

func (s *Server) handleStreamStatus(w http.ResponseWriter, r *http.Request) {
	var req StreamStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	workID := firstNonEmpty(req.WorkID, req.StreamID)
	if workID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "work_id or stream_id is required")
		return
	}
	stream, err := s.repo.GetStream(r.Context(), workID)
	if err != nil {
		writeError(w, http.StatusNotFound, types.ErrCodeStreamNotFound, workID)
		return
	}
	writeJSON(w, http.StatusOK, stream)
}

func (s *Server) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	gatewaySessionID := strings.TrimSpace(r.PathValue("gateway_session_id"))
	if gatewaySessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "gateway_session_id is required")
		return
	}
	info, ok, reason := s.activeSessionInfoForGatewaySession(r.Context(), gatewaySessionID)
	if !ok {
		if reason == "stream_not_active" {
			writeError(w, http.StatusConflict, reason, "session is not active")
			return
		}
		writeError(w, http.StatusNotFound, types.ErrCodeStreamNotFound, gatewaySessionID)
		return
	}
	stream, err := s.repo.GetStream(r.Context(), gatewaySessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, types.ErrCodeStreamNotFound, gatewaySessionID)
		return
	}
	if err := s.liveRunner.Stop(r.Context(), info.StreamID); err != nil {
		writeError(w, http.StatusInternalServerError, "stop_failed", err.Error())
		return
	}
	if s.payment != nil && !s.dev && stream.Phase == types.StreamPhaseStarting {
		if err := s.payment.CloseSession(r.Context(), info.Sender, info.WorkID); err != nil {
			writeError(w, http.StatusBadGateway, "close_session_failed", err.Error())
			return
		}
	}
	s.liveSessions.Delete(gatewaySessionID)
	writeJSON(w, http.StatusOK, SessionEndResponse{
		Status:           "ended",
		GatewaySessionID: gatewaySessionID,
		WorkerSessionID:  info.WorkerSessionID,
		WorkID:           info.WorkID,
	})
}

// maybeProcessPayment runs ProcessPayment if a ticket is present and
// payment is wired. Returns (sender, balance, statusCode). Returns
// statusCode=0 for "ok / skipped".
func (s *Server) maybeProcessPayment(ctx context.Context, r *http.Request, bodyTicket, workID string) ([]byte, []byte, int) {
	if s.payment == nil || s.dev {
		return nil, nil, 0
	}
	ticket, code, err := s.decodeRequestPayment(r, bodyTicket, true)
	if err != nil {
		return nil, nil, code
	}
	receipt, err := s.payment.ProcessPayment(ctx, ticket, workID)
	if err != nil {
		return nil, nil, http.StatusPaymentRequired
	}
	return receipt.Sender, receipt.BalanceWei, 0
}

type paymentSessionRequest struct {
	WorkID     string
	Capability string
	Preset     string
	Offering   string
}

func (s *Server) openSessionForRequest(ctx context.Context, req paymentSessionRequest) error {
	if s.payment == nil || s.dev {
		return nil
	}
	binding, err := s.resolveSessionBinding(req)
	if err != nil {
		return err
	}
	return s.payment.OpenSession(ctx, binding)
}

func (s *Server) resolveSessionBinding(req paymentSessionRequest) (paymentbroker.SessionBinding, error) {
	if strings.TrimSpace(req.WorkID) == "" {
		return paymentbroker.SessionBinding{}, errors.New("work_id is required")
	}
	capIdx := slices.IndexFunc(s.registryCaps, func(cap config.RegistryCapability) bool {
		return cap.Name == req.Capability
	})
	if capIdx < 0 {
		return paymentbroker.SessionBinding{}, fmt.Errorf("capability %q is not configured", req.Capability)
	}
	capability := s.registryCaps[capIdx]
	offering, err := resolveOffering(capability, req.Offering, req.Preset)
	if err != nil {
		return paymentbroker.SessionBinding{}, err
	}
	return paymentbroker.SessionBinding{
		WorkID:              req.WorkID,
		Capability:          capability.Name,
		Offering:            offering.ID,
		PricePerWorkUnitWei: offering.PricePerWorkUnitWei,
		WorkUnit:            capability.WorkUnit,
	}, nil
}

func resolveOffering(capability config.RegistryCapability, requestedOffering string, presetHint string) (config.RegistryOffering, error) {
	if requestedOffering != "" {
		for _, offering := range capability.Offerings {
			if offering.ID == requestedOffering {
				return offering, nil
			}
		}
		return config.RegistryOffering{}, fmt.Errorf("offering %q is not configured for capability %q", requestedOffering, capability.Name)
	}
	if presetHint != "" {
		for _, offering := range capability.Offerings {
			if offering.ID == presetHint {
				return offering, nil
			}
			if preset, _ := offering.Constraints["preset"].(string); preset == presetHint {
				return offering, nil
			}
		}
	}
	if len(capability.Offerings) == 1 {
		return capability.Offerings[0], nil
	}
	return config.RegistryOffering{}, fmt.Errorf("offering is required for capability %q when multiple offerings are configured", capability.Name)
}

func (s *Server) decodeRequestPayment(r *http.Request, bodyTicket string, required bool) ([]byte, int, error) {
	ticketB64 := paymentTicketFromRequest(r, bodyTicket)
	if ticketB64 == "" {
		if required {
			return nil, http.StatusPaymentRequired, errors.New("missing payment ticket")
		}
		return nil, 0, nil
	}
	ticket, err := decodeTicket(ticketB64)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	return ticket, 0, nil
}

func paymentTicketFromRequest(r *http.Request, bodyTicket string) string {
	if r != nil {
		if headerTicket := r.Header.Get(paymentHeaderName); headerTicket != "" {
			return headerTicket
		}
	}
	return bodyTicket
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

type liveSessionInfo struct {
	GatewaySessionID string
	WorkerSessionID  string
	WorkID           string
	StreamID         string
	Sender           []byte
}

type liveSessionRegistry struct {
	mu    sync.Mutex
	items map[string]liveSessionInfo
}

func (s *Server) sessionInfoForGatewaySession(ctx context.Context, gatewaySessionID string) (liveSessionInfo, bool) {
	if info, ok := s.liveSessions.Get(gatewaySessionID); ok {
		return info, true
	}
	stream, err := s.repo.GetStream(ctx, gatewaySessionID)
	if err != nil {
		return liveSessionInfo{}, false
	}
	if stream.GatewaySessionID == "" {
		stream.GatewaySessionID = stream.WorkID
	}
	return liveSessionInfo{
		GatewaySessionID: stream.GatewaySessionID,
		WorkerSessionID:  stream.WorkerSessionID,
		WorkID:           firstNonEmpty(stream.PaymentWorkID, stream.WorkID),
		StreamID:         stream.WorkID,
		Sender:           append([]byte(nil), stream.Sender...),
	}, true
}

func (s *Server) activeSessionInfoForGatewaySession(ctx context.Context, gatewaySessionID string) (liveSessionInfo, bool, string) {
	stream, err := s.repo.GetStream(ctx, gatewaySessionID)
	if err != nil {
		return liveSessionInfo{}, false, ""
	}
	if stream.Phase.IsTerminal() || stream.Phase == types.StreamPhaseClosing {
		s.liveSessions.Delete(gatewaySessionID)
		return liveSessionInfo{}, false, "stream_not_active"
	}
	info, ok := s.sessionInfoForGatewaySession(ctx, gatewaySessionID)
	if !ok {
		return liveSessionInfo{}, false, ""
	}
	return info, true, ""
}

func newLiveSessionRegistry() *liveSessionRegistry {
	return &liveSessionRegistry{items: make(map[string]liveSessionInfo)}
}

func (r *liveSessionRegistry) Upsert(info liveSessionInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info.Sender = append([]byte(nil), info.Sender...)
	r.items[info.GatewaySessionID] = info
}

func (r *liveSessionRegistry) Get(gatewaySessionID string) (liveSessionInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.items[gatewaySessionID]
	if !ok {
		return liveSessionInfo{}, false
	}
	info.Sender = append([]byte(nil), info.Sender...)
	return info, true
}

func (r *liveSessionRegistry) Delete(gatewaySessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, gatewaySessionID)
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

func deriveSessionWorkID(paymentBytes []byte) string {
	if len(paymentBytes) == 0 {
		return ""
	}
	sum := sha256.Sum256(paymentBytes)
	return hex.EncodeToString(sum[:])
}

func deriveWorkerSessionID(workID string) string {
	if workID == "" {
		return "worker_session_unknown"
	}
	return "worker_" + workID
}

func (s *Server) requireBearerAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.offeringsAuthToken == "" {
		return true
	}
	got := r.Header.Get("Authorization")
	want := "Bearer " + s.offeringsAuthToken
	if len(got) != len(want) || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		writeError(w, http.StatusUnauthorized, "unauthorized", "missing or invalid bearer token")
		return false
	}
	return true
}

func ensureSingleJSONDocument(dec *json.Decoder) error {
	var tail struct{}
	if err := dec.Decode(&tail); err == nil {
		return fmt.Errorf("request body must contain exactly one JSON object")
	} else if err == io.EOF {
		return nil
	} else {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
}

func parseTicketParamsRequest(in ticketParamsRequest) (paymentclient.GetTicketParamsRequest, error) {
	sender, err := parseHexAddress("sender_eth_address", in.SenderETHAddress)
	if err != nil {
		return paymentclient.GetTicketParamsRequest{}, err
	}
	recipient, err := parseHexAddress("recipient_eth_address", in.RecipientETHAddress)
	if err != nil {
		return paymentclient.GetTicketParamsRequest{}, err
	}
	faceValue, ok := new(big.Int).SetString(strings.TrimSpace(in.FaceValueWei), 10)
	if !ok {
		return paymentclient.GetTicketParamsRequest{}, fmt.Errorf("face_value_wei must be a decimal integer")
	}
	if faceValue.Sign() <= 0 {
		return paymentclient.GetTicketParamsRequest{}, fmt.Errorf("face_value_wei must be > 0")
	}
	if strings.TrimSpace(in.Capability) == "" {
		return paymentclient.GetTicketParamsRequest{}, fmt.Errorf("capability is required")
	}
	if strings.TrimSpace(in.Offering) == "" {
		return paymentclient.GetTicketParamsRequest{}, fmt.Errorf("offering is required")
	}
	return paymentclient.GetTicketParamsRequest{
		Sender:     sender,
		Recipient:  recipient,
		FaceValue:  faceValue,
		Capability: strings.TrimSpace(in.Capability),
		Offering:   strings.TrimSpace(in.Offering),
	}, nil
}

func parseHexAddress(field, value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "0x") && !strings.HasPrefix(trimmed, "0X") {
		return nil, fmt.Errorf("%s must be a 0x-prefixed hex address", field)
	}
	raw := trimmed[2:]
	if len(raw) != 40 {
		return nil, fmt.Errorf("%s must be exactly 20 bytes (40 hex chars)", field)
	}
	out, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be a valid hex address", field)
	}
	return out, nil
}

func (s *Server) writeTicketParamsProxyError(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case codes.Unavailable, codes.DeadlineExceeded:
		writeError(w, http.StatusServiceUnavailable, "payment_daemon_unavailable", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "ticket_params_unavailable", err.Error())
	}
}

func renderTicketParamsJSON(tp paymentclient.TicketParams) ticketParamsJSON {
	return ticketParamsJSON{
		Recipient:         bytesToHexString(tp.Recipient),
		FaceValue:         bytesToDecimalString(tp.FaceValueWei),
		WinProb:           bytesToHexString(tp.WinProb),
		RecipientRandHash: bytesToHexString(tp.RecipientRandHash),
		Seed:              bytesToHexString(tp.Seed),
		ExpirationBlock:   bytesToDecimalString(tp.ExpirationBlock),
		ExpirationParams: ticketExpirationParamsJSON{
			CreationRound:          tp.ExpirationParams.CreationRound,
			CreationRoundBlockHash: bytesToHexString(tp.ExpirationParams.CreationRoundBlockHash),
		},
	}
}

func bytesToHexString(b []byte) string {
	if len(b) == 0 {
		return "0x"
	}
	return "0x" + hex.EncodeToString(b)
}

func bytesToDecimalString(b []byte) string {
	if len(b) == 0 {
		return "0"
	}
	return new(big.Int).SetBytes(b).String()
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
