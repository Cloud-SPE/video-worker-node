package paymentclient

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pmt "github.com/Cloud-SPE/video-worker-node/proto/clients/livepeer/payments/v1"
)

// fakeServer implements PayeeDaemonServer just enough for tests.
type fakeServer struct {
	pmt.UnimplementedPayeeDaemonServer
	processCalls int
	debitCalls   int
}

func (s *fakeServer) ListCapabilities(_ context.Context, _ *pmt.ListCapabilitiesRequest) (*pmt.ListCapabilitiesResponse, error) {
	return &pmt.ListCapabilitiesResponse{
		Capabilities: []*pmt.CapabilityEntry{
			{
				Capability: "video:transcode.vod",
				WorkUnit:   "video_frame_megapixel",
				Offerings: []*pmt.OfferingPrice{
					{
						Id: "h264-1080p",
						PriceInfo: &pmt.PriceInfo{
							PricePerUnit:  1250000,
							PixelsPerUnit: 1,
						},
					},
				},
			},
		},
	}, nil
}

func (s *fakeServer) ProcessPayment(_ context.Context, req *pmt.ProcessPaymentRequest) (*pmt.ProcessPaymentResponse, error) {
	s.processCalls++
	return &pmt.ProcessPaymentResponse{
		Sender: []byte("sender"), CreditedEv: []byte{1}, Balance: []byte{2},
		WinnersQueued: 1,
	}, nil
}

func (s *fakeServer) DebitBalance(_ context.Context, _ *pmt.DebitBalanceRequest) (*pmt.DebitBalanceResponse, error) {
	s.debitCalls++
	return &pmt.DebitBalanceResponse{Balance: []byte{42}}, nil
}

func (s *fakeServer) SufficientBalance(_ context.Context, _ *pmt.SufficientBalanceRequest) (*pmt.SufficientBalanceResponse, error) {
	return &pmt.SufficientBalanceResponse{Sufficient: true, Balance: []byte{42}}, nil
}

func (s *fakeServer) CloseSession(_ context.Context, _ *pmt.PayeeDaemonCloseSessionRequest) (*pmt.PayeeDaemonCloseSessionResponse, error) {
	return &pmt.PayeeDaemonCloseSessionResponse{}, nil
}

func startFake(t *testing.T) (string, *fakeServer, func()) {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "payment.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	fs := &fakeServer{}
	pmt.RegisterPayeeDaemonServer(srv, fs)
	go func() { _ = srv.Serve(lis) }()
	return sock, fs, func() { srv.Stop(); lis.Close() }
}

func TestClientHappyPath(t *testing.T) {
	t.Parallel()
	sock, fs, stop := startFake(t)
	defer stop()
	c, err := Open(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := c.ProcessPayment(context.Background(), []byte("ticket"), "w")
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Sender) != "sender" {
		t.Errorf("sender=%q", r.Sender)
	}
	if fs.processCalls != 1 {
		t.Errorf("calls=%d", fs.processCalls)
	}
	bal, err := c.DebitBalance(context.Background(), []byte("s"), "w", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bal) == 0 {
		t.Error("empty balance")
	}
	ok, err := c.SufficientBalance(context.Background(), []byte("s"), "w", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected sufficient")
	}
	if err := c.CloseSession(context.Background(), []byte("s"), "w"); err != nil {
		t.Fatal(err)
	}
	catalog, err := c.ListCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Capabilities) != 1 {
		t.Fatalf("capabilities=%d", len(catalog.Capabilities))
	}
	if catalog.Capabilities[0].Offerings[0].PricePerWorkUnitWei != "1250000" {
		t.Fatalf("price=%q", catalog.Capabilities[0].Offerings[0].PricePerWorkUnitWei)
	}
}

func TestOpenEmptySocket(t *testing.T) {
	t.Parallel()
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestCloseNil(t *testing.T) {
	t.Parallel()
	var c *Client
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestUnreachableSocket(t *testing.T) {
	t.Parallel()
	c, err := Open(context.Background(), "/no/such/socket")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.ProcessPayment(ctx, nil, "w")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestInsecureCreds(t *testing.T) {
	// Sanity: credentials.insecure is ok for unix-socket.
	t.Parallel()
	_ = insecure.NewCredentials()
}
