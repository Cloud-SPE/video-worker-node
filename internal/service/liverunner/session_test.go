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
		Repo:           repo,
		Shell:          shell,
		EncoderFactory: factory,
		WorkerURL:      "http://worker:8080",
		LivePreset:     "h264-live",
		DebitCadence:   20 * time.Millisecond,
		RunwaySeconds:  30,
		GraceSeconds:   1,
		PreCreditSeconds: 60,
		TopupMinInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return r, shell, getEnc, repo
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
	if end.Reason != "worker_error" {
		t.Errorf("reason=%q want worker_error", end.Reason)
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
