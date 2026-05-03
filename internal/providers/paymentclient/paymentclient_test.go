package paymentclient

import (
	"context"
	"math/big"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	pmt "github.com/Cloud-SPE/video-worker-node/proto/clients/livepeer/payments/v1"
)

// fakeServer implements PayeeDaemonServer just enough for tests.
type fakeServer struct {
	pmt.UnimplementedPayeeDaemonServer
	processCalls int
	debitCalls   int
	openCalls    int
	nilTicket    bool
	lastOpen     *pmt.OpenSessionRequest
	lastDebit    *pmt.DebitBalanceRequest
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

func (s *fakeServer) OpenSession(_ context.Context, req *pmt.OpenSessionRequest) (*pmt.OpenSessionResponse, error) {
	s.openCalls++
	s.lastOpen = req
	return &pmt.OpenSessionResponse{Outcome: pmt.OpenSessionResponse_OUTCOME_OPENED}, nil
}

func (s *fakeServer) GetTicketParams(_ context.Context, req *pmt.GetTicketParamsRequest) (*pmt.GetTicketParamsResponse, error) {
	if s.nilTicket {
		return &pmt.GetTicketParamsResponse{}, nil
	}
	return &pmt.GetTicketParamsResponse{
		TicketParams: &pmt.TicketParams{
			Recipient:         append([]byte(nil), req.GetRecipient()...),
			FaceValue:         append([]byte(nil), req.GetFaceValue()...),
			WinProb:           []byte{0x01},
			RecipientRandHash: []byte{0x02},
			Seed:              []byte{0x03},
			ExpirationBlock:   []byte{0x04},
			ExpirationParams: &pmt.TicketExpirationParams{
				CreationRound:          7,
				CreationRoundBlockHash: []byte{0x05},
			},
		},
	}, nil
}

func (s *fakeServer) DebitBalance(_ context.Context, req *pmt.DebitBalanceRequest) (*pmt.DebitBalanceResponse, error) {
	s.debitCalls++
	s.lastDebit = req
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
	if err := c.OpenSession(context.Background(), paymentbroker.SessionBinding{
		WorkID:              "w",
		Capability:          "video:transcode.vod",
		Offering:            "h264-1080p",
		PricePerWorkUnitWei: "1250000",
		WorkUnit:            "video_frame_megapixel",
	}); err != nil {
		t.Fatal(err)
	}
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
	if fs.openCalls != 1 {
		t.Fatalf("open_calls=%d", fs.openCalls)
	}
	if got := fs.lastOpen.GetOffering(); got != "h264-1080p" {
		t.Fatalf("open offering=%q", got)
	}
	bal, err := c.DebitBalance(context.Background(), []byte("s"), "w", 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bal) == 0 {
		t.Error("empty balance")
	}
	if got := fs.lastDebit.GetDebitSeq(); got != 1 {
		t.Fatalf("debit_seq=%d", got)
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
	ticketParams, err := c.GetTicketParams(context.Background(), GetTicketParamsRequest{
		Sender:     []byte{0x10},
		Recipient:  []byte{0x20},
		FaceValue:  big.NewInt(123),
		Capability: "video:transcode.vod",
		Offering:   "h264-1080p",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := new(big.Int).SetBytes(ticketParams.FaceValueWei).String(); got != "123" {
		t.Fatalf("face_value=%s", got)
	}
	if ticketParams.ExpirationParams.CreationRound != 7 {
		t.Fatalf("creation_round=%d", ticketParams.ExpirationParams.CreationRound)
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

func TestGetTicketParamsNilTicketParams(t *testing.T) {
	t.Parallel()
	sock, fs, stop := startFake(t)
	fs.nilTicket = true
	defer stop()
	c, err := Open(context.Background(), sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.GetTicketParams(context.Background(), GetTicketParamsRequest{
		Recipient: []byte{0x20},
		FaceValue: big.NewInt(1),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
