// Package shellclient is the worker → shell HTTP client over the
// /internal/live/* callback API. Auth is a shared secret presented via
// the X-Worker-Secret header.
//
// This is the worker's complement to the routes in
// `apps/api/src/runtime/http/internal/live/`. Wire formats are kept in
// lockstep with the zod schemas there.
package shellclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the worker's shell-callback surface. Implementations: the
// HTTP client below, plus a Fake for tests.
type Client interface {
	ValidateKey(ctx context.Context, in ValidateKeyInput) (ValidateKeyResult, error)
	SessionActive(ctx context.Context, in SessionActiveInput) (SessionActiveResult, error)
	SessionTick(ctx context.Context, in SessionTickInput) (SessionTickResult, error)
	SessionEnded(ctx context.Context, in SessionEndedInput) (SessionEndedResult, error)
	RecordingFinalized(ctx context.Context, in RecordingFinalizedInput) (RecordingFinalizedResult, error)
	Topup(ctx context.Context, in TopupInput) (TopupResult, error)
}

// ValidateKeyInput / ValidateKeyResult — POST /internal/live/validate-key.
type ValidateKeyInput struct {
	StreamKey string
	WorkerURL string
}
type ValidateKeyResult struct {
	Accepted         bool
	StreamID         string
	ProjectID        string
	RecordingEnabled bool
}

// SessionActiveInput / SessionActiveResult — POST /internal/live/session-active.
type SessionActiveInput struct {
	StreamID  string
	WorkerURL string
	StartedAt time.Time
}
type SessionActiveResult struct {
	ReservationID string
}

// SessionTickInput / SessionTickResult — POST /internal/live/session-tick.
type SessionTickInput struct {
	StreamID          string
	Seq               uint64
	DebitSeconds      float64
	CumulativeSeconds float64
}
type SessionTickResult struct {
	BalanceCents   int64
	RunwaySeconds  int64
	GraceTriggered bool
}

// SessionEndedInput / SessionEndedResult — POST /internal/live/session-ended.
type SessionEndedInput struct {
	StreamID     string
	Reason       string // "graceful" | "insufficient_balance" | "session_worker_failed" | "admin_stop"
	FinalSeq     uint64
	FinalSeconds float64
}
type SessionEndedResult struct {
	RecordingProcessing bool
}

// RecordingFinalizedInput / RecordingFinalizedResult — POST /internal/live/recording-finalized.
type RecordingFinalizedInput struct {
	StreamID           string
	SegmentStorageKeys []string
	MasterStorageKey   string
	TotalDurationSec   float64
}
type RecordingFinalizedResult struct {
	RecordingAssetID string
}

// TopupInput / TopupResult — POST /internal/live/topup.
type TopupInput struct {
	StreamID       string
	RequestSeconds int64
}
type TopupResult struct {
	Succeeded       bool
	AuthorizedCents int64
	BalanceCents    int64
}

// Errors mirroring the shell's response codes for callers that want to
// switch on them.
var (
	ErrUnauthorized = errors.New("shellclient: unauthorized (bad shared secret)")
	ErrNotFound     = errors.New("shellclient: not found")
	ErrConflict     = errors.New("shellclient: conflict (state mismatch)")
	ErrBadRequest   = errors.New("shellclient: bad request")
)

// Config wires the HTTP client.
type Config struct {
	// BaseURL is the shell's internal URL, e.g. "http://api:8080".
	BaseURL string
	// Secret is sent as X-Worker-Secret on every request.
	Secret string
	// HTTPClient is overridable (tests). Defaults to a sane net/http client.
	HTTPClient *http.Client
	// Timeout applied per-request when the parent ctx has no deadline.
	RequestTimeout time.Duration
}

type httpClient struct {
	cfg Config
	hc  *http.Client
}

// New returns a Client backed by net/http.
func New(cfg Config) (Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("shellclient: BaseURL required")
	}
	if cfg.Secret == "" {
		return nil, errors.New("shellclient: Secret required")
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 5 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.RequestTimeout}
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &httpClient{cfg: cfg, hc: hc}, nil
}

func (c *httpClient) post(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("shellclient: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("shellclient: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Worker-Secret", c.cfg.Secret)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("shellclient: do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("shellclient: decode: %w", err)
		}
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, string(respBody))
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", ErrConflict, string(respBody))
	case http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrBadRequest, string(respBody))
	default:
		return fmt.Errorf("shellclient: unexpected status %d: %s", resp.StatusCode, string(respBody))
	}
}

// Wire-format JSON shapes. snake_case on the wire to match the shell.
type validateKeyReq struct {
	StreamKey string `json:"stream_key"`
	WorkerURL string `json:"worker_url"`
}
type validateKeyResp struct {
	Accepted         bool   `json:"accepted"`
	StreamID         string `json:"stream_id,omitempty"`
	ProjectID        string `json:"project_id,omitempty"`
	RecordingEnabled bool   `json:"recording_enabled,omitempty"`
}

type sessionActiveReq struct {
	StreamID  string `json:"stream_id"`
	WorkerURL string `json:"worker_url"`
	StartedAt string `json:"started_at,omitempty"`
}
type sessionActiveResp struct {
	OK            bool   `json:"ok"`
	ReservationID string `json:"reservation_id"`
}

type sessionTickReq struct {
	StreamID          string  `json:"stream_id"`
	Seq               uint64  `json:"seq"`
	DebitSeconds      float64 `json:"debit_seconds"`
	CumulativeSeconds float64 `json:"cumulative_seconds"`
}
type sessionTickResp struct {
	OK             bool  `json:"ok"`
	BalanceCents   int64 `json:"balance_cents"`
	RunwaySeconds  int64 `json:"runway_seconds"`
	GraceTriggered bool  `json:"grace_triggered"`
}

type sessionEndedReq struct {
	StreamID     string  `json:"stream_id"`
	Reason       string  `json:"reason"`
	FinalSeq     uint64  `json:"final_seq"`
	FinalSeconds float64 `json:"final_seconds"`
}
type sessionEndedResp struct {
	OK                  bool `json:"ok"`
	RecordingProcessing bool `json:"recording_processing"`
}

type recordingFinalizedReq struct {
	StreamID           string   `json:"stream_id"`
	SegmentStorageKeys []string `json:"segment_storage_keys"`
	MasterStorageKey   string   `json:"master_storage_key"`
	TotalDurationSec   float64  `json:"total_duration_sec"`
}
type recordingFinalizedResp struct {
	OK               bool   `json:"ok"`
	RecordingAssetID string `json:"recording_asset_id"`
}

type topupReq struct {
	StreamID       string `json:"stream_id"`
	RequestSeconds int64  `json:"request_seconds"`
}
type topupResp struct {
	Succeeded       bool  `json:"succeeded"`
	AuthorizedCents int64 `json:"authorized_cents"`
	BalanceCents    int64 `json:"balance_cents"`
}

func (c *httpClient) ValidateKey(ctx context.Context, in ValidateKeyInput) (ValidateKeyResult, error) {
	var resp validateKeyResp
	err := c.post(ctx, "/internal/live/validate-key", validateKeyReq{
		StreamKey: in.StreamKey, WorkerURL: in.WorkerURL,
	}, &resp)
	return ValidateKeyResult{
		Accepted: resp.Accepted, StreamID: resp.StreamID,
		ProjectID: resp.ProjectID, RecordingEnabled: resp.RecordingEnabled,
	}, err
}

func (c *httpClient) SessionActive(ctx context.Context, in SessionActiveInput) (SessionActiveResult, error) {
	body := sessionActiveReq{StreamID: in.StreamID, WorkerURL: in.WorkerURL}
	if !in.StartedAt.IsZero() {
		body.StartedAt = in.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	var resp sessionActiveResp
	err := c.post(ctx, "/internal/live/session-active", body, &resp)
	return SessionActiveResult{ReservationID: resp.ReservationID}, err
}

func (c *httpClient) SessionTick(ctx context.Context, in SessionTickInput) (SessionTickResult, error) {
	var resp sessionTickResp
	err := c.post(ctx, "/internal/live/session-tick", sessionTickReq{
		StreamID: in.StreamID, Seq: in.Seq,
		DebitSeconds: in.DebitSeconds, CumulativeSeconds: in.CumulativeSeconds,
	}, &resp)
	return SessionTickResult{
		BalanceCents: resp.BalanceCents, RunwaySeconds: resp.RunwaySeconds,
		GraceTriggered: resp.GraceTriggered,
	}, err
}

func (c *httpClient) SessionEnded(ctx context.Context, in SessionEndedInput) (SessionEndedResult, error) {
	var resp sessionEndedResp
	err := c.post(ctx, "/internal/live/session-ended", sessionEndedReq{
		StreamID: in.StreamID, Reason: in.Reason,
		FinalSeq: in.FinalSeq, FinalSeconds: in.FinalSeconds,
	}, &resp)
	return SessionEndedResult{RecordingProcessing: resp.RecordingProcessing}, err
}

func (c *httpClient) RecordingFinalized(ctx context.Context, in RecordingFinalizedInput) (RecordingFinalizedResult, error) {
	var resp recordingFinalizedResp
	err := c.post(ctx, "/internal/live/recording-finalized", recordingFinalizedReq{
		StreamID: in.StreamID, SegmentStorageKeys: in.SegmentStorageKeys,
		MasterStorageKey: in.MasterStorageKey, TotalDurationSec: in.TotalDurationSec,
	}, &resp)
	return RecordingFinalizedResult{RecordingAssetID: resp.RecordingAssetID}, err
}

func (c *httpClient) Topup(ctx context.Context, in TopupInput) (TopupResult, error) {
	var resp topupResp
	err := c.post(ctx, "/internal/live/topup", topupReq{
		StreamID: in.StreamID, RequestSeconds: in.RequestSeconds,
	}, &resp)
	return TopupResult{
		Succeeded: resp.Succeeded, AuthorizedCents: resp.AuthorizedCents,
		BalanceCents: resp.BalanceCents,
	}, err
}
