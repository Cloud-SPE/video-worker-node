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
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pmt "github.com/Cloud-SPE/video-worker-node/proto/clients/livepeer/payments/v1"
	"github.com/Cloud-SPE/video-worker-node/internal/service/paymentbroker"
)

// Client wraps a PayeeDaemon gRPC connection.
type Client struct {
	conn *grpc.ClientConn
	rpc  pmt.PayeeDaemonClient
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

// DebitBalance satisfies paymentbroker.Broker. The debitSeq parameter is
// not part of the wire payload at v1 — receiver-side idempotency relies on
// the (sender, work_id, work_units) tuple plus the pattern doc's
// recommendation to retry the same call. Reserved for a future wire
// extension.
func (c *Client) DebitBalance(ctx context.Context, sender []byte, workID string, units int64, _ uint64) ([]byte, error) {
	resp, err := c.rpc.DebitBalance(ctx, &pmt.DebitBalanceRequest{
		Sender: sender, WorkId: workID, WorkUnits: units,
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
