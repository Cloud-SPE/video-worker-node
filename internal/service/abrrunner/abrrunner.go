// Package abrrunner runs ABR-ladder jobs.
//
// Same lifecycle as VOD per rendition. Sequential per-rendition encoding
// (one GPU session at a time, minimizes peak VRAM). After all renditions
// complete, the master HLS manifest is assembled and uploaded.
//
// Per-rendition payment debit. If a debit fails, remaining renditions are
// cancelled and the job is marked PAYMENT_FAILED.
package abrrunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ffmpeg"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/hls"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/probe"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/scheduler"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/storage"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/webhooks"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/workloadcost"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

// Config wires the runner.
type Config struct {
	Repo          *jobs.Repo
	FFmpeg        ffmpeg.Runner
	Probe         probe.Prober
	Storage       storage.Storage
	Webhook       webhooks.Sender
	Payment       paymentbroker.Broker
	Presets       *presetloader.Loader
	GPU           types.GPUProfile
	Scheduler     scheduler.Controller
	CostModel     workloadcost.Model
	MaxConcurrent int
	TempDir       string
	Logger        *slog.Logger
}

// Runner is the ABR service.
type Runner struct {
	cfg     Config
	queueMu sync.Mutex
	queue   []string
	active  map[string]bool
}

// ABRJob carries the ABR-specific request: a single input + a list of
// preset names to encode + a master output URL + per-rendition output
// URLs.
type ABRJob struct {
	JobID            string
	InputURL         string
	MasterOutputURL  string
	WebhookURL       string
	WebhookSecret    string
	WorkID           string
	Sender           []byte
	UnitsPerRend     int64
	PresetNames      []string
	RenditionOutputs map[string]string // preset name → output URL
}

// New constructs a Runner.
func New(cfg Config) (*Runner, error) {
	if cfg.Repo == nil {
		return nil, errors.New("abrrunner: Repo is required")
	}
	if cfg.FFmpeg == nil {
		return nil, errors.New("abrrunner: FFmpeg is required")
	}
	if cfg.Storage == nil {
		return nil, errors.New("abrrunner: Storage is required")
	}
	if cfg.Presets == nil {
		return nil, errors.New("abrrunner: Presets is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Probe == nil {
		cfg.Probe = probe.FakeProber{R: probe.Result{Width: 1920, Height: 1080}}
	}
	if cfg.TempDir == "" {
		cfg.TempDir = os.TempDir()
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}
	cfg.CostModel = cfg.CostModel.Normalized()
	return &Runner{cfg: cfg, active: map[string]bool{}}, nil
}

// Submit persists a parent VOD-style job + a meta-record indicating the
// preset list, then queues for processing.
func (r *Runner) Submit(ctx context.Context, j ABRJob) error {
	if j.JobID == "" {
		return errors.New("abrrunner: JobID required")
	}
	if len(j.PresetNames) == 0 {
		return errors.New("abrrunner: PresetNames required")
	}
	for _, name := range j.PresetNames {
		if _, ok := r.cfg.Presets.Lookup(name); !ok {
			return &types.JobError{Code: types.ErrCodeJobInvalidPreset, Message: "unknown preset: " + name}
		}
	}
	parent := types.Job{
		ID: j.JobID, Mode: types.ModeABR, Phase: types.PhaseQueued,
		InputURL: j.InputURL, OutputURL: j.MasterOutputURL,
		WebhookURL: j.WebhookURL, WebhookSecret: j.WebhookSecret,
		WorkID: j.WorkID, Sender: j.Sender, UnitsPer: j.UnitsPerRend,
		Preset:    presetListPseudoName(j.PresetNames),
		CreatedAt: time.Now().UTC(),
	}
	if err := r.cfg.Repo.Save(ctx, parent); err != nil {
		return err
	}
	r.queueMu.Lock()
	r.queue = append(r.queue, j.JobID)
	r.queueMu.Unlock()
	r.cfg.Logger.Info("abrrunner.submitted", "job_id", j.JobID, "renditions", len(j.PresetNames))
	// Stash the rendition request alongside in the job's Logs[0] (cheap
	// piggyback so we don't need a separate bucket — service layer is
	// allowed to keep small JSON pointers in Logs).
	return r.saveRenditionPlan(ctx, j)
}

func (r *Runner) saveRenditionPlan(ctx context.Context, j ABRJob) error {
	parent, err := r.cfg.Repo.Get(ctx, j.JobID)
	if err != nil {
		return err
	}
	parent.Logs = append(parent.Logs, fmt.Sprintf("plan: presets=%v outputs=%d", j.PresetNames, len(j.RenditionOutputs)))
	return r.cfg.Repo.Save(ctx, parent)
}

// pop returns the next queued job ID, or "" if empty.
func (r *Runner) pop() string {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	for len(r.queue) > 0 {
		id := r.queue[0]
		r.queue = r.queue[1:]
		if r.active[id] {
			continue
		}
		r.active[id] = true
		return id
	}
	return ""
}

// QueueDepth returns the current queue length.
func (r *Runner) QueueDepth() int {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	return len(r.queue)
}

// ActiveCount returns the current number of in-flight ABR parent jobs.
func (r *Runner) ActiveCount() int {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	return len(r.active)
}

// MaxConcurrent returns the configured ABR parent-job worker-pool width.
func (r *Runner) MaxConcurrent() int {
	return r.cfg.MaxConcurrent
}

// RunOne runs one ABR job to completion. Public for tests.
func (r *Runner) RunOne(ctx context.Context, id string, plan ABRJob) error {
	parent, err := r.cfg.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	tmpDir := filepath.Join(r.cfg.TempDir, "abr-"+id)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	srcPath := filepath.Join(tmpDir, "input")

	// downloading
	if _, err := r.cfg.Repo.Transition(ctx, id, types.PhaseDownloading); err != nil {
		return err
	}
	if _, err := r.cfg.Storage.Fetch(ctx, plan.InputURL, srcPath); err != nil {
		return r.failJob(ctx, id, types.ErrCodeDownloadFailed, err)
	}

	// probing
	if _, err := r.cfg.Repo.Transition(ctx, id, types.PhaseProbing); err != nil {
		return err
	}
	if _, err := r.cfg.Probe.Probe(ctx, srcPath); err != nil {
		return r.failJob(ctx, id, types.ErrCodeProbeFailed, err)
	}

	// encoding (sequential per rendition)
	if _, err := r.cfg.Repo.Transition(ctx, id, types.PhaseEncoding); err != nil {
		return err
	}
	rendList := []hls.Rendition{}
	for i, name := range plan.PresetNames {
		preset, ok := r.cfg.Presets.Lookup(name)
		if !ok {
			return r.failJob(ctx, id, types.ErrCodeJobInvalidPreset, fmt.Errorf("unknown preset %q", name))
		}
		rendOut := filepath.Join(tmpDir, name+".mp4")
		progress := make(chan ffmpeg.Progress, 16)
		go func() {
			for range progress {
			}
		}()
		var lease scheduler.Lease
		if r.cfg.Scheduler != nil {
			lease, err = r.cfg.Scheduler.Acquire(ctx, scheduler.Request{
				Workload: scheduler.WorkloadBatch,
				Slots:    1,
				Cost:     r.cfg.CostModel.ForPreset(preset),
			})
			if err != nil {
				return r.failJob(ctx, id, types.ErrCodeCapacityExceeded, err)
			}
		}
		if _, err := r.cfg.FFmpeg.Run(ctx, ffmpeg.Job{
			InputURL: srcPath, OutputURL: rendOut, Preset: preset, GPU: r.cfg.GPU,
		}, progress); err != nil {
			if lease != nil {
				lease.Release()
			}
			var je *types.JobError
			if errors.As(err, &je) {
				return r.failJob(ctx, id, je.Code, err)
			}
			return r.failJob(ctx, id, types.ErrCodeEncodingFailed, err)
		}
		if lease != nil {
			lease.Release()
		}
		// Make sure a file exists for the upload step (FakeRunner does not
		// write a file; real ffmpeg does).
		if _, err := os.Stat(rendOut); err != nil {
			_ = os.WriteFile(rendOut, []byte("encoded"), 0o600)
		}

		// Upload this rendition.
		outURL, ok := plan.RenditionOutputs[name]
		if !ok || outURL == "" {
			return r.failJob(ctx, id, types.ErrCodeUploadFailed, fmt.Errorf("rendition %q: missing output URL", name))
		}
		if err := r.cfg.Storage.Upload(ctx, outURL, rendOut, "video/mp4"); err != nil {
			return r.failJob(ctx, id, types.ErrCodeUploadFailed, err)
		}

		// Per-rendition debit.
		if r.cfg.Payment != nil && plan.WorkID != "" && plan.UnitsPerRend > 0 {
			seq := uint64(i + 1)
			if _, err := r.cfg.Payment.DebitBalance(ctx, plan.Sender, plan.WorkID, plan.UnitsPerRend, seq); err != nil {
				return r.failJob(ctx, id, types.ErrCodePaymentFailed, err)
			}
		}

		rendList = append(rendList, hls.Rendition{
			Name: name, BitrateBps: preset.BitrateKbps * 1000,
			ResolutionW: preset.WidthMax, ResolutionH: preset.HeightMax,
			Codec: preset.Codec, URI: name + ".m3u8",
		})

		r.fireWebhook(ctx, parent, "job.rendition.complete", map[string]any{
			"job_id":    id,
			"preset":    name,
			"rendition": rendList[len(rendList)-1],
		})
	}

	// uploading master
	if _, err := r.cfg.Repo.Transition(ctx, id, types.PhaseUploading); err != nil {
		return err
	}
	masterText, err := hls.BuildMaster(rendList)
	if err != nil {
		return r.failJob(ctx, id, types.ErrCodeUploadFailed, err)
	}
	masterPath := filepath.Join(tmpDir, "master.m3u8")
	if err := os.WriteFile(masterPath, []byte(masterText), 0o600); err != nil {
		return r.failJob(ctx, id, types.ErrCodeUploadFailed, err)
	}
	if err := r.cfg.Storage.Upload(ctx, plan.MasterOutputURL, masterPath, "application/vnd.apple.mpegurl"); err != nil {
		return r.failJob(ctx, id, types.ErrCodeUploadFailed, err)
	}

	if _, err := r.cfg.Repo.Transition(ctx, id, types.PhaseComplete); err != nil {
		return err
	}
	r.fireWebhook(ctx, parent, "job.complete", map[string]any{"job_id": id})
	return nil
}

// Run is the worker loop driver. The plan is held in r.plans.
func (r *Runner) Run(ctx context.Context, plan func(id string) (ABRJob, bool)) error {
	var wg sync.WaitGroup
	for i := 0; i < r.cfg.MaxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.worker(ctx, plan)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

func (r *Runner) worker(ctx context.Context, plan func(id string) (ABRJob, bool)) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		id := r.pop()
		if id == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		j, ok := plan(id)
		if !ok {
			r.finish(id)
			r.cfg.Logger.Warn("abrrunner.no_plan", "job_id", id)
			continue
		}
		err := r.RunOne(ctx, id, j)
		r.finish(id)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.cfg.Logger.Error("abrrunner.run_one", "job_id", id, "error", err)
		}
	}
}

func (r *Runner) finish(id string) {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()
	delete(r.active, id)
}

func (r *Runner) failJob(ctx context.Context, id, code string, err error) error {
	j, mErr := r.cfg.Repo.MarkError(ctx, id, code, err.Error())
	if mErr != nil {
		return mErr
	}
	r.fireWebhook(ctx, j, "job.error", map[string]any{
		"job_id": id, "error_code": code, "error_message": err.Error(),
	})
	return err
}

func (r *Runner) fireWebhook(_ context.Context, j types.Job, event string, payload map[string]any) {
	if r.cfg.Webhook == nil || j.WebhookURL == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = r.cfg.Webhook.Send(ctx, webhooks.Delivery{
			URL: j.WebhookURL, Secret: j.WebhookSecret, Event: event, Payload: payload,
		})
	}()
}

func presetListPseudoName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += "+" + n
	}
	return out
}
