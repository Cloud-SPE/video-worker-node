package paymentbroker

import (
	"context"
	"errors"
	"testing"
)

func TestFakeProcessPayment(t *testing.T) {
	t.Parallel()
	f := NewFake()
	r, err := f.ProcessPayment(context.Background(), []byte("ticket"), "w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.CreditedWei) == 0 {
		t.Errorf("missing credited")
	}
	if f.Balance("w1") != 100 {
		t.Errorf("balance=%d", f.Balance("w1"))
	}
}

func TestFakeFailProcess(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.FailProcess = errors.New("invalid")
	if _, err := f.ProcessPayment(context.Background(), nil, "w"); err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeDebit(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.CreditFor("w", 50)
	bal, err := f.DebitBalance(context.Background(), []byte("s"), "w", 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bal) == 0 {
		t.Error("expected balance")
	}
	debits := f.Debits()
	if len(debits) != 1 || debits[0].DebitSeq != 1 {
		t.Errorf("debits=%v", debits)
	}
	f.FailDebit = errors.New("transient")
	if _, err := f.DebitBalance(context.Background(), nil, "w", 1, 2); err == nil {
		t.Fatal("expected fail")
	}
}

func TestFakeSufficient(t *testing.T) {
	t.Parallel()
	f := NewFake()
	f.CreditFor("w", 30)
	ok, err := f.SufficientBalance(context.Background(), nil, "w", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected sufficient")
	}
	ok, _ = f.SufficientBalance(context.Background(), nil, "w", 31)
	if ok {
		t.Error("expected insufficient")
	}
}

func TestFakeClose(t *testing.T) {
	t.Parallel()
	f := NewFake()
	if err := f.CloseSession(context.Background(), nil, "w"); err != nil {
		t.Fatal(err)
	}
	if !f.IsClosed("w") {
		t.Error("expected closed")
	}
	// idempotent
	if err := f.CloseSession(context.Background(), nil, "w"); err != nil {
		t.Fatal(err)
	}
	if f.CloseCount("w") != 2 {
		t.Fatalf("CloseCount=%d want 2", f.CloseCount("w"))
	}
}

func TestSentinels(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrInvalidPayment, ErrInvalidPayment) {
		t.Fatal("Is broken")
	}
	if !errors.Is(ErrInsufficientBalance, ErrInsufficientBalance) {
		t.Fatal("Is broken")
	}
}
