// Package liverunner implements the live HLS state machine the worker
// runs per accepted RTMP session.
//
// Transition note:
//   - Pattern B sessions are pre-opened through the worker's
//     /api/sessions/* routes, use receiver-side debit/runway checks, and
//     emit typed worker events back to the gateway.
//   - Legacy sessions still validate via stream key and continue to use
//     the older session-active/session-tick/session-ended callbacks
//     until the gateway rewrite removes that path.
//
// Lifecycle (per stream, legacy callback nomenclature shown where it
// still applies):
//
//	NEW ──validate-key──▶ VALIDATED ──session-active──▶ STREAMING ──┐
//	                                                                │
//	                                          ┌──tick miss──▶ RECONNECTING (bounded)
//	                                          │
//	                                          ▼
//	                                       CLOSING ──session-ended──▶ CLOSED
//
// The runner satisfies `ingest.SessionAcceptor` so the RTMP provider can
// hand it freshly-arrived broadcaster sessions. Each accepted session
// gets its own goroutine that:
//   - validates the stream key unless the session was pre-opened through
//     the Pattern B /api/sessions/start flow
//   - activates legacy shell state only on the fallback path
//   - spawns the encoder (§D plugs in the real FFmpeg one)
//   - either debits receiver-side balance locally and emits typed events
//     (Pattern B) or ticks the shell directly (legacy)
//   - on encoder exit / ctx cancel: emits/records terminal state
//   - on graceful close with recording enabled: reads the per-session
//     `livecdn.Mirror.Segments()` (populated by §E's manifest writer)
//     and emits recording-ready state.
//
// State persists into BoltDB via `repo/jobs.SaveStream` so a worker
// restart resumes the correct DebitSeq. First-cut Pattern B still treats
// restart as a terminal event rather than migrating the session.
package liverunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ingest"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/shellclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/webhooks"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/livecdn"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// ErrStreamNotFound is returned by Status when no stream with the given
// WorkID is in flight.
var ErrStreamNotFound = errors.New("liverunner: stream not found")

// EncoderFactory builds an Encoder per accepted session. Plan §D
// supplies the real FFmpeg-backed factory; tests + §C scaffolding use
// `NewDrainEncoder`.
type EncoderFactory func() Encoder

// Config wires the runner.
type Config struct {
	Repo    *jobs.Repo
	Webhook webhooks.Sender
	Payment paymentbroker.Broker
	Presets *presetloader.Loader
	GPU     types.GPUProfile
	Logger  *slog.Logger

	Ingest ingest.IngestProvider
	Shell  shellclient.Client

	// EncoderFactory is invoked once per accepted session. nil → drain encoder.
	EncoderFactory EncoderFactory

	// WorkerURL is the externally-visible URL the worker registered as
	// during preflight; passed back to the shell on validate-key /
	// session-active so the shell can return it on dispatch_status.
	WorkerURL string

	// Preset name for the live encode (e.g., "h264-live").
	LivePreset string

	// StoragePrefix template — `{stream_id}` is substituted. Default
	// "live/{stream_id}".
	StoragePrefix string

	// MaxConcurrentStreams caps in-flight streams. 0 = unlimited.
	MaxConcurrentStreams int

	// LocalDirRoot is the FFmpeg scratch dir; per-session output goes
	// to `${LocalDirRoot}/${stream_id}`. Must match the FFmpeg encoder
	// factory's LocalDirRoot. Empty disables the mirror/manifest path
	// (useful in dev mode with the drain encoder).
	LocalDirRoot string

	// Sink is the storage target for live segments + playlists. nil
	// disables the mirror.
	Sink livecdn.Sink

	// Ladder is the encode ladder used to build the master manifest at
	// session-active time. Empty disables master writing.
	Ladder []types.Preset

	// MirrorPollInterval governs how often the per-session mirror
	// scans the local FFmpeg output dir. Default 500ms.
	MirrorPollInterval time.Duration

	// Streaming-session knobs (per docs/design-docs/streaming-session-pattern.md).
	DebitCadence      time.Duration // default 5s
	RunwaySeconds     int           // default 30
	GraceSeconds      int           // default 60
	PreCreditSeconds  int           // default 60
	DebitRetryBackoff time.Duration // default 1s
	RestartLimit      int           // default 3
	TopupMinInterval  time.Duration // default 5s

	// Test seams.
	SleepFn func(time.Duration)
	NowFn   func() time.Time
}

// StartRequest is retained for backward compat with existing HTTP/gRPC
// surfaces. The RTMP-driven path doesn't use it (sessions arrive via
// IngestProvider). Bodies opened via Start are assumed pre-validated
// and skip the validate-key callback.
type StartRequest struct {
	WorkID          string
	Sender          []byte
	PaymentTicket   []byte
	PaymentWorkID   string
	WorkerSessionID string
	Preset          string
	WebhookURL      string
	WebhookSecret   string
}

// Runner manages all live streams.
type Runner struct {
	cfg Config

	mu      sync.Mutex
	streams map[string]*streamCtl
}

type streamCtl struct {
	stream          types.Stream
	cancel          context.CancelFunc
	encoder         Encoder
	mirror          *livecdn.Mirror
	done            chan struct{}
	patternBSender  []byte
	patternBWorkID  string
	workerSessionID string
	usePatternB     bool
	mu              sync.Mutex
	reason          string
}

const workerEventPostAttempts = 3

const (
	sessionEndReasonGraceful            = "graceful"
	sessionEndReasonInsufficientBalance = "insufficient_balance"
	sessionEndReasonWorkerFailed        = "session_worker_failed"
	sessionEndReasonPaymentFailed       = "payment_path_failed"
	sessionEndReasonAdminStop           = "admin_stop"
)

func (s *streamCtl) setReason(reason string) {
	if reason == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if terminationReasonPriority(reason) >= terminationReasonPriority(s.reason) {
		s.reason = reason
	}
}

func (s *streamCtl) getReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

func terminationReasonPriority(reason string) int {
	switch reason {
	case sessionEndReasonGraceful:
		return 1
	case sessionEndReasonAdminStop:
		return 2
	case sessionEndReasonInsufficientBalance:
		return 3
	case sessionEndReasonPaymentFailed:
		return 4
	case sessionEndReasonWorkerFailed:
		return 5
	default:
		return 0
	}
}

// New constructs the runner with defaulted streaming-session knobs.
func New(cfg Config) (*Runner, error) {
	if cfg.DebitCadence == 0 {
		cfg.DebitCadence = 5 * time.Second
	}
	if cfg.RunwaySeconds == 0 {
		cfg.RunwaySeconds = 30
	}
	if cfg.GraceSeconds == 0 {
		cfg.GraceSeconds = 60
	}
	if cfg.PreCreditSeconds == 0 {
		cfg.PreCreditSeconds = 60
	}
	if cfg.DebitRetryBackoff == 0 {
		cfg.DebitRetryBackoff = time.Second
	}
	if cfg.RestartLimit == 0 {
		cfg.RestartLimit = 3
	}
	if cfg.TopupMinInterval == 0 {
		cfg.TopupMinInterval = 5 * time.Second
	}
	if cfg.SleepFn == nil {
		cfg.SleepFn = time.Sleep
	}
	if cfg.NowFn == nil {
		cfg.NowFn = time.Now
	}
	if cfg.EncoderFactory == nil {
		cfg.EncoderFactory = NewDrainEncoder
	}
	if cfg.LivePreset == "" {
		cfg.LivePreset = "h264-live"
	}
	if cfg.StoragePrefix == "" {
		cfg.StoragePrefix = "live/{stream_id}"
	}
	if cfg.MirrorPollInterval == 0 {
		cfg.MirrorPollInterval = 500 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Runner{
		cfg:     cfg,
		streams: make(map[string]*streamCtl),
	}, nil
}

// Accept implements ingest.SessionAcceptor. The RTMP provider hands
// every newly-published session here; we validate the key with the
// shell, open a streaming-session reservation, and start the per-stream
// goroutine.
func (r *Runner) Accept(ctx context.Context, sess ingest.IngestSession) (ingest.Acceptance, error) {
	if r.cfg.Shell == nil {
		return ingest.Acceptance{}, errors.New("liverunner: no shell client configured")
	}

	if r.cfg.MaxConcurrentStreams > 0 {
		r.mu.Lock()
		if len(r.streams) >= r.cfg.MaxConcurrentStreams {
			r.mu.Unlock()
			return ingest.Acceptance{}, ingest.ErrCapacityExceeded
		}
		r.mu.Unlock()
	}

	// 1. validate-key.
	validate, err := r.cfg.Shell.ValidateKey(ctx, shellclient.ValidateKeyInput{
		StreamKey: sess.StreamKey(),
		WorkerURL: r.cfg.WorkerURL,
	})
	if err != nil {
		return ingest.Acceptance{}, fmt.Errorf("validate-key: %w", err)
	}
	if !validate.Accepted {
		return ingest.Acceptance{}, ingest.ErrStreamKeyInvalid
	}

	startedAt := r.cfg.NowFn()
	r.mu.Lock()
	ctl, hasPendingSession := r.streams[validate.StreamID]
	r.mu.Unlock()

	var active shellclient.SessionActiveResult
	if !hasPendingSession || !ctl.usePatternB {
		active, err = r.cfg.Shell.SessionActive(ctx, shellclient.SessionActiveInput{
			StreamID:  validate.StreamID,
			WorkerURL: r.cfg.WorkerURL,
			StartedAt: startedAt,
		})
		if err != nil {
			return ingest.Acceptance{}, fmt.Errorf("session-active: %w", err)
		}
	}

	// 3. Persist the worker-side stream record.
	stream := types.Stream{
		WorkID:           validate.StreamID,
		GatewaySessionID: validate.StreamID,
		Phase:            types.StreamPhaseStreaming,
		Preset:           r.cfg.LivePreset,
		StartedAt:        startedAt,
		PublishURL:       sess.RemoteAddr(),
	}
	if hasPendingSession {
		stream.WorkerSessionID = ctl.workerSessionID
		stream.PaymentWorkID = ctl.patternBWorkID
		stream.Sender = append([]byte(nil), ctl.patternBSender...)
	}
	if r.cfg.Repo != nil {
		if err := r.cfg.Repo.SaveStream(ctx, stream); err != nil {
			r.cfg.Logger.Warn("liverunner.persist_stream_failed", "stream_id", stream.WorkID, "err", err)
		}
	}

	// 4. Spawn the per-stream goroutine.
	streamCtx, cancel := context.WithCancel(ctx)
	enc := r.cfg.EncoderFactory()

	// Mirror + master manifest, if configured. The mirror is per-session
	// because the SinkPrefix encodes the stream id.
	var mirror *livecdn.Mirror
	if r.cfg.Sink != nil && r.cfg.LocalDirRoot != "" {
		localDir := filepath.Join(r.cfg.LocalDirRoot, validate.StreamID)
		_ = os.MkdirAll(localDir, 0o755)
		sinkPrefix := storagePrefix(r.cfg.StoragePrefix, validate.StreamID)
		mirror = livecdn.NewMirror(localDir, sinkPrefix, r.cfg.Sink)
		mirror.PollInterval = r.cfg.MirrorPollInterval
		mirror.SetLogger(func(err error) {
			r.cfg.Logger.Warn("liverunner.mirror_error",
				"stream_id", validate.StreamID, "err", err)
		})
		if len(r.cfg.Ladder) > 0 {
			if werr := livecdn.WriteMaster(ctx, mirror, r.cfg.Ladder); werr != nil {
				r.cfg.Logger.Warn("liverunner.master_write_failed",
					"stream_id", validate.StreamID, "err", werr)
			}
		}
	}

	if !hasPendingSession {
		ctl = &streamCtl{reason: sessionEndReasonGraceful}
	}
	ctl.stream = stream
	ctl.cancel = cancel
	ctl.encoder = enc
	ctl.mirror = mirror
	ctl.done = make(chan struct{})
	if ctl.reason == "" {
		ctl.reason = sessionEndReasonGraceful
	}

	r.mu.Lock()
	r.streams[validate.StreamID] = ctl
	r.mu.Unlock()

	if ctl.usePatternB {
		r.cfg.Logger.Info("liverunner.session_ready",
			"stream_id", validate.StreamID,
			"worker_session_id", ctl.workerSessionID,
			"work_id", ctl.patternBWorkID,
			"recording_enabled", validate.RecordingEnabled,
		)
	} else {
		r.cfg.Logger.Info("liverunner.session_active",
			"stream_id", validate.StreamID,
			"reservation_id", active.ReservationID,
			"recording_enabled", validate.RecordingEnabled,
		)
	}

	go r.runSession(streamCtx, ctl, sess, validate)

	return ingest.Acceptance{
		OnEnd: func(reason string) {
			// Provider says the underlying connection is gone. Cancel
			// the per-stream goroutine; it owns the close path.
			if reason == "close" {
				ctl.setReason(sessionEndReasonGraceful)
			} else {
				ctl.setReason(sessionEndReasonWorkerFailed)
			}
			r.cfg.Logger.Info("liverunner.ingest_ended",
				"stream_id", validate.StreamID, "reason", reason)
			cancel()
		},
	}, nil
}

// runSession drives one stream's lifecycle: encoder + payment ticker.
func (r *Runner) runSession(
	ctx context.Context,
	ctl *streamCtl,
	sess ingest.IngestSession,
	v shellclient.ValidateKeyResult,
) {
	defer close(ctl.done)
	defer func() {
		_ = sess.Close()
		r.mu.Lock()
		delete(r.streams, v.StreamID)
		r.mu.Unlock()
	}()

	// Run the encoder in a background goroutine so the payment loop
	// can run alongside. encErr captures the encoder's exit status.
	encErr := make(chan error, 1)
	go func() {
		encErr <- ctl.encoder.Start(ctx, EncoderInput{
			StreamID:      v.StreamID,
			Reader:        sess.Reader(),
			MediaFormat:   sess.MediaFormat(),
			Preset:        r.cfg.LivePreset,
			StoragePrefix: storagePrefix(r.cfg.StoragePrefix, v.StreamID),
		})
	}()

	// Mirror loop: shadows the FFmpeg local-write directory to the Sink
	// (segments + variant playlists) and tracks the cumulative segment
	// list for the recording bridge.
	var mirrorWG sync.WaitGroup
	if ctl.mirror != nil {
		mirrorWG.Add(1)
		go func() {
			defer mirrorWG.Done()
			_ = ctl.mirror.Run(ctx)
		}()
	}

	// Payment ticker. Default cadence 5s; cumulative encoded seconds
	// drive each tick.
	ticker := time.NewTicker(r.cfg.DebitCadence)
	defer ticker.Stop()

	var (
		lastDebited   float64
		seq           uint64
		graceDeadline time.Time // zero = not in grace
		closeReason   = sessionEndReasonGraceful
		inLowBalance  bool
	)

	if r.cfg.Repo != nil {
		// Resume the persisted DebitSeq if a prior process restarted
		// mid-stream. Idempotent on the receiver side.
		if existing, err := r.cfg.Repo.GetStream(ctx, v.StreamID); err == nil {
			seq = existing.DebitSeq
		}
	}

	emitEvent := func(callCtx context.Context, in shellclient.WorkerEventInput) {
		if r.cfg.Shell == nil {
			return
		}
		in.GatewaySessionID = v.StreamID
		in.OccurredAt = r.cfg.NowFn()
		if ctl.usePatternB {
			in.WorkerSessionID = ctl.workerSessionID
			in.WorkID = ctl.patternBWorkID
		}
		var lastErr error
		for attempt := 1; attempt <= workerEventPostAttempts; attempt++ {
			if err := r.cfg.Shell.PostEvent(callCtx, in); err == nil {
				return
			} else {
				lastErr = err
			}
			if attempt == workerEventPostAttempts || callCtx.Err() != nil {
				break
			}
			r.cfg.SleepFn(r.cfg.DebitRetryBackoff)
		}
		if lastErr != nil {
			r.cfg.Logger.Warn("liverunner.event_post_failed",
				"stream_id", v.StreamID,
				"type", in.Type,
				"attempts", workerEventPostAttempts,
				"err", lastErr,
			)
		}
	}

	if ctl.usePatternB {
		emitEvent(ctx, shellclient.WorkerEventInput{
			Type: "session.ready",
		})
	}

	persistStreamUpdate := func(callCtx context.Context, apply func(*types.Stream)) {
		ctl.stream.UpdatedAt = r.cfg.NowFn()
		apply(&ctl.stream)
		if r.cfg.Repo != nil {
			if cur, err := r.cfg.Repo.GetStream(callCtx, v.StreamID); err == nil {
				apply(&cur)
				_ = r.cfg.Repo.SaveStream(callCtx, cur)
			}
		}
	}

	tickAndDecide := func() (closeNow bool, reason string) {
		encoded := ctl.encoder.EncodedSeconds()
		debit := encoded - lastDebited
		if debit < 0 {
			debit = 0
		}
		if ctl.usePatternB {
			units := int64(0)
			if debit > 0 {
				units = int64(math.Ceil(debit))
			}
			if units > 0 {
				seq++
				if r.cfg.Repo != nil {
					_, _ = r.cfg.Repo.IncrementDebitSeq(ctx, v.StreamID)
				}
				if _, err := r.cfg.Payment.DebitBalance(ctx, ctl.patternBSender, ctl.patternBWorkID, units, seq); err != nil {
					persistStreamUpdate(ctx, func(stream *types.Stream) {
						stream.Phase = types.StreamPhasePaymentLost
						stream.ErrorCode = sessionEndReasonPaymentFailed
					})
					emitEvent(ctx, shellclient.WorkerEventInput{
						Type:        "session.error",
						Reason:      sessionEndReasonPaymentFailed,
						Message:     err.Error(),
						Recoverable: false,
					})
					return true, sessionEndReasonPaymentFailed
				}
				lastDebited = encoded
				ok, err := r.cfg.Payment.SufficientBalance(ctx, ctl.patternBSender, ctl.patternBWorkID, int64(r.cfg.RunwaySeconds))
				if err != nil {
					persistStreamUpdate(ctx, func(stream *types.Stream) {
						stream.Phase = types.StreamPhasePaymentLost
						stream.ErrorCode = sessionEndReasonPaymentFailed
					})
					emitEvent(ctx, shellclient.WorkerEventInput{
						Type:        "session.error",
						Reason:      sessionEndReasonPaymentFailed,
						Message:     err.Error(),
						Recoverable: false,
					})
					return true, sessionEndReasonPaymentFailed
				}
				remainingRunway := int64(0)
				if ok {
					remainingRunway = int64(r.cfg.RunwaySeconds)
				}
				emitEvent(ctx, shellclient.WorkerEventInput{
					Type:            "session.usage.tick",
					UsageSeq:        seq,
					Units:           units,
					UnitType:        "seconds",
					RemainingRunway: remainingRunway,
					LowBalance:      !ok,
				})
				if !ok {
					if !inLowBalance {
						inLowBalance = true
						if graceDeadline.IsZero() {
							graceDeadline = r.cfg.NowFn().Add(time.Duration(r.cfg.GraceSeconds) * time.Second)
						}
						persistStreamUpdate(ctx, func(stream *types.Stream) {
							stream.Phase = types.StreamPhaseLowBalance
							stream.LowBalance = true
							stream.GraceUntil = graceDeadline
							stream.ErrorCode = ""
						})
						emitEvent(ctx, shellclient.WorkerEventInput{
							Type:            "session.balance.low",
							RemainingRunway: remainingRunway,
							LowBalance:      true,
						})
					}
					if r.cfg.NowFn().After(graceDeadline) {
						return true, sessionEndReasonInsufficientBalance
					}
				} else {
					if inLowBalance {
						inLowBalance = false
						persistStreamUpdate(ctx, func(stream *types.Stream) {
							stream.Phase = types.StreamPhaseStreaming
							stream.LowBalance = false
							stream.GraceUntil = time.Time{}
							stream.ErrorCode = ""
						})
						emitEvent(ctx, shellclient.WorkerEventInput{
							Type:            "session.balance.refilled",
							RemainingRunway: remainingRunway,
						})
					}
					graceDeadline = time.Time{}
				}
			}
			return false, ""
		}

		seq++
		if r.cfg.Repo != nil {
			_, _ = r.cfg.Repo.IncrementDebitSeq(ctx, v.StreamID)
		}
		tickRes, err := r.cfg.Shell.SessionTick(ctx, shellclient.SessionTickInput{
			StreamID:          v.StreamID,
			Seq:               seq,
			DebitSeconds:      debit,
			CumulativeSeconds: encoded,
		})
		if err != nil {
			r.cfg.Logger.Warn("liverunner.tick_failed",
				"stream_id", v.StreamID, "seq", seq, "err", err)
			return false, ""
		}
		lastDebited = encoded
		if tickRes.GraceTriggered {
			if graceDeadline.IsZero() {
				graceDeadline = r.cfg.NowFn().Add(time.Duration(r.cfg.GraceSeconds) * time.Second)
			}
			if r.cfg.NowFn().After(graceDeadline) {
				return true, sessionEndReasonInsufficientBalance
			}
		} else if !graceDeadline.IsZero() && tickRes.RunwaySeconds > int64(r.cfg.RunwaySeconds) {
			graceDeadline = time.Time{}
		}
		if tickRes.RunwaySeconds <= int64(r.cfg.RunwaySeconds) {
			lastTopupAt := ctl.stream.LastTopupAt
			if lastTopupAt.IsZero() || r.cfg.NowFn().Sub(lastTopupAt) >= r.cfg.TopupMinInterval {
				topupRes, err := r.cfg.Shell.Topup(ctx, shellclient.TopupInput{
					StreamID:       v.StreamID,
					RequestSeconds: int64(r.cfg.PreCreditSeconds),
				})
				if err != nil {
					r.cfg.Logger.Warn("liverunner.topup_failed",
						"stream_id", v.StreamID, "err", err)
				} else {
					ctl.stream.LastTopupAt = r.cfg.NowFn()
					if r.cfg.Repo != nil {
						if cur, gerr := r.cfg.Repo.GetStream(ctx, v.StreamID); gerr == nil {
							cur.LastTopupAt = ctl.stream.LastTopupAt
							_ = r.cfg.Repo.SaveStream(ctx, cur)
						}
					}
					if !topupRes.Succeeded {
						r.cfg.Logger.Warn("liverunner.topup_declined",
							"stream_id", v.StreamID,
							"authorized_cents", topupRes.AuthorizedCents,
							"balance_cents", topupRes.BalanceCents,
						)
					}
				}
			}
		}
		return false, ""
	}

	for {
		select {
		case <-ctx.Done():
			if reason := ctl.getReason(); reason != "" {
				closeReason = reason
			} else {
				closeReason = sessionEndReasonWorkerFailed
			}
			goto end
		case err := <-encErr:
			if errors.Is(err, ErrEncoderExited) || errors.Is(err, context.Canceled) {
				if reason := ctl.getReason(); reason != "" {
					closeReason = reason
				} else {
					closeReason = sessionEndReasonGraceful
				}
			} else {
				closeReason = sessionEndReasonWorkerFailed
				ctl.setReason(closeReason)
				r.cfg.Logger.Warn("liverunner.encoder_failed",
					"stream_id", v.StreamID, "err", err)
			}
			goto end
		case <-ticker.C:
			if shouldClose, reason := tickAndDecide(); shouldClose {
				closeReason = reason
				goto end
			}
		}
	}

end:
	// Best-effort one final tick to capture residual seconds, then
	// session-ended. We use a short fresh context because the session
	// ctx may already be cancelled by the time we get here.
	finalCtx, cancelFinal := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFinal()

	finalEncoded := ctl.encoder.EncodedSeconds()
	var endResp shellclient.SessionEndedResult
	if ctl.usePatternB {
		finalDebit := finalEncoded - lastDebited
		if finalDebit > 0 {
			units := int64(math.Ceil(finalDebit))
			seq++
			if r.cfg.Repo != nil {
				_, _ = r.cfg.Repo.IncrementDebitSeq(finalCtx, v.StreamID)
			}
			if _, err := r.cfg.Payment.DebitBalance(finalCtx, ctl.patternBSender, ctl.patternBWorkID, units, seq); err == nil {
				emitEvent(finalCtx, shellclient.WorkerEventInput{
					Type:            "session.usage.tick",
					UsageSeq:        seq,
					Units:           units,
					UnitType:        "seconds",
					RemainingRunway: 0,
					LowBalance:      closeReason == sessionEndReasonInsufficientBalance,
				})
			}
		}
		emitEvent(finalCtx, shellclient.WorkerEventInput{
			Type:       "session.ended",
			Reason:     closeReason,
			FinalUnits: int64(finalEncoded),
		})
		if err := r.cfg.Payment.CloseSession(finalCtx, ctl.patternBSender, ctl.patternBWorkID); err != nil {
			r.cfg.Logger.Warn("liverunner.close_session_failed",
				"stream_id", v.StreamID, "work_id", ctl.patternBWorkID, "err", err)
		}
		endResp = shellclient.SessionEndedResult{RecordingProcessing: v.RecordingEnabled}
	} else {
		finalDebit := finalEncoded - lastDebited
		if finalDebit > 0 {
			seq++
			_, _ = r.cfg.Shell.SessionTick(finalCtx, shellclient.SessionTickInput{
				StreamID: v.StreamID, Seq: seq,
				DebitSeconds: finalDebit, CumulativeSeconds: finalEncoded,
			})
		}

		var err error
		endResp, err = r.cfg.Shell.SessionEnded(finalCtx, shellclient.SessionEndedInput{
			StreamID:     v.StreamID,
			Reason:       closeReason,
			FinalSeq:     seq,
			FinalSeconds: finalEncoded,
		})
		if err != nil {
			r.cfg.Logger.Warn("liverunner.session_ended_failed",
				"stream_id", v.StreamID, "err", err)
		}
	}

	// Persist terminal phase.
	terminalPhase := types.StreamPhaseClosed
	if closeReason == sessionEndReasonInsufficientBalance {
		terminalPhase = types.StreamPhaseBalanceExhausted
	} else if closeReason == sessionEndReasonWorkerFailed {
		terminalPhase = types.StreamPhaseEncoderFailed
	}
	if r.cfg.Repo != nil {
		if cur, gerr := r.cfg.Repo.GetStream(finalCtx, v.StreamID); gerr == nil {
			cur.Phase = terminalPhase
			cur.ClosedAt = r.cfg.NowFn()
			cur.UnitsDebited = int64(finalEncoded)
			cur.CloseReason = closeReason
			cur.DebitSeq = seq
			cur.LowBalance = false
			cur.GraceUntil = time.Time{}
			_ = r.cfg.Repo.SaveStream(finalCtx, cur)
		}
	}

	// Wait for the mirror to drain its final scan so the segment list is
	// complete before we hand it to the bridge.
	mirrorWG.Wait()

	// Recording bridge: if shell flipped the row to recording_processing
	// AND we have a mirror, call recording-finalized with the segment
	// list. Without a mirror (e.g., dev mode + drain encoder) we skip;
	// the shell's stale-stream sweeper eventually times the row out.
	if endResp.RecordingProcessing && v.RecordingEnabled && ctl.mirror != nil {
		segs := ctl.mirror.Segments()
		if ctl.usePatternB {
			emitEvent(finalCtx, shellclient.WorkerEventInput{
				Type:                "session.recording.ready",
				MasterStorageKey:    ctl.mirror.MasterKey(),
				SegmentStorageKeys:  segs,
				TotalDurationSecond: finalEncoded,
			})
		} else {
			_, ferr := r.cfg.Shell.RecordingFinalized(finalCtx, shellclient.RecordingFinalizedInput{
				StreamID:           v.StreamID,
				SegmentStorageKeys: segs,
				MasterStorageKey:   ctl.mirror.MasterKey(),
				TotalDurationSec:   finalEncoded,
			})
			if ferr != nil {
				r.cfg.Logger.Warn("liverunner.recording_finalized_failed",
					"stream_id", v.StreamID, "err", ferr)
			} else {
				r.cfg.Logger.Info("liverunner.recording_finalized",
					"stream_id", v.StreamID,
					"segment_count", len(segs),
					"total_seconds", finalEncoded,
				)
			}
		}
	} else if endResp.RecordingProcessing && v.RecordingEnabled {
		r.cfg.Logger.Info("liverunner.recording_processing_no_mirror",
			"stream_id", v.StreamID,
			"final_seconds", finalEncoded,
		)
	}

	r.cfg.Logger.Info("liverunner.session_closed",
		"stream_id", v.StreamID,
		"reason", closeReason,
		"final_seconds", finalEncoded,
		"final_seq", seq,
	)
}

// Start opens a live stream entry without going through the RTMP path.
// Retained for compat with existing HTTP/gRPC routes; no per-stream
// goroutine is launched, so `done` stays nil and Stop returns immediately.
func (r *Runner) Start(ctx context.Context, req StartRequest) (types.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.streams[req.WorkID]; exists {
		return types.Stream{}, fmt.Errorf("liverunner: stream %s already started", req.WorkID)
	}
	stream := types.Stream{
		WorkID:           req.WorkID,
		GatewaySessionID: req.WorkID,
		WorkerSessionID:  req.WorkerSessionID,
		PaymentWorkID:    req.PaymentWorkID,
		Sender:           append([]byte(nil), req.Sender...),
		Phase:            types.StreamPhaseStarting,
		Preset:           req.Preset,
		StartedAt:        r.cfg.NowFn(),
	}
	if r.cfg.Repo != nil {
		if err := r.cfg.Repo.SaveStream(ctx, stream); err != nil {
			return types.Stream{}, fmt.Errorf("persist stream: %w", err)
		}
	}
	_, cancel := context.WithCancel(ctx)
	r.streams[req.WorkID] = &streamCtl{
		stream:          stream,
		cancel:          cancel,
		patternBSender:  append([]byte(nil), req.Sender...),
		patternBWorkID:  req.PaymentWorkID,
		workerSessionID: req.WorkerSessionID,
		usePatternB:     req.PaymentWorkID != "" && len(req.Sender) > 0,
	}
	return stream, nil
}

// Stop tells the per-stream goroutine to wind down. For legacy
// `Start`-opened streams (no goroutine) the entry is removed inline.
func (r *Runner) Stop(_ context.Context, workID string) error {
	r.mu.Lock()
	ctl, ok := r.streams[workID]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	if ctl.done == nil {
		// Legacy path: no goroutine to wait on, remove inline.
		ctl.stream.Phase = types.StreamPhaseClosed
		ctl.stream.CloseReason = sessionEndReasonAdminStop
		ctl.stream.ClosedAt = r.cfg.NowFn()
		if r.cfg.Repo != nil {
			_ = r.cfg.Repo.SaveStream(context.Background(), ctl.stream)
		}
		delete(r.streams, workID)
	}
	r.mu.Unlock()

	ctl.setReason(sessionEndReasonAdminStop)
	ctl.cancel()
	if ctl.done != nil {
		<-ctl.done
	}
	return nil
}

// Topup is invoked by the operator HTTP/gRPC surface; the shell's
// /internal/live/topup is the canonical path. Retained as a no-op so
// existing routes keep compiling.
func (r *Runner) Topup(ctx context.Context, workID string, _ []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctl, ok := r.streams[workID]; ok {
		ctl.stream.LastTopupAt = r.cfg.NowFn()
		if r.cfg.Repo != nil {
			if cur, err := r.cfg.Repo.GetStream(ctx, workID); err == nil {
				cur.LastTopupAt = ctl.stream.LastTopupAt
				_ = r.cfg.Repo.SaveStream(ctx, cur)
			}
		}
	}
	return nil
}

// Status returns a snapshot of the requested stream.
func (r *Runner) Status(_ context.Context, workID string) (types.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctl, ok := r.streams[workID]
	if !ok {
		return types.Stream{}, ErrStreamNotFound
	}
	return ctl.stream, nil
}

// ActiveCount returns the number of in-flight live streams.
func (r *Runner) ActiveCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// Shutdown cancels all in-flight streams and waits for their goroutines
// to drain. Legacy `Start`-opened streams (no goroutine) are deleted
// inline. Idempotent.
func (r *Runner) Shutdown(_ context.Context) {
	r.mu.Lock()
	dones := make([]chan struct{}, 0, len(r.streams))
	for id, ctl := range r.streams {
		ctl.setReason(sessionEndReasonAdminStop)
		ctl.cancel()
		if ctl.done == nil {
			delete(r.streams, id)
			continue
		}
		dones = append(dones, ctl.done)
	}
	r.mu.Unlock()
	for _, d := range dones {
		<-d
	}
}

func storagePrefix(template, streamID string) string {
	out := template
	for i := 0; i < len(out); i++ {
		const tok = "{stream_id}"
		if i+len(tok) <= len(out) && out[i:i+len(tok)] == tok {
			return out[:i] + streamID + out[i+len(tok):]
		}
	}
	return out
}
