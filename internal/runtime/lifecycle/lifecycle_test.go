package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/logger"
	"github.com/Cloud-SPE/video-worker-node/internal/types"
)

func TestRunInvalidMode(t *testing.T) {
	t.Parallel()
	if err := Run(context.Background(), Config{Mode: "wat"}); err == nil {
		t.Fatal("expected mode error")
	}
}

func TestRunHTTPOnly(t *testing.T) {
	t.Parallel()
	httpFired := false
	cfg := Config{
		Mode: types.ModeVOD, Logger: logger.Discard(),
		HTTPListen: func(ctx context.Context) error {
			httpFired = true
			<-ctx.Done()
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := Run(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if !httpFired {
		t.Error("HTTPListen should have run")
	}
}

func TestRunErrorPropagation(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Mode: types.ModeVOD, Logger: logger.Discard(),
		HTTPListen: func(_ context.Context) error {
			return errors.New("port in use")
		},
	}
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected error")
	}
}

func TestRunABRRequiresPlan(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := Run(ctx, Config{Mode: types.ModeABR, Logger: logger.Discard()}); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ABR with no runner should be no-op, got %v", err)
	}
}
