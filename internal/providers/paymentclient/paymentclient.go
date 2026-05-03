// Package paymentclient is the gRPC client wrapper around payment-daemon's
// PayeeDaemon service. The proto stubs are vendored locally under
// proto/clients/livepeer/payments/v1 (not imported from payment-daemon
// directly) per plan 0007's release-independence rule.
//
// This package implements paymentbroker.Broker so the runners depend only
// on the broker interface, never on the proto types directly.
package paymentclient

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
	pmt "github.com/Cloud-SPE/video-worker-node/proto/clients/livepeer/payments/v1"
)

// Client wraps a PayeeDaemon gRPC connection.
type Client struct {
	conn *grpc.ClientConn
	rpc  pmt.PayeeDaemonClient
}

// ListCapabilitiesResult is the worker-side projection of the daemon's
// capability catalog, used for startup drift detection.
type ListCapabilitiesResult struct {
	Capabilities []Capability
}

// Capability mirrors the daemon's CapabilityEntry minus proto details.
type Capability struct {
	Capability string
	WorkUnit   string
	Offerings  []OfferingPrice
}

// OfferingPrice mirrors the daemon's offering price rows.
type OfferingPrice struct {
	ID                  string
	PricePerWorkUnitWei string
}

// GetTicketParamsRequest is the worker-side projection of the daemon's
// exact ticket-params request.
type GetTicketParamsRequest struct {
	Sender     []byte
	Recipient  []byte
	FaceValue  *big.Int
	Capability string
	Offering   string
}

// OpenSessionRequest is the worker-side projection of the daemon's
// authoritative payee session-open contract.
type OpenSessionRequest struct {
	WorkID              string
	Capability          string
	Offering            string
	PricePerWorkUnitWei string
	WorkUnit            string
}

// TicketParams is the worker-side projection of the proto TicketParams.
type TicketParams struct {
	Recipient         []byte
	FaceValueWei      []byte
	WinProb           []byte
	RecipientRandHash []byte
	Seed              []byte
	ExpirationBlock   []byte
	ExpirationParams  TicketExpirationParams
}

// TicketExpirationParams projects payments.v1.TicketExpirationParams.
type TicketExpirationParams struct {
	CreationRound          int64
	CreationRoundBlockHash []byte
}

// Open dials a payment-daemon over the given unix socket path.
func Open(ctx context.Context, socketPath string) (*Client, error) {
	if socketPath == "" {
		return nil, errors.New("paymentclient: empty socket path")
	}
	target := socketPath
	dial := func(_ context.Context, _ string) (net.Conn, error) {
		return net.DialTimeout("unix", socketPath, 5*time.Second)
	}
	conn, err := grpc.NewClient("passthrough:"+target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dial),
	)
	if err != nil {
		return nil, fmt.Errorf("paymentclient: dial %s: %w", socketPath, err)
	}
	return &Client{conn: conn, rpc: pmt.NewPayeeDaemonClient(conn)}, nil
}

// Close releases the gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// ListCapabilities returns the daemon's configured capability catalog for
// startup drift detection against worker.yaml.
func (c *Client) ListCapabilities(ctx context.Context) (ListCapabilitiesResult, error) {
	resp, err := c.rpc.ListCapabilities(ctx, &pmt.ListCapabilitiesRequest{})
	if err != nil {
		return ListCapabilitiesResult{}, err
	}
	caps := make([]Capability, 0, len(resp.GetCapabilities()))
	for _, capability := range resp.GetCapabilities() {
		offerings := make([]OfferingPrice, 0, len(capability.GetOfferings()))
		for _, offering := range capability.GetOfferings() {
			offerings = append(offerings, OfferingPrice{
				ID:                  offering.GetId(),
				PricePerWorkUnitWei: priceInfoToWeiString(offering.GetPriceInfo()),
			})
		}
		caps = append(caps, Capability{
			Capability: capability.GetCapability(),
			WorkUnit:   capability.GetWorkUnit(),
			Offerings:  offerings,
		})
	}
	return ListCapabilitiesResult{Capabilities: caps}, nil
}

// GetTicketParams proxies the receiver daemon's exact ticket-params helper.
func (c *Client) GetTicketParams(ctx context.Context, req GetTicketParamsRequest) (TicketParams, error) {
	faceValue := []byte(nil)
	if req.FaceValue != nil {
		faceValue = req.FaceValue.Bytes()
	}
	resp, err := c.rpc.GetTicketParams(ctx, &pmt.GetTicketParamsRequest{
		Sender:     append([]byte(nil), req.Sender...),
		Recipient:  append([]byte(nil), req.Recipient...),
		FaceValue:  faceValue,
		Capability: req.Capability,
		Offering:   req.Offering,
	})
	if err != nil {
		return TicketParams{}, err
	}
	tp := resp.GetTicketParams()
	if tp == nil {
		return TicketParams{}, errors.New("paymentclient: daemon returned nil ticket_params")
	}
	return TicketParams{
		Recipient:         tp.GetRecipient(),
		FaceValueWei:      tp.GetFaceValue(),
		WinProb:           tp.GetWinProb(),
		RecipientRandHash: tp.GetRecipientRandHash(),
		Seed:              tp.GetSeed(),
		ExpirationBlock:   tp.GetExpirationBlock(),
		ExpirationParams: TicketExpirationParams{
			CreationRound:          tp.GetExpirationParams().GetCreationRound(),
			CreationRoundBlockHash: tp.GetExpirationParams().GetCreationRoundBlockHash(),
		},
	}, nil
}

// OpenSession satisfies paymentbroker.Broker.
func (c *Client) OpenSession(ctx context.Context, req paymentbroker.SessionBinding) error {
	if req.WorkID == "" {
		return errors.New("paymentclient: work_id is required")
	}
	priceWei := new(big.Int)
	if _, ok := priceWei.SetString(req.PricePerWorkUnitWei, 10); !ok {
		return fmt.Errorf("paymentclient: invalid price_per_work_unit_wei %q", req.PricePerWorkUnitWei)
	}
	_, err := c.rpc.OpenSession(ctx, &pmt.OpenSessionRequest{
		WorkId:              req.WorkID,
		Capability:          req.Capability,
		Offering:            req.Offering,
		PricePerWorkUnitWei: priceWei.Bytes(),
		WorkUnit:            req.WorkUnit,
	})
	return err
}

// ProcessPayment satisfies paymentbroker.Broker.
func (c *Client) ProcessPayment(ctx context.Context, paymentBytes []byte, workID string) (paymentbroker.Receipt, error) {
	resp, err := c.rpc.ProcessPayment(ctx, &pmt.ProcessPaymentRequest{
		PaymentBytes: paymentBytes,
		WorkId:       workID,
	})
	if err != nil {
		return paymentbroker.Receipt{}, err
	}
	return paymentbroker.Receipt{
		Sender:        resp.GetSender(),
		CreditedWei:   resp.GetCreditedEv(),
		BalanceWei:    resp.GetBalance(),
		WinnersQueued: resp.GetWinnersQueued(),
	}, nil
}

// DebitBalance satisfies paymentbroker.Broker.
func (c *Client) DebitBalance(ctx context.Context, sender []byte, workID string, units int64, debitSeq uint64) ([]byte, error) {
	resp, err := c.rpc.DebitBalance(ctx, &pmt.DebitBalanceRequest{
		Sender: sender, WorkId: workID, WorkUnits: units, DebitSeq: debitSeq,
	})
	if err != nil {
		return nil, err
	}
	return resp.GetBalance(), nil
}

// SufficientBalance satisfies paymentbroker.Broker.
func (c *Client) SufficientBalance(ctx context.Context, sender []byte, workID string, minUnits int64) (bool, error) {
	resp, err := c.rpc.SufficientBalance(ctx, &pmt.SufficientBalanceRequest{
		Sender: sender, WorkId: workID, MinWorkUnits: minUnits,
	})
	if err != nil {
		return false, err
	}
	return resp.GetSufficient(), nil
}

// CloseSession satisfies paymentbroker.Broker.
func (c *Client) CloseSession(ctx context.Context, sender []byte, workID string) error {
	_, err := c.rpc.CloseSession(ctx, &pmt.PayeeDaemonCloseSessionRequest{
		Sender: sender, WorkId: workID,
	})
	return err
}

func priceInfoToWeiString(p *pmt.PriceInfo) string {
	if p == nil {
		return "0"
	}
	num := big.NewInt(p.GetPricePerUnit())
	den := big.NewInt(p.GetPixelsPerUnit())
	if den.Sign() <= 0 {
		return num.String()
	}
	return num.Div(num, den).String()
}
