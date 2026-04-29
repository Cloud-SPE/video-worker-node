// Package paymentbroker is the service-layer adapter over the
// payment-daemon gRPC client. Service runners (jobrunner, abrrunner,
// liverunner) call into this; the gRPC details live in
// internal/providers/paymentclient/.
//
// The Broker interface is what runners depend on. The default in-process
// implementation wraps a paymentclient.Client. Tests substitute Fake.
package paymentbroker

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Broker is the runner-facing payment surface.
type Broker interface {
	// ProcessPayment validates an incoming payment ticket and credits the
	// (sender, work_id) balance. Returns the credited amount in wei and
	// the new balance.
	ProcessPayment(ctx context.Context, paymentBytes []byte, workID string) (Receipt, error)
	// DebitBalance debits work_units from the (sender, work_id) balance.
	// debitSeq is monotonically increasing per-work_id; replaying the
	// same seq is a receiver-side no-op.
	DebitBalance(ctx context.Context, sender []byte, workID string, units int64, debitSeq uint64) (balanceWei []byte, err error)
	// SufficientBalance checks whether the (sender, work_id) balance is
	// at least minUnits worth.
	SufficientBalance(ctx context.Context, sender []byte, workID string, minUnits int64) (bool, error)
	// CloseSession releases any residual credit and garbage-collects.
	CloseSession(ctx context.Context, sender []byte, workID string) error
}

// Receipt describes the result of a ProcessPayment call.
type Receipt struct {
	Sender         []byte
	CreditedWei    []byte
	BalanceWei     []byte
	WinnersQueued  int32
}

// ErrInvalidPayment is returned when ProcessPayment rejects the ticket.
var ErrInvalidPayment = errors.New("payment: invalid ticket")

// ErrInsufficientBalance is returned when balance < requested.
var ErrInsufficientBalance = errors.New("payment: insufficient balance")

// Fake is an in-memory Broker for tests + dev mode.
type Fake struct {
	mu       sync.Mutex
	balances map[string]int64
	debits   []FakeDebit
	closed   map[string]bool

	// CreditPerProcess is the amount ProcessPayment credits per call.
	// Default 100; tests override to 0 when they're driving the balance
	// directly via CreditFor.
	CreditPerProcess int64

	// FailProcess and FailDebit cause the next call to return that error.
	FailProcess error
	FailDebit   error
}

// FakeDebit records a single DebitBalance call.
type FakeDebit struct {
	Sender   []byte
	WorkID   string
	Units    int64
	DebitSeq uint64
	At       time.Time
}

// NewFake returns an empty Fake. Default CreditPerProcess is 100.
func NewFake() *Fake {
	return &Fake{balances: map[string]int64{}, closed: map[string]bool{}, CreditPerProcess: 100}
}

// CreditFor sets the balance for a workID directly. Used in tests to
// pre-credit a session without round-tripping ProcessPayment.
func (f *Fake) CreditFor(workID string, units int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.balances[workID] += units
}

// ProcessPayment satisfies Broker. Always credits a fixed 100 units per call
// unless FailProcess is set.
func (f *Fake) ProcessPayment(_ context.Context, _ []byte, workID string) (Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailProcess != nil {
		return Receipt{}, f.FailProcess
	}
	credited := f.CreditPerProcess
	f.balances[workID] += credited
	return Receipt{
		Sender:      []byte("fake-sender"),
		CreditedWei: []byte{byte(credited)},
		BalanceWei:  []byte{byte(f.balances[workID])},
	}, nil
}

// DebitBalance satisfies Broker.
func (f *Fake) DebitBalance(_ context.Context, sender []byte, workID string, units int64, seq uint64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailDebit != nil {
		return nil, f.FailDebit
	}
	f.debits = append(f.debits, FakeDebit{Sender: sender, WorkID: workID, Units: units, DebitSeq: seq, At: time.Now()})
	f.balances[workID] -= units
	return []byte{byte(f.balances[workID])}, nil
}

// SufficientBalance satisfies Broker.
func (f *Fake) SufficientBalance(_ context.Context, _ []byte, workID string, minUnits int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[workID] >= minUnits, nil
}

// CloseSession satisfies Broker.
func (f *Fake) CloseSession(_ context.Context, _ []byte, workID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed[workID] = true
	return nil
}

// Debits returns a copy of debit history.
func (f *Fake) Debits() []FakeDebit {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeDebit, len(f.debits))
	copy(out, f.debits)
	return out
}

// Balance returns the current balance for workID.
func (f *Fake) Balance(workID string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.balances[workID]
}

// IsClosed reports whether CloseSession was called for workID.
func (f *Fake) IsClosed(workID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed[workID]
}
