package abrrunner

import (
	"context"
	"errors"
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
	os.WriteFile(presetsPath, []byte(`presets:
  - name: 360p
    codec: h264
    width_max: 640
    height_max: 360
    bitrate_kbps: 800
  - name: 720p
    codec: h264
    width_max: 1280
    height_max: 720
    bitrate_kbps: 2500
  - name: 1080p
    codec: h264
    width_max: 1920
    height_max: 1080
    bitrate_kbps: 5000
`), 0o600)
	pl, _ := presetloader.New(presetsPath)
	st := store.Memory()
	repo := jobs.New(st)
	stg := storage.NewFake()
	stg.Inputs["http://in/x"] = []byte("video bytes")
	pay := paymentbroker.NewFake()
	pay.CreditFor("w", 9999)
	wh := &webhooks.FakeSender{}
	r, err := New(Config{
		Repo: repo, FFmpeg: &ffmpeg.FakeRunner{Steps: 1},
		Probe:   probe.FakeProber{R: probe.Result{Width: 1, Height: 1}},
		Storage: stg, Webhook: wh, Payment: pay, Presets: pl,
		GPU:     types.GPUProfile{Vendor: types.GPUVendorNVIDIA, SupportsH264: true},
		TempDir: dir, Logger: logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, stg, wh, pay, repo
}

func TestSubmitAndRunHappyPath(t *testing.T) {
	t.Parallel()
	r, stg, wh, pay, repo := newTestEnv(t)
	plan := ABRJob{
		JobID: "j1", InputURL: "http://in/x", MasterOutputURL: "http://master/",
		WebhookURL: "http://hook/", WebhookSecret: "s",
		WorkID: "w", UnitsPerRend: 5, Sender: []byte("s"),
		PresetNames: []string{"360p", "720p", "1080p"},
		RenditionOutputs: map[string]string{
			"360p": "http://r/360", "720p": "http://r/720", "1080p": "http://r/1080",
		},
	}
	if err := r.Submit(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if r.QueueDepth() != 1 {
		t.Errorf("depth=%d", r.QueueDepth())
	}
	if err := r.RunOne(context.Background(), "j1", plan); err != nil {
		t.Fatal(err)
	}
	final, _ := repo.Get(context.Background(), "j1")
	if final.Phase != types.PhaseComplete {
		t.Errorf("phase=%s", final.Phase)
	}
	for _, u := range []string{"http://r/360", "http://r/720", "http://r/1080", "http://master/"} {
		if _, ok := stg.Uploads[u]; !ok {
			t.Errorf("missing upload: %s", u)
		}
	}
	if len(pay.Debits()) != 3 {
		t.Errorf("expected 3 debits, got %d", len(pay.Debits()))
	}
	time.Sleep(50 * time.Millisecond)
	if len(wh.EventsByName("job.rendition.complete")) != 3 {
		t.Errorf("rendition webhooks: %d", len(wh.EventsByName("job.rendition.complete")))
	}
}

func TestSubmitInvalidPreset(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := newTestEnv(t)
	err := r.Submit(context.Background(), ABRJob{JobID: "j", PresetNames: []string{"ghost"}})
	if err == nil {
		t.Fatal("expected invalid-preset error")
	}
}

func TestSubmitMissingFields(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := newTestEnv(t)
	if err := r.Submit(context.Background(), ABRJob{}); err == nil {
		t.Fatal("expected jobid error")
	}
	if err := r.Submit(context.Background(), ABRJob{JobID: "x"}); err == nil {
		t.Fatal("expected presets error")
	}
}

func TestRunDownloadFails(t *testing.T) {
	t.Parallel()
	r, stg, _, _, repo := newTestEnv(t)
	stg.FailFetch = "http://broken/"
	plan := ABRJob{JobID: "j", InputURL: "http://broken/", PresetNames: []string{"360p"}, RenditionOutputs: map[string]string{"360p": "u"}}
	r.Submit(context.Background(), plan)
	if err := r.RunOne(context.Background(), "j", plan); err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j")
	if final.ErrorCode != types.ErrCodeDownloadFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunMissingRenditionOutput(t *testing.T) {
	t.Parallel()
	r, _, _, _, repo := newTestEnv(t)
	plan := ABRJob{JobID: "j", InputURL: "http://in/x", PresetNames: []string{"360p"}, RenditionOutputs: map[string]string{}}
	r.Submit(context.Background(), plan)
	err := r.RunOne(context.Background(), "j", plan)
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j")
	if final.ErrorCode != types.ErrCodeUploadFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunPaymentFails(t *testing.T) {
	t.Parallel()
	r, _, _, pay, repo := newTestEnv(t)
	pay.FailDebit = errors.New("boom")
	plan := ABRJob{
		JobID: "j", InputURL: "http://in/x", PresetNames: []string{"360p"},
		WorkID: "w", UnitsPerRend: 1, MasterOutputURL: "m",
		RenditionOutputs: map[string]string{"360p": "u"},
	}
	r.Submit(context.Background(), plan)
	err := r.RunOne(context.Background(), "j", plan)
	if err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j")
	if final.ErrorCode != types.ErrCodePaymentFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunFFmpegFails(t *testing.T) {
	t.Parallel()
	r, _, _, _, repo := newTestEnv(t)
	r.cfg.FFmpeg = &ffmpeg.FakeRunner{FailWithExit: 1}
	plan := ABRJob{
		JobID: "j", InputURL: "http://in/x", PresetNames: []string{"360p"},
		MasterOutputURL:  "m",
		RenditionOutputs: map[string]string{"360p": "u"},
	}
	r.Submit(context.Background(), plan)
	if err := r.RunOne(context.Background(), "j", plan); err == nil {
		t.Fatal("expected error")
	}
	final, _ := repo.Get(context.Background(), "j")
	if final.ErrorCode != types.ErrCodeEncodingFailed {
		t.Errorf("code=%s", final.ErrorCode)
	}
}

func TestRunLoopExitsOnCtx(t *testing.T) {
	t.Parallel()
	r, _, _, _, _ := newTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.Run(ctx, func(string) (ABRJob, bool) { return ABRJob{}, false }); err != context.DeadlineExceeded {
		t.Errorf("err=%v", err)
	}
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

func TestPresetListPseudoName(t *testing.T) {
	t.Parallel()
	if got := presetListPseudoName([]string{"a", "b", "c"}); got != "a+b+c" {
		t.Errorf("got=%q", got)
	}
	if got := presetListPseudoName(nil); got != "" {
		t.Errorf("got=%q", got)
	}
}
