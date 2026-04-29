package rtmp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ingest"
)

func TestProtocolReturnsRTMP(t *testing.T) {
	p := New(Config{})
	if p.Protocol() != ingest.ProtocolRTMP {
		t.Fatalf("expected rtmp protocol, got %v", p.Protocol())
	}
}

func TestNewDefaultsListenAddr(t *testing.T) {
	p := New(Config{})
	if p.cfg.Listen != ":1935" {
		t.Fatalf("expected default :1935, got %q", p.cfg.Listen)
	}
}

func TestNewPreservesExplicitListenAddr(t *testing.T) {
	p := New(Config{Listen: ":1936"})
	if p.cfg.Listen != ":1936" {
		t.Fatalf("expected :1936, got %q", p.cfg.Listen)
	}
}

func TestStopWithoutListenIsAnError(t *testing.T) {
	p := New(Config{})
	err := p.Stop(context.Background())
	if !errors.Is(err, ingest.ErrNotListening) {
		t.Fatalf("expected ErrNotListening, got %v", err)
	}
}

// fakeAcceptor lets us drive the SessionAcceptor surface without a real
// RTMP client.
type fakeAcceptor struct {
	called   chan struct{}
	accepted ingest.IngestSession
	rejected error
}

func (f *fakeAcceptor) Accept(_ context.Context, sess ingest.IngestSession) (ingest.Acceptance, error) {
	f.accepted = sess
	close(f.called)
	if f.rejected != nil {
		return ingest.Acceptance{}, f.rejected
	}
	return ingest.Acceptance{OnEnd: func(_ string) {}}, nil
}

func TestListenAndStopSequence(t *testing.T) {
	// Bind a random port so concurrent tests don't fight.
	p := New(Config{Listen: "127.0.0.1:0"})
	acc := &fakeAcceptor{called: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Listen(ctx, acc) }()

	// Give the listener a moment to bind.
	time.Sleep(20 * time.Millisecond)

	// Stop should close the listener; Listen returns nil (not an error,
	// since we drove the stop ourselves).
	if err := p.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	// go-rtmp's Serve can take a moment to unblock after the listener
	// closes; 5s gives generous slack without slowing the suite materially.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("listen returned %v after stop (want nil)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listen did not return within 5s after stop")
	}
}

func TestListenTwiceFails(t *testing.T) {
	p := New(Config{Listen: "127.0.0.1:0"})
	acc := &fakeAcceptor{called: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.Listen(ctx, acc) }()
	time.Sleep(20 * time.Millisecond)

	err := p.Listen(ctx, acc)
	if !errors.Is(err, ingest.ErrAlreadyListening) {
		t.Fatalf("expected ErrAlreadyListening, got %v", err)
	}
	_ = p.Stop(context.Background())
}
