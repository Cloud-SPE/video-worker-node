package jobrunner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ffmpeg"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/logger"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/probe"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/storage"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/webhooks"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func newTestEnv(t *testing.T) (*Runner, *storage.Fake, *webhooks.FakeSender, *paymentbroker.Fake, *jobs.Repo) {
	t.Helper()
	dir := t.TempDir()
	presetsPath := filepath.Join(dir, "presets.yaml")
	if err := os.WriteFile(presetsPath, []byte(`presets:
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
`), 0o600); err != nil {
		t.Fatal(err)
	}
	pl, err := presetloader.New(presetsPath)
	if err != nil {
		t.Fatal(err)
	}
	st := store.Memory()
	repo := jobs.New(st)
	stg := storage.NewFake()
	stg.Inputs["http://in/x"] = []byte("video bytes")
	wh := &webhooks.FakeSender{}
	pay := paymentbroker.NewFake()
	r, err := New(Config{
		Repo: repo, FFmpeg: &ffmpeg.FakeRunner{Steps: 2},
		Probe:   probe.FakeProber{R: probe.Result{Width: 1, Height: 1}},
		Storage: stg, Webhook: wh, Payment: pay, Presets: pl,
		GPU:     types.GPUProfile{Vendor: types.GPUVendorNVIDIA, SupportsH264: true},
		TempDir: dir, MaxQueue: 5, Logger: logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, stg, wh, pay, repo
}

func TestSubmitAndRunHappyPath(t *testing.T) {
	t.Parallel()
	r, stg, wh, pay, repo := newTestEnv(t)
	pay.CreditFor("w1", 1000)
	job, err := r.Submit(context.Background(), types.Job{
		ID: "j1", InputURL: "http://in/x", OutputURL: "http://out/y",
		Preset: "720p", WorkID: "w1", UnitsPer: 10,
		WebhookURL: "http://hook/", WebhookSecret: "shh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != types.PhaseQueued {
		t.Errorf("phase=%s", job.Phase)
	}
	if r.QueueDepth() != 1 {
		t.Errorf("depth=%d", r.QueueDepth())
	}
	if err := r.RunOne(context.Background(), "j1"); err != nil {
		t.Fatal(err)
	}
	final, _ := repo.Get(context.Background(), "j1")
	if final.Phase != types.PhaseComplete {
		t.Fatalf("phase=%s", final.Phase)
	}
	// Should have uploaded
	if _, ok := stg.Uploads["http://out/y"]; !ok {
		t.Error("upload missing")
	}
	// Should have debited
	if len(pay.Debits()) != 1 {
		t.Errorf("debits=%v", pay.Debits())
	}
	// Should have fired webhooks for each transition + complete
	time.Sleep(50 * time.Millisecond) // webhook is fire-and-forget
	events := wh.Events()
	if len(events) == 0 {
		t.Error("no webhooks delivered")
	}
}

func TestSubmitInvalidPreset(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := newTestEnv(t)
	_, err := r.Submit(context.Background(), types.Job{ID: "j", Preset: "ghost"})
	if err == nil {
		t.Fatal("expected error")
	}
	je, ok := err.(*types.JobError)
	if !ok || je.Code != types.ErrCodeJobInvalidPreset {
		t.Fatalf("err=%v", err)
	}
}

func TestSubmitEmptyID(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := newTestEnv(t)
	if _, err := r.Submit(context.Background(), types.Job{Preset: "720p"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunDownloadFailed(t *testing.T) {
	t.Parallel()
	r, stg, _, _, repo := newTestEnv(t)
	stg.FailFetch = "http://broken/"
	r.Submit(context.Background(), types.Job{ID: "j2", InputURL: "http://broken/", OutputURL: "out", Preset: "720p"})
	err := r.RunOne(context.Background(), "j2")
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j2")
	if final.ErrorCode != types.ErrCodeDownloadFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunEncodingFailed(t *testing.T) {
	t.Parallel()
	r, _, _, _, repo := newTestEnv(t)
	r.cfg.FFmpeg = &ffmpeg.FakeRunner{Steps: 1, FailWithExit: 13, FailedStderr: "broken"}
	r.Submit(context.Background(), types.Job{ID: "j3", InputURL: "http://in/x", OutputURL: "out", Preset: "720p"})
	r.cfg.Storage.(*storage.Fake).Inputs["http://in/x"] = []byte("hi")
	err := r.RunOne(context.Background(), "j3")
	if err == nil {
		t.Fatal("expected encoding error")
	}
	final, _ := repo.Get(context.Background(), "j3")
	if final.ErrorCode != types.ErrCodeEncodingFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunUploadFailed(t *testing.T) {
	t.Parallel()
	r, stg, _, _, repo := newTestEnv(t)
	stg.FailUpload = "http://out/y"
	r.Submit(context.Background(), types.Job{ID: "j4", InputURL: "http://in/x", OutputURL: "http://out/y", Preset: "720p"})
	err := r.RunOne(context.Background(), "j4")
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j4")
	if final.ErrorCode != types.ErrCodeUploadFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunPaymentFailed(t *testing.T) {
	t.Parallel()
	r, _, _, pay, repo := newTestEnv(t)
	pay.FailDebit = errInjected
	r.Submit(context.Background(), types.Job{
		ID: "j5", InputURL: "http://in/x", OutputURL: "out", Preset: "720p",
		WorkID: "w", UnitsPer: 10,
	})
	err := r.RunOne(context.Background(), "j5")
	if err == nil {
		t.Fatal("expected payment error")
	}
	final, _ := repo.Get(context.Background(), "j5")
	if final.ErrorCode != types.ErrCodePaymentFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

var errInjected = errInjectedSentinel{}

type errInjectedSentinel struct{}

func (errInjectedSentinel) Error() string { return "injected" }

func TestResumeAll(t *testing.T) {
	t.Parallel()
	r, _, _, _, repo := newTestEnv(t)
	repo.Save(context.Background(), types.Job{ID: "a", Mode: types.ModeVOD, Phase: types.PhaseEncoding})
	repo.Save(context.Background(), types.Job{ID: "b", Mode: types.ModeVOD, Phase: types.PhaseComplete})
	repo.Save(context.Background(), types.Job{ID: "c", Mode: types.ModeABR, Phase: types.PhaseEncoding})
	if err := r.ResumeAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.QueueDepth() != 1 {
		t.Errorf("queue=%d (only the VOD non-terminal should resume)", r.QueueDepth())
	}
}

func TestRunInvalidPreset(t *testing.T) {
	t.Parallel()
	r, _, _, _, repo := newTestEnv(t)
	repo.Save(context.Background(), types.Job{ID: "j", Mode: types.ModeVOD, Preset: "ghost", InputURL: "x", OutputURL: "y"})
	err := r.RunOne(context.Background(), "j")
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j")
	if final.ErrorCode != types.ErrCodeJobInvalidPreset {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunLoopExitsOnCtx(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := newTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.Run(ctx); err != context.DeadlineExceeded {
		t.Errorf("err=%v", err)
	}
}

func TestRunUsesWorkerPool(t *testing.T) {
	t.Parallel()
	r, stg, _, _, _ := newTestEnv(t)
	r.cfg.FFmpeg = &ffmpeg.FakeRunner{Steps: 5, PerStep: 100 * time.Millisecond}
	stg.Inputs["http://in/y"] = []byte("video bytes")
	for _, job := range []types.Job{
		{ID: "j1", InputURL: "http://in/x", OutputURL: "http://out/1", Preset: "720p"},
		{ID: "j2", InputURL: "http://in/y", OutputURL: "http://out/2", Preset: "720p"},
	} {
		if _, err := r.Submit(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- r.Run(ctx)
	}()

	waitForActiveJobs(t, r, 2)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("run err=%v", err)
	}
}

func waitForActiveJobs(t *testing.T, r *Runner, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.ActiveCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("active jobs=%d, want >= %d", r.ActiveCount(), want)
}

func TestNewValidations(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected repo error")
	}
	st := store.Memory()
	repo := jobs.New(st)
	if _, err := New(Config{Repo: repo}); err == nil {
		t.Fatal("expected ffmpeg error")
	}
	if _, err := New(Config{Repo: repo, FFmpeg: &ffmpeg.FakeRunner{}}); err == nil {
		t.Fatal("expected storage error")
	}
	if _, err := New(Config{Repo: repo, FFmpeg: &ffmpeg.FakeRunner{}, Storage: storage.NewFake()}); err == nil {
		t.Fatal("expected presets error")
	}
}
