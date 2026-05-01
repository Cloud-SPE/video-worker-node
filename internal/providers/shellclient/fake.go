package shellclient

import (
	"context"
	"sync"
)

// Fake is an in-memory Client for tests.
//
// Each method has a programmable response (default: zero values). The
// Calls slice records every invocation for assertions.
type Fake struct {
	mu sync.Mutex

	ValidateFunc           func(ctx context.Context, in ValidateKeyInput) (ValidateKeyResult, error)
	SessionActiveFunc      func(ctx context.Context, in SessionActiveInput) (SessionActiveResult, error)
	SessionTickFunc        func(ctx context.Context, in SessionTickInput) (SessionTickResult, error)
	SessionEndedFunc       func(ctx context.Context, in SessionEndedInput) (SessionEndedResult, error)
	RecordingFinalizedFunc func(ctx context.Context, in RecordingFinalizedInput) (RecordingFinalizedResult, error)
	TopupFunc              func(ctx context.Context, in TopupInput) (TopupResult, error)

	ValidateCalls           []ValidateKeyInput
	SessionActiveCalls      []SessionActiveInput
	SessionTickCalls        []SessionTickInput
	SessionEndedCalls       []SessionEndedInput
	RecordingFinalizedCalls []RecordingFinalizedInput
	TopupCalls              []TopupInput
}

// NewFake returns an empty Fake. Override any *Func to script behavior.
func NewFake() *Fake { return &Fake{} }

func (f *Fake) ValidateKey(ctx context.Context, in ValidateKeyInput) (ValidateKeyResult, error) {
	f.mu.Lock()
	f.ValidateCalls = append(f.ValidateCalls, in)
	fn := f.ValidateFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return ValidateKeyResult{Accepted: true, StreamID: "live_fake", ProjectID: "proj_fake", RecordingEnabled: true}, nil
}

func (f *Fake) SessionActive(ctx context.Context, in SessionActiveInput) (SessionActiveResult, error) {
	f.mu.Lock()
	f.SessionActiveCalls = append(f.SessionActiveCalls, in)
	fn := f.SessionActiveFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return SessionActiveResult{ReservationID: "rsv_fake"}, nil
}

func (f *Fake) SessionTick(ctx context.Context, in SessionTickInput) (SessionTickResult, error) {
	f.mu.Lock()
	f.SessionTickCalls = append(f.SessionTickCalls, in)
	fn := f.SessionTickFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return SessionTickResult{BalanceCents: 10_000, RunwaySeconds: 200_000, GraceTriggered: false}, nil
}

func (f *Fake) SessionEnded(ctx context.Context, in SessionEndedInput) (SessionEndedResult, error) {
	f.mu.Lock()
	f.SessionEndedCalls = append(f.SessionEndedCalls, in)
	fn := f.SessionEndedFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return SessionEndedResult{RecordingProcessing: true}, nil
}

func (f *Fake) RecordingFinalized(ctx context.Context, in RecordingFinalizedInput) (RecordingFinalizedResult, error) {
	f.mu.Lock()
	f.RecordingFinalizedCalls = append(f.RecordingFinalizedCalls, in)
	fn := f.RecordingFinalizedFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return RecordingFinalizedResult{RecordingAssetID: "asset_fake"}, nil
}

func (f *Fake) Topup(ctx context.Context, in TopupInput) (TopupResult, error) {
	f.mu.Lock()
	f.TopupCalls = append(f.TopupCalls, in)
	fn := f.TopupFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, in)
	}
	return TopupResult{Succeeded: true, AuthorizedCents: 100, BalanceCents: 10_000}, nil
}

// Snapshots return defensive copies of the call slices.
func (f *Fake) Snapshot() FakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := func(in []ValidateKeyInput) []ValidateKeyInput {
		o := make([]ValidateKeyInput, len(in))
		copy(o, in)
		return o
	}
	cp2 := func(in []SessionActiveInput) []SessionActiveInput {
		o := make([]SessionActiveInput, len(in))
		copy(o, in)
		return o
	}
	cp3 := func(in []SessionTickInput) []SessionTickInput {
		o := make([]SessionTickInput, len(in))
		copy(o, in)
		return o
	}
	cp4 := func(in []SessionEndedInput) []SessionEndedInput {
		o := make([]SessionEndedInput, len(in))
		copy(o, in)
		return o
	}
	cp5 := func(in []RecordingFinalizedInput) []RecordingFinalizedInput {
		o := make([]RecordingFinalizedInput, len(in))
		copy(o, in)
		return o
	}
	cp6 := func(in []TopupInput) []TopupInput { o := make([]TopupInput, len(in)); copy(o, in); return o }
	return FakeSnapshot{
		Validate:           cp(f.ValidateCalls),
		SessionActive:      cp2(f.SessionActiveCalls),
		SessionTick:        cp3(f.SessionTickCalls),
		SessionEnded:       cp4(f.SessionEndedCalls),
		RecordingFinalized: cp5(f.RecordingFinalizedCalls),
		Topup:              cp6(f.TopupCalls),
	}
}

type FakeSnapshot struct {
	Validate           []ValidateKeyInput
	SessionActive      []SessionActiveInput
	SessionTick        []SessionTickInput
	SessionEnded       []SessionEndedInput
	RecordingFinalized []RecordingFinalizedInput
	Topup              []TopupInput
}
