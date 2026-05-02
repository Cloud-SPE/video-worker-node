package liverunner

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ingest"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/shellclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// fakeSession implements ingest.IngestSession.
type fakeSession struct {
	streamKey string
	reader    io.Reader
	closed    atomic.Bool
}

func newFakeSession(key string, body string) *fakeSession {
	return &fakeSession{streamKey: key, reader: strings.NewReader(body)}
}

func (*fakeSession) Protocol() ingest.Protocol { return ingest.ProtocolRTMP }
func (s *fakeSession) StreamKey() string       { return s.streamKey }
func (*fakeSession) MediaFormat() string       { return "flv" }
func (s *fakeSession) Reader() io.Reader       { return s.reader }
func (*fakeSession) RemoteAddr() string        { return "127.0.0.1:54321" }
func (s *fakeSession) Close() error            { s.closed.Store(true); return nil }

// scriptedEncoder is an Encoder whose EncodedSeconds advances on demand.
// Lets tests deterministically exercise tick math.
type scriptedEncoder struct {
	mu         sync.Mutex
	secondsX10 int64
	startCh    chan struct{}
	exit       chan error
}

func newScriptedEncoder() *scriptedEncoder {
	return &scriptedEncoder{startCh: make(chan struct{}), exit: make(chan error, 1)}
}

func (s *scriptedEncoder) Start(ctx context.Context, _ EncoderInput) error {
	close(s.startCh)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-s.exit:
		return err
	}
}

func (s *scriptedEncoder) EncodedSeconds() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return float64(s.secondsX10) / 10.0
}

func (s *scriptedEncoder) advance(seconds float64) {
	s.mu.Lock()
	s.secondsX10 += int64(seconds * 10)
	s.mu.Unlock()
}

func (s *scriptedEncoder) finish(err error) { s.exit <- err }

func newRunnerWithFakes(t *testing.T) (*Runner, *shellclient.Fake, func() *scriptedEncoder, *jobs.Repo) {
	t.Helper()
	repo := jobs.New(store.Memory())
	shell := shellclient.NewFake()
	var (
		mu  sync.Mutex
		enc *scriptedEncoder
	)
	factory := func() Encoder {
		mu.Lock()
		defer mu.Unlock()
		enc = newScriptedEncoder()
		return enc
	}
	getEnc := func() *scriptedEncoder {
		mu.Lock()
		defer mu.Unlock()
		return enc
	}
	r, err := New(Config{
		Repo:              repo,
		Shell:             shell,
		EncoderFactory:    factory,
		WorkerURL:         "http://worker:8080",
		LivePreset:        "h264-live",
		DebitCadence:      20 * time.Millisecond,
		DebitRetryBackoff: time.Millisecond,
		RunwaySeconds:     30,
		GraceSeconds:      1,
		PreCreditSeconds:  60,
		TopupMinInterval:  time.Millisecond,
		SleepFn:           func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return r, shell, getEnc, repo
}

func newPatternBRunnerWithFakes(t *testing.T) (*Runner, *shellclient.Fake, *paymentbroker.Fake, func() *scriptedEncoder, *jobs.Repo) {
	t.Helper()
	repo := jobs.New(store.Memory())
	shell := shellclient.NewFake()
	payment := paymentbroker.NewFake()
	var (
		mu  sync.Mutex
		enc *scriptedEncoder
	)
	factory := func() Encoder {
		mu.Lock()
		defer mu.Unlock()
		enc = newScriptedEncoder()
		return enc
	}
	getEnc := func() *scriptedEncoder {
		mu.Lock()
		defer mu.Unlock()
		return enc
	}
	r, err := New(Config{
		Repo:              repo,
		Shell:             shell,
		Payment:           payment,
		EncoderFactory:    factory,
		WorkerURL:         "http://worker:8080",
		LivePreset:        "h264-live",
		DebitCadence:      20 * time.Millisecond,
		DebitRetryBackoff: time.Millisecond,
		RunwaySeconds:     30,
		GraceSeconds:      1,
		SleepFn:           func(time.Duration) {},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return r, shell, payment, getEnc, repo
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func TestAcceptHappyPath(t *testing.T) {
	r, shell, getEnc, _ := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_TEST", "flv-bytes")

	acc, err := r.Accept(context.Background(), sess)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acc.OnEnd == nil {
		t.Fatal("OnEnd should be wired")
	}

	// validate-key + session-active fired exactly once.
	snap := shell.Snapshot()
	if len(snap.Validate) != 1 || snap.Validate[0].StreamKey != "sk_live_TEST" {
		t.Fatalf("validate-key calls: %+v", snap.Validate)
	}
	if len(snap.SessionActive) != 1 {
		t.Fatalf("session-active calls: %+v", snap.SessionActive)
	}

	// Encoder is now started; advance and let one tick land.
	enc := getEnc()
	if enc == nil {
		t.Fatal("encoder factory not invoked")
	}
	enc.advance(5)
	waitFor(t, func() bool { return len(shell.Snapshot().SessionTick) >= 1 }, time.Second, "first tick")

	// Drive to graceful close.
	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "session-ended")

	final := shell.Snapshot().SessionEnded[0]
	if final.Reason != "graceful" {
		t.Errorf("reason=%q want graceful", final.Reason)
	}
	if final.FinalSeconds < 5 {
		t.Errorf("FinalSeconds=%v want ≥5", final.FinalSeconds)
	}

	// ActiveCount drops back to zero.
	waitFor(t, func() bool { return r.ActiveCount() == 0 }, time.Second, "active-count drains")
}

func TestAcceptRejectedKey(t *testing.T) {
	r, shell, _, _ := newRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: false}, nil
	}

	sess := newFakeSession("sk_live_BOGUS", "")
	_, err := r.Accept(context.Background(), sess)
	if !errors.Is(err, ingest.ErrStreamKeyInvalid) {
		t.Fatalf("err=%v want ErrStreamKeyInvalid", err)
	}
	if r.ActiveCount() != 0 {
		t.Fatalf("ActiveCount=%d want 0", r.ActiveCount())
	}
	if len(shell.SessionActiveCalls) != 0 {
		t.Fatal("session-active must not fire on rejected key")
	}
}

func TestAcceptValidateError(t *testing.T) {
	r, shell, _, _ := newRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{}, errors.New("shell-down")
	}
	sess := newFakeSession("sk_live_X", "")
	_, err := r.Accept(context.Background(), sess)
	if err == nil {
		t.Fatal("expected error from validate-key failure")
	}
	if r.ActiveCount() != 0 {
		t.Fatalf("ActiveCount=%d want 0", r.ActiveCount())
	}
}

func TestTickAdvancesSeq(t *testing.T) {
	r, shell, getEnc, _ := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_X", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	enc := getEnc()
	enc.advance(1)
	waitFor(t, func() bool { return len(shell.Snapshot().SessionTick) >= 1 }, time.Second, "first tick")
	enc.advance(1)
	waitFor(t, func() bool { return len(shell.Snapshot().SessionTick) >= 2 }, time.Second, "second tick")

	ticks := shell.Snapshot().SessionTick
	if ticks[0].Seq >= ticks[1].Seq {
		t.Errorf("seq must monotonically increase, got %d → %d", ticks[0].Seq, ticks[1].Seq)
	}
	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "ended")
}

func TestRunwayTriggersTopup(t *testing.T) {
	r, shell, getEnc, _ := newRunnerWithFakes(t)
	// Make every tick report runway 5s — well below the 30s threshold.
	shell.SessionTickFunc = func(_ context.Context, _ shellclient.SessionTickInput) (shellclient.SessionTickResult, error) {
		return shellclient.SessionTickResult{BalanceCents: 100, RunwaySeconds: 5, GraceTriggered: false}, nil
	}

	sess := newFakeSession("sk_live_X", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	enc := getEnc()
	enc.advance(1)
	waitFor(t, func() bool { return len(shell.Snapshot().Topup) >= 1 }, time.Second, "topup")

	t1 := shell.Snapshot().Topup[0]
	if t1.RequestSeconds != 60 {
		t.Errorf("RequestSeconds=%d want 60 (PreCreditSeconds default)", t1.RequestSeconds)
	}
	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "ended")
}

func TestGraceExpiryClosesStream(t *testing.T) {
	r, shell, getEnc, _ := newRunnerWithFakes(t)
	shell.SessionTickFunc = func(_ context.Context, _ shellclient.SessionTickInput) (shellclient.SessionTickResult, error) {
		return shellclient.SessionTickResult{BalanceCents: 0, RunwaySeconds: 0, GraceTriggered: true}, nil
	}
	shell.TopupFunc = func(_ context.Context, _ shellclient.TopupInput) (shellclient.TopupResult, error) {
		return shellclient.TopupResult{Succeeded: false, AuthorizedCents: 0, BalanceCents: 0}, nil
	}

	sess := newFakeSession("sk_live_X", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	enc := getEnc()
	enc.advance(1)

	// Wait for session-ended to land. Grace deadline = 1s in newRunnerWithFakes.
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, 5*time.Second, "session-ended after grace")

	end := shell.Snapshot().SessionEnded[0]
	if end.Reason != "insufficient_balance" {
		t.Errorf("reason=%q want insufficient_balance", end.Reason)
	}
}

func TestSessionEndedCalledOnEncoderError(t *testing.T) {
	r, shell, getEnc, _ := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_X", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	enc := getEnc()
	enc.finish(errors.New("boom"))
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "ended")
	end := shell.Snapshot().SessionEnded[0]
	if end.Reason != "session_worker_failed" {
		t.Errorf("reason=%q want session_worker_failed", end.Reason)
	}
}

func TestIngestCloseEndsGracefully(t *testing.T) {
	r, shell, _, _ := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_close", "")
	acc, err := r.Accept(context.Background(), sess)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	acc.OnEnd("close")
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "ended after close")
	end := shell.Snapshot().SessionEnded[0]
	if end.Reason != "graceful" {
		t.Fatalf("reason=%q want graceful", end.Reason)
	}
	if !sess.closed.Load() {
		t.Fatal("session must be closed on ingest close")
	}
}

func TestIngestFailureMapsToSessionWorkerFailed(t *testing.T) {
	r, shell, _, _ := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_transport", "")
	acc, err := r.Accept(context.Background(), sess)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	acc.OnEnd("transport_error")
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "ended after ingest failure")
	end := shell.Snapshot().SessionEnded[0]
	if end.Reason != "session_worker_failed" {
		t.Fatalf("reason=%q want session_worker_failed", end.Reason)
	}
}

func TestStopMapsToAdminStop(t *testing.T) {
	r, shell, _, _ := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_admin", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := r.Stop(context.Background(), "live_fake"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, func() bool { return len(shell.Snapshot().SessionEnded) == 1 }, time.Second, "ended after stop")
	end := shell.Snapshot().SessionEnded[0]
	if end.Reason != "admin_stop" {
		t.Fatalf("reason=%q want admin_stop", end.Reason)
	}
}

func TestPersistsStreamRecordOnAccept(t *testing.T) {
	r, _, _, repo := newRunnerWithFakes(t)
	sess := newFakeSession("sk_live_X", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	got, err := repo.GetStream(context.Background(), "live_fake")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if got.WorkID != "live_fake" {
		t.Errorf("WorkID=%q", got.WorkID)
	}
}

func TestAcceptPatternBUsesLocalPaymentAndEvents(t *testing.T) {
	r, shell, payment, getEnc, repo := newPatternBRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: true, StreamID: "gw_123", ProjectID: "proj_fake", RecordingEnabled: false}, nil
	}
	if _, err := r.Start(context.Background(), StartRequest{
		WorkID:          "gw_123",
		Sender:          []byte("fake-sender"),
		PaymentWorkID:   "work_123",
		WorkerSessionID: "worker_123",
		Preset:          "h264-live",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	payment.CreditFor("work_123", 120)

	sess := newFakeSession("sk_live_pattern_b", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if len(shell.SessionActiveCalls) != 0 {
		t.Fatalf("legacy session-active should not fire in Pattern B path")
	}
	got, err := repo.GetStream(context.Background(), "gw_123")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if got.GatewaySessionID != "gw_123" || got.WorkerSessionID != "worker_123" || got.PaymentWorkID != "work_123" {
		t.Fatalf("unexpected persisted correlation state: %+v", got)
	}

	enc := getEnc()
	if enc == nil {
		t.Fatal("encoder factory not invoked")
	}
	enc.advance(5)
	waitFor(t, func() bool { return len(shell.Snapshot().Events) >= 2 }, time.Second, "pattern-b events")

	events := shell.Snapshot().Events
	if events[0].Type != "session.ready" {
		t.Fatalf("first event type=%q want session.ready", events[0].Type)
	}
	foundTick := false
	for _, ev := range events {
		if ev.Type == "session.usage.tick" {
			foundTick = true
			if ev.WorkID != "work_123" {
				t.Fatalf("tick work_id=%q want work_123", ev.WorkID)
			}
			if ev.WorkerSessionID != "worker_123" {
				t.Fatalf("tick worker_session_id=%q want worker_123", ev.WorkerSessionID)
			}
		}
	}
	if !foundTick {
		t.Fatal("expected session.usage.tick event")
	}

	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.ended" {
				return true
			}
		}
		return false
	}, time.Second, "session.ended")
	if !payment.IsClosed("work_123") {
		t.Fatal("expected payment session to be closed")
	}
}

func TestPatternBStopUsesFreshContextForTerminalEvent(t *testing.T) {
	r, shell, payment, _, _ := newPatternBRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: true, StreamID: "gw_123", ProjectID: "proj_fake", RecordingEnabled: false}, nil
	}
	var endedPosted atomic.Bool
	shell.PostEventFunc = func(ctx context.Context, in shellclient.WorkerEventInput) error {
		if in.Type == "session.ended" {
			if err := ctx.Err(); err != nil {
				return err
			}
			endedPosted.Store(true)
		}
		return nil
	}
	if _, err := r.Start(context.Background(), StartRequest{
		WorkID:          "gw_123",
		Sender:          []byte("fake-sender"),
		PaymentWorkID:   "work_123",
		WorkerSessionID: "worker_123",
		Preset:          "h264-live",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	payment.CreditFor("work_123", 120)

	sess := newFakeSession("sk_live_pattern_b", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := r.Stop(context.Background(), "gw_123"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, endedPosted.Load, time.Second, "pattern-b session.ended event")
}

func TestPatternBLowBalancePersistsDurableState(t *testing.T) {
	r, shell, payment, getEnc, repo := newPatternBRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: true, StreamID: "gw_123", ProjectID: "proj_fake", RecordingEnabled: false}, nil
	}
	if _, err := r.Start(context.Background(), StartRequest{
		WorkID:          "gw_123",
		Sender:          []byte("fake-sender"),
		PaymentWorkID:   "work_123",
		WorkerSessionID: "worker_123",
		Preset:          "h264-live",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	payment.CreditFor("work_123", 5)

	sess := newFakeSession("sk_live_pattern_b", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	enc := getEnc()
	enc.advance(1)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.balance.low" {
				return true
			}
		}
		return false
	}, time.Second, "pattern-b low-balance event")

	got, err := repo.GetStream(context.Background(), "gw_123")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if got.Phase != types.StreamPhaseLowBalance {
		t.Fatalf("Phase=%q want %q", got.Phase, types.StreamPhaseLowBalance)
	}
	if !got.LowBalance {
		t.Fatal("expected LowBalance=true")
	}
	if got.GraceUntil.IsZero() {
		t.Fatal("expected GraceUntil to be set")
	}

	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.ended" {
				return true
			}
		}
		return false
	}, time.Second, "session.ended")
}

func TestPatternBRefillClearsDurableLowBalanceState(t *testing.T) {
	r, shell, payment, getEnc, repo := newPatternBRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: true, StreamID: "gw_123", ProjectID: "proj_fake", RecordingEnabled: false}, nil
	}
	if _, err := r.Start(context.Background(), StartRequest{
		WorkID:          "gw_123",
		Sender:          []byte("fake-sender"),
		PaymentWorkID:   "work_123",
		WorkerSessionID: "worker_123",
		Preset:          "h264-live",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	payment.CreditFor("work_123", 5)

	sess := newFakeSession("sk_live_pattern_b", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	enc := getEnc()
	enc.advance(1)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.balance.low" {
				return true
			}
		}
		return false
	}, time.Second, "pattern-b low-balance event")

	payment.CreditFor("work_123", 100)
	enc.advance(1)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.balance.refilled" {
				return true
			}
		}
		return false
	}, time.Second, "pattern-b refilled event")

	got, err := repo.GetStream(context.Background(), "gw_123")
	if err != nil {
		t.Fatalf("GetStream: %v", err)
	}
	if got.Phase != types.StreamPhaseStreaming {
		t.Fatalf("Phase=%q want %q", got.Phase, types.StreamPhaseStreaming)
	}
	if got.LowBalance {
		t.Fatal("expected LowBalance=false")
	}
	if !got.GraceUntil.IsZero() {
		t.Fatal("expected GraceUntil to be cleared")
	}

	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.ended" {
				return true
			}
		}
		return false
	}, time.Second, "session.ended")
}

func TestPatternBEventPostingRetriesTransientFailure(t *testing.T) {
	r, shell, payment, getEnc, _ := newPatternBRunnerWithFakes(t)
	shell.ValidateFunc = func(_ context.Context, _ shellclient.ValidateKeyInput) (shellclient.ValidateKeyResult, error) {
		return shellclient.ValidateKeyResult{Accepted: true, StreamID: "gw_123", ProjectID: "proj_fake", RecordingEnabled: false}, nil
	}
	var readyAttempts atomic.Int32
	shell.PostEventFunc = func(_ context.Context, in shellclient.WorkerEventInput) error {
		if in.Type == "session.ready" {
			n := readyAttempts.Add(1)
			if n < 3 {
				return errors.New("transient event ingest failure")
			}
		}
		return nil
	}
	if _, err := r.Start(context.Background(), StartRequest{
		WorkID:          "gw_123",
		Sender:          []byte("fake-sender"),
		PaymentWorkID:   "work_123",
		WorkerSessionID: "worker_123",
		Preset:          "h264-live",
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	payment.CreditFor("work_123", 120)

	sess := newFakeSession("sk_live_pattern_b", "")
	if _, err := r.Accept(context.Background(), sess); err != nil {
		t.Fatalf("accept: %v", err)
	}
	waitFor(t, func() bool { return readyAttempts.Load() == 3 }, time.Second, "event post retries")

	enc := getEnc()
	if enc == nil {
		t.Fatal("encoder factory not invoked")
	}
	enc.finish(ErrEncoderExited)
	waitFor(t, func() bool {
		for _, ev := range shell.Snapshot().Events {
			if ev.Type == "session.ended" {
				return true
			}
		}
		return false
	}, time.Second, "session.ended")
}
