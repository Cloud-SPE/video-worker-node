package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/config"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/ffmpeg"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/gpu"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/logger"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/paymentclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/probe"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/shellclient"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/storage"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/store"
	"github.com/Cloud-SPE/video-worker-node/internal/providers/webhooks"
	"github.com/Cloud-SPE/video-worker-node/internal/repo/jobs"
	"github.com/Cloud-SPE/video-worker-node/internal/runtime/grpc"
	httpsurface "github.com/Cloud-SPE/video-worker-node/internal/runtime/http"
	"github.com/Cloud-SPE/video-worker-node/internal/runtime/lifecycle"
	"github.com/Cloud-SPE/video-worker-node/internal/runtime/metrics"
	"github.com/Cloud-SPE/video-worker-node/internal/service/abrrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/jobrunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/livecdn"
	"github.com/Cloud-SPE/video-worker-node/internal/service/liverunner"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	"github.com/Cloud-SPE/video-worker-node/internal/service/preflight"
	"github.com/Cloud-SPE/video-worker-node/internal/service/presetloader"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func run(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("livepeer-video-worker-node", flag.ContinueOnError)
	fs.SetOutput(stderr)

	mode := fs.String("mode", "vod", "vod | abr | live")
	dev := fs.Bool("dev", false, "use FakeFFmpeg + fake clients (no real chain / GPU detection)")
	logLevel := fs.String("log-level", "info", "log level: error|warn|info|debug")
	logFormat := fs.String("log-format", "text", "log format: text|json")

	gpuVendor := fs.String("gpu-vendor", "auto", "auto|nvidia|intel|amd")
	ffmpegBin := fs.String("ffmpeg-bin", "/usr/local/bin/ffmpeg", "path to ffmpeg binary")
	maxQueueSize := fs.Int("max-queue-size", 5, "max concurrent VOD/ABR jobs")
	tempDir := fs.String("temp-dir", "/tmp/livepeer-transcode", "scratch directory for ffmpeg")

	httpListen := fs.String("http-listen", ":8080", "public HTTP API listen addr; empty disables")
	grpcSocket := fs.String("grpc-socket", "", "operator gRPC unix socket path; empty = disabled")
	metricsListen := fs.String("metrics-listen", "", "Prometheus listener addr; empty = disabled")
	metricsMaxSeries := fs.Int("metrics-max-series", 10_000, "cap on distinct label tuples per metric")

	storePath := fs.String("store-path", "", "BoltDB file path; empty = in-memory (dev only)")
	presetsFile := fs.String("presets-file", "presets/h264-streaming.yaml", "YAML preset catalogue")

	paymentSocket := fs.String("payment-socket", "", "payment-daemon unix socket path; empty disables payment")

	// v3.0.0: --registry-socket / --registry-refresh / --public-url /
	// --node-id / --region / --operator-address / --price-wei-per-unit
	// flags removed. Workers are registry-invisible under archetype A;
	// the orch-coordinator carries node identity + region + pricing in
	// its operator-curated roster, not the worker.

	debitCadence := fs.Duration("debit-cadence", 5*time.Second, "Live mode periodic DebitBalance interval")
	streamPreCredit := fs.Int("stream-pre-credit-seconds", 1, "Live mode minimum pre-credit at session open")
	streamRunway := fs.Int("stream-runway-seconds", 30, "Live mode SufficientBalance watermark runway")
	streamGrace := fs.Int("stream-grace-seconds", 60, "Live mode grace period before fatal close")
	streamRestartLimit := fs.Int("stream-restart-limit", 3, "Live mode FFmpeg restart limit")
	streamTopupMinInterval := fs.Duration("stream-topup-min-interval", 5*time.Second, "rate limit between /stream/topup calls")

	shellInternalURL := fs.String("shell-internal-url", "", "Live mode: shell base URL for /internal/live/* callbacks (e.g., http://api:8080)")
	shellInternalSecret := fs.String("shell-internal-secret", "", "Live mode: shared secret for /internal/live/* callbacks (X-Worker-Secret)")
	ingestRTMPListen := fs.String("ingest-rtmp-listen", ":1935", "Live mode: RTMP ingest listen addr")
	ingestRTMPMaxConcurrent := fs.Int("ingest-rtmp-max-concurrent", 4, "Live mode: max concurrent RTMP sessions; 0 = unlimited")
	livePreset := fs.String("live-preset", "h264-live", "Live mode: encode preset name")
	liveStoragePrefix := fs.String("live-storage-prefix", "live/{stream_id}", "Live mode: storage prefix template; {stream_id} substituted")
	liveSinkRoot := fs.String("live-sink-root", "/var/live", "Live mode: local-FS sink root (shared with playback origin); empty = no local sink")

	authToken := fs.String("auth-token", "", "optional bearer token for the public HTTP API")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	log, err := logger.Build(*logLevel, *logFormat, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "logger: %v\n", err)
		return 2
	}

	cfg := config.Default()
	cfg.Mode = types.Mode(*mode)
	cfg.Dev = *dev
	cfg.Version = version
	cfg.GPUVendor = types.GPUVendor(*gpuVendor)
	cfg.FFmpegBin = *ffmpegBin
	cfg.MaxQueueSize = *maxQueueSize
	cfg.TempDir = *tempDir
	cfg.PresetsFile = *presetsFile
	cfg.HTTPListen = *httpListen
	cfg.GRPCSocket = *grpcSocket
	cfg.MetricsListen = *metricsListen
	cfg.MetricsMaxSeries = *metricsMaxSeries
	cfg.StorePath = *storePath
	cfg.PaymentSocket = *paymentSocket
	cfg.DebitCadence = *debitCadence
	cfg.StreamPreCreditSeconds = *streamPreCredit
	cfg.StreamRunwaySeconds = *streamRunway
	cfg.StreamGraceSeconds = *streamGrace
	cfg.StreamRestartLimit = *streamRestartLimit
	cfg.StreamTopupMinInterval = *streamTopupMinInterval
	cfg.ShellInternalURL = *shellInternalURL
	cfg.ShellInternalSecret = *shellInternalSecret
	cfg.IngestRTMPListen = *ingestRTMPListen
	cfg.IngestRTMPMaxConcurrent = *ingestRTMPMaxConcurrent
	cfg.LivePreset = *livePreset
	cfg.LiveStoragePrefix = *liveStoragePrefix
	cfg.AuthToken = *authToken

	if cfg.Dev {
		log.Warn("DEV MODE — using FakeFFmpeg, FakeGPU, fake payment broker; no real subprocesses or chain calls")
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "invalid config: %v\n", err)
		return 2
	}

	// Preflight: GPU + FFmpeg
	var detector gpu.Detector
	if cfg.Dev {
		detector = gpu.FakeNVIDIA()
	} else {
		detector = gpu.NewSystemDetector()
	}
	pre, err := preflight.Run(ctx, preflight.Config{
		Mode: cfg.Mode, GPUVendor: cfg.GPUVendor, Detector: detector,
		FFmpegBin: cfg.FFmpegBin, Dev: cfg.Dev, Logger: log,
	})
	if err != nil {
		fmt.Fprintf(stderr, "preflight failed: %v\n", err)
		return 1
	}

	// Store
	var st store.Store
	if cfg.StorePath != "" {
		st, err = store.OpenBolt(cfg.StorePath)
		if err != nil {
			fmt.Fprintf(stderr, "store: %v\n", err)
			return 1
		}
		defer st.Close()
	} else {
		st = store.Memory()
	}
	repo := jobs.New(st)

	// Presets
	pl, err := presetloader.New(cfg.PresetsFile)
	if err != nil {
		fmt.Fprintf(stderr, "presets: %v\n", err)
		return 1
	}

	// FFmpeg runner
	var ffRunner ffmpeg.Runner
	if cfg.Dev {
		ffRunner = &ffmpeg.FakeRunner{Steps: 5, PerStep: 50 * time.Millisecond}
	} else {
		ffRunner = &ffmpeg.SystemRunner{Bin: cfg.FFmpegBin, CancelGrace: cfg.CancelGrace}
	}

	// Storage
	stg := storage.New()

	// Webhook
	wh := webhooks.NewHTTP()
	wh.Backoffs = cfg.WebhookRetryBackoffs

	// Payment
	var paymentBroker paymentbroker.Broker
	var pmtClient *paymentclient.Client
	if cfg.PaymentSocket != "" && !cfg.Dev {
		pmtClient, err = paymentclient.Open(ctx, cfg.PaymentSocket)
		if err != nil {
			fmt.Fprintf(stderr, "payment client: %v\n", err)
			return 1
		}
		paymentBroker = pmtClient
		defer pmtClient.Close()
	} else {
		paymentBroker = paymentbroker.NewFake()
	}

	// v3.0.0 (suite plan 0003 §Decision 1): worker self-publishing is
	// dead. Workers are registry-invisible. The orch-coordinator scrapes
	// /registry/offerings (not implemented in this repo yet — see
	// follow-up local plan §Decision 5) and the secure-orch console
	// signs the manifest. capabilityreporter + registryclient packages
	// removed; --registry-socket / --registry-refresh flags removed.

	// Build runners by mode.
	abrPlans := newPlanRegistry()
	var jobR *jobrunner.Runner
	var abrR *abrrunner.Runner
	var liveR *liverunner.Runner

	switch {
	case cfg.Mode.IsVOD():
		jobR, err = jobrunner.New(jobrunner.Config{
			Repo: repo, FFmpeg: ffRunner, Probe: probe.NewSystem(), Storage: stg,
			Webhook: wh, Payment: paymentBroker, Presets: pl, GPU: pre.GPU,
			TempDir: cfg.TempDir, MaxQueue: cfg.MaxQueueSize, Logger: log,
		})
	case cfg.Mode.IsABR():
		abrR, err = abrrunner.New(abrrunner.Config{
			Repo: repo, FFmpeg: ffRunner, Probe: probe.NewSystem(), Storage: stg,
			Webhook: wh, Payment: paymentBroker, Presets: pl, GPU: pre.GPU,
			TempDir: cfg.TempDir, Logger: log,
		})
	case cfg.Mode.IsLive():
		// FFmpeg subprocess wiring lands in §D; until then the runner
		// uses the drain encoder so the state machine + payment loop
		// exercise even without a real GPU encode.
		_ = ffRunner

		var shellCl shellclient.Client
		if cfg.ShellInternalURL != "" && cfg.ShellInternalSecret != "" {
			shellCl, err = shellclient.New(shellclient.Config{
				BaseURL: cfg.ShellInternalURL, Secret: cfg.ShellInternalSecret,
			})
			if err != nil {
				fmt.Fprintf(stderr, "shell client: %v\n", err)
				return 1
			}
		} else if !cfg.Dev {
			fmt.Fprintf(stderr, "shell-internal-url + shell-internal-secret are required in live mode (non-dev)\n")
			return 2
		} else {
			shellCl = shellclient.NewFake()
		}

		// Build the ladder from the loaded preset catalogue.
		ladder := pl.FilterByGPU(pre.GPU)

		liveLocalDirRoot := filepath.Join(cfg.TempDir, "live")
		var encFactory liverunner.EncoderFactory
		if cfg.Dev {
			// Dev mode: drain the input + tick wall-clock seconds so the
			// state machine + payment loop exercise without FFmpeg.
			encFactory = liverunner.NewDrainEncoder
		} else {
			encFactory = liverunner.NewFFmpegEncoderFactory(liverunner.FFmpegEncoderConfig{
				LocalDirRoot: liveLocalDirRoot,
				Ladder:       ladder,
				GPU:          pre.GPU,
				FFmpegBin:    cfg.FFmpegBin,
			})
		}

		// Live segment sink. Default: a local FS path the playback-
		// origin nginx serves over HTTP. Production swaps in S3 (tracked
		// as tech-debt). In dev mode we use a per-run temp dir so the
		// daemon can boot anywhere.
		var liveSink livecdn.Sink
		sinkRoot := *liveSinkRoot
		if cfg.Dev {
			sinkRoot = filepath.Join(cfg.TempDir, "live-sink")
		}
		if sinkRoot != "" {
			s, serr := livecdn.NewLocalFSSink(sinkRoot)
			if serr != nil {
				fmt.Fprintf(stderr, "live sink: %v\n", serr)
				return 1
			}
			liveSink = s
		}

		liveR, err = liverunner.New(liverunner.Config{
			Repo: repo, Webhook: wh, Payment: paymentBroker, Presets: pl,
			GPU: pre.GPU, Logger: log,
			Shell: shellCl,
			WorkerURL: cfg.PublicURL,
			LivePreset: cfg.LivePreset,
			StoragePrefix: cfg.LiveStoragePrefix,
			MaxConcurrentStreams: cfg.IngestRTMPMaxConcurrent,
			EncoderFactory: encFactory,
			LocalDirRoot: liveLocalDirRoot,
			Sink: liveSink,
			Ladder: ladder,
			DebitCadence: cfg.DebitCadence, RunwaySeconds: cfg.StreamRunwaySeconds,
			GraceSeconds: cfg.StreamGraceSeconds, PreCreditSeconds: cfg.StreamPreCreditSeconds,
			DebitRetryBackoff: cfg.StreamDebitRetryBackoff, RestartLimit: cfg.StreamRestartLimit,
			TopupMinInterval: cfg.StreamTopupMinInterval,
		})
	}
	if err != nil {
		fmt.Fprintf(stderr, "runner: %v\n", err)
		return 1
	}

	// HTTP entry
	httpSrv, err := httpsurface.New(httpsurface.Config{
		Mode: cfg.Mode, Dev: cfg.Dev, Repo: repo,
		JobRunner: jobR, ABRRunner: abrR, LiveRunner: liveR,
		Payment: paymentBroker, Presets: pl,
		Prober: probe.NewSystem(),
		AuthToken: cfg.AuthToken, Logger: log,
	})
	if err != nil {
		fmt.Fprintf(stderr, "http: %v\n", err)
		return 1
	}

	// gRPC operator surface
	grpcSrv, err := grpc.New(grpc.Config{
		Mode: cfg.Mode, Version: version, Dev: cfg.Dev, GPU: pre.GPU,
		Repo: repo, JobRunner: jobR, LiveRunner: liveR, Presets: pl,
		StartedAt: time.Now(), Logger: log,
	})
	if err != nil {
		fmt.Fprintf(stderr, "grpc: %v\n", err)
		return 1
	}

	// Metrics listener (off by default).
	metricsSrv, err := metrics.New(cfg.MetricsListen, cfg.MetricsMaxSeries)
	if err != nil {
		fmt.Fprintf(stderr, "metrics: %v\n", err)
		return 1
	}

	// HTTP listener helper: blocks until ctx cancelled.
	httpListenFn := func(ctx context.Context) error {
		if cfg.HTTPListen == "" {
			<-ctx.Done()
			return ctx.Err()
		}
		lis, err := net.Listen("tcp", cfg.HTTPListen)
		if err != nil {
			return fmt.Errorf("http listen: %w", err)
		}
		srv := &http.Server{Handler: httpSrv.Handler(), ReadHeaderTimeout: 10 * time.Second}
		errCh := make(chan error, 1)
		go func() { errCh <- srv.Serve(lis) }()
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
			return ctx.Err()
		case err := <-errCh:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		}
	}

	var grpcListenFn func() error
	var grpcStopFn func()
	if cfg.GRPCSocket != "" {
		// Make sure parent dir exists.
		_ = ensureDir(filepath.Dir(cfg.GRPCSocket))
		grpcListenFn = func() error { return grpcSrv.Listen(cfg.GRPCSocket) }
		grpcStopFn = grpcSrv.Stop
	}
	var metricsListenFn func(ctx context.Context) error
	if metricsSrv != nil {
		metricsListenFn = metricsSrv.Listen
	}

	if err := lifecycle.Run(ctx, lifecycle.Config{
		Mode: cfg.Mode, JobRunner: jobR, ABRRunner: abrR, LiveRunner: liveR,
		HTTPListen: httpListenFn,
		GRPCListen: grpcListenFn, GRPCStop: grpcStopFn,
		MetricsListen: metricsListenFn, Logger: log,
		ABRPlanFn: abrPlans.Get,
	}); err != nil {
		fmt.Fprintf(stderr, "lifecycle: %v\n", err)
		return 1
	}
	return 0
}

func ensureDir(p string) error {
	if p == "" || p == "." {
		return nil
	}
	return os.MkdirAll(p, 0o755)
}

// abrPlans is a tiny in-memory map for ABR plan lookup. Real
// implementations would persist plans alongside the parent job; deferred.
type planRegistry struct {
	mu sync.Mutex
	m  map[string]abrrunner.ABRJob
}

func newPlanRegistry() *planRegistry { return &planRegistry{m: map[string]abrrunner.ABRJob{}} }

func (p *planRegistry) Get(id string) (abrrunner.ABRJob, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	j, ok := p.m[id]
	return j, ok
}
