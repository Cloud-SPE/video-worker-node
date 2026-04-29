// Package rtmp implements the IngestProvider interface over RTMP via
// github.com/yutopp/go-rtmp. Pure-Go (no cgo, preserves the no-cgo
// invariant). The publish-stream key is extracted from the URL path:
// rtmp://host:1935/live/{stream_key}.
//
// At v1 (skeleton): listens, accepts connections, hands an io.Reader-backed
// session to the registered SessionAcceptor. Full FFmpeg pipe + payment
// integration lands in plan 0006.
package rtmp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/Cloud-SPE/video-worker-node/internal/providers/ingest"
)

// Config wires the provider.
type Config struct {
	// Listen is the TCP listen address (e.g., ":1935").
	Listen string
	// MaxConcurrent caps in-flight sessions; 0 means unlimited.
	MaxConcurrent int
	// Logger is the structured logger.
	Logger *slog.Logger
}

// Provider implements ingest.IngestProvider.
type Provider struct {
	cfg      Config
	listener net.Listener
	server   *rtmp.Server
	mu       sync.Mutex
	stopped  atomic.Bool
}

// New constructs a Provider. The listener is not bound until Listen is called.
func New(cfg Config) *Provider {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Listen == "" {
		cfg.Listen = ":1935"
	}
	return &Provider{cfg: cfg}
}

// Protocol returns ProtocolRTMP.
func (*Provider) Protocol() ingest.Protocol { return ingest.ProtocolRTMP }

// Listen binds and accepts. Returns when ctx is cancelled, when Stop is
// called externally, or when a fatal listen error occurs.
//
// Internally Serve is run on a goroutine so this method's lifecycle is
// driven by ctx (not by the underlying listener's blocking Accept).
func (p *Provider) Listen(ctx context.Context, acceptor ingest.SessionAcceptor) error {
	p.mu.Lock()
	if p.listener != nil {
		p.mu.Unlock()
		return ingest.ErrAlreadyListening
	}
	ln, err := net.Listen("tcp", p.cfg.Listen)
	if err != nil {
		p.mu.Unlock()
		return fmt.Errorf("rtmp listen: %w", err)
	}
	p.listener = ln
	p.mu.Unlock()

	p.cfg.Logger.Info("rtmp.listening", "addr", p.cfg.Listen)

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			h := &connHandler{
				provider: p,
				acceptor: acceptor,
				logger:   p.cfg.Logger,
				remote:   conn.RemoteAddr().String(),
			}
			return conn, &rtmp.ConnConfig{
				Handler: h,
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024 / 8,
				},
			}
		},
	})
	p.mu.Lock()
	p.server = srv
	p.mu.Unlock()

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !p.stopped.Load() {
			serveErr <- fmt.Errorf("rtmp serve: %w", err)
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		_ = p.Stop(context.Background())
		// Drain serveErr so the goroutine doesn't leak; ignore its value.
		<-serveErr
		return nil
	case err := <-serveErr:
		return err
	}
}

// Stop gracefully shuts down the server; the listener closes via
// rtmp.Server.Close(). In-flight sessions terminate when their underlying
// connections error out.
func (p *Provider) Stop(_ context.Context) error {
	p.stopped.Store(true)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listener == nil {
		return ingest.ErrNotListening
	}
	srv := p.server
	p.listener = nil
	p.server = nil
	if srv != nil {
		return srv.Close()
	}
	// Server isn't constructed yet; close the bare listener.
	return nil
}

// connHandler implements rtmp.Handler for one accepted connection.
//
// The lifecycle: OnPublish triggers the SessionAcceptor; if accepted, audio
// + video chunks pipe into a session reader the acceptor's caller
// (typically liverunner) consumes. OnClose terminates the session.
type connHandler struct {
	rtmp.DefaultHandler

	provider *Provider
	acceptor ingest.SessionAcceptor
	logger   *slog.Logger
	remote   string

	mu        sync.Mutex
	pipeW     *io.PipeWriter
	pipeR     *io.PipeReader
	session   *session
	onEnd     func(string)
}

func (h *connHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	streamKey := strings.TrimSpace(cmd.PublishingName)
	if streamKey == "" {
		return errors.New("rtmp: empty publishing name (stream key)")
	}

	pr, pw := io.Pipe()
	sess := &session{
		streamKey: streamKey,
		reader:    pr,
		writer:    pw,
		remote:    h.remote,
	}
	h.mu.Lock()
	h.pipeR = pr
	h.pipeW = pw
	h.session = sess
	h.mu.Unlock()

	ctx := context.Background()
	acc, err := h.acceptor.Accept(ctx, sess)
	if err != nil {
		h.logger.Warn("rtmp.acceptor_rejected", "stream_key_prefix", redactKey(streamKey), "remote", h.remote, "error", err)
		_ = pw.Close()
		_ = pr.Close()
		return err
	}
	h.onEnd = acc.OnEnd
	h.logger.Info("rtmp.session_open", "stream_key_prefix", redactKey(streamKey), "remote", h.remote)
	return nil
}

// redactKey returns a short safe-to-log prefix of a stream key so ops can
// correlate sessions across logs without leaking the full credential.
func redactKey(k string) string {
	if len(k) <= 6 {
		return "[short-key]"
	}
	return k[:6] + "..."
}

func (h *connHandler) OnAudio(_ uint32, payload io.Reader) error {
	return h.pipeData(payload)
}

func (h *connHandler) OnVideo(_ uint32, payload io.Reader) error {
	return h.pipeData(payload)
}

func (h *connHandler) pipeData(payload io.Reader) error {
	h.mu.Lock()
	w := h.pipeW
	h.mu.Unlock()
	if w == nil {
		return nil
	}
	if _, err := io.Copy(w, payload); err != nil {
		return err
	}
	return nil
}

func (h *connHandler) OnClose() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pipeW != nil {
		_ = h.pipeW.Close()
	}
	if h.pipeR != nil {
		_ = h.pipeR.Close()
	}
	if h.session != nil {
		h.logger.Info("rtmp.session_closed", "stream_key_prefix", redactKey(h.session.streamKey), "remote", h.remote)
	}
	if h.onEnd != nil {
		h.onEnd("close")
		h.onEnd = nil
	}
}

// session implements ingest.IngestSession.
type session struct {
	streamKey string
	reader    *io.PipeReader
	writer    *io.PipeWriter
	remote    string
}

func (*session) Protocol() ingest.Protocol { return ingest.ProtocolRTMP }
func (s *session) StreamKey() string       { return s.streamKey }
func (*session) MediaFormat() string       { return "flv" }
func (s *session) Reader() io.Reader       { return s.reader }
func (s *session) RemoteAddr() string      { return s.remote }
func (s *session) Close() error {
	_ = s.writer.Close()
	return s.reader.Close()
}
