package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestLiveFailsFastWhenFull(t *testing.T) {
	t.Parallel()
	c := New(Config{TotalSlots: 2, LiveReservedSlots: 1})
	l1, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch})
	if err != nil {
		t.Fatalf("batch acquire: %v", err)
	}
	defer l1.Release()
	l2, err := c.Acquire(context.Background(), Request{Workload: WorkloadLive})
	if err != nil {
		t.Fatalf("live acquire: %v", err)
	}
	defer l2.Release()
	if _, err := c.Acquire(context.Background(), Request{Workload: WorkloadLive}); err != ErrCapacityExceeded {
		t.Fatalf("err=%v want %v", err, ErrCapacityExceeded)
	}
}

func TestBatchLeavesLiveReserve(t *testing.T) {
	t.Parallel()
	c := New(Config{TotalSlots: 2, LiveReservedSlots: 1})
	l, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch})
	if err != nil {
		t.Fatalf("batch acquire: %v", err)
	}
	defer l.Release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Acquire(ctx, Request{Workload: WorkloadBatch}); err != context.DeadlineExceeded {
		t.Fatalf("err=%v want %v", err, context.DeadlineExceeded)
	}
}

func TestBatchUnblocksAfterRelease(t *testing.T) {
	t.Parallel()
	c := New(Config{TotalSlots: 2, LiveReservedSlots: 0})
	l1, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch})
	if err != nil {
		t.Fatalf("batch acquire: %v", err)
	}
	l2, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch})
	if err != nil {
		t.Fatalf("batch acquire: %v", err)
	}
	defer l2.Release()

	done := make(chan error, 1)
	go func() {
		lease, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch})
		if err == nil {
			lease.Release()
		}
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	l1.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for batch acquire")
	}
}

func TestCostBudgetBlocksHeavyBatchWork(t *testing.T) {
	t.Parallel()
	c := New(Config{TotalSlots: 2, LiveReservedSlots: 0, TotalCost: 120})
	l, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch, Slots: 1, Cost: 80})
	if err != nil {
		t.Fatalf("batch acquire: %v", err)
	}
	defer l.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Acquire(ctx, Request{Workload: WorkloadBatch, Slots: 1, Cost: 50}); err != context.DeadlineExceeded {
		t.Fatalf("err=%v want %v", err, context.DeadlineExceeded)
	}
}

func TestLiveFailsFastWhenCostBudgetFull(t *testing.T) {
	t.Parallel()
	c := New(Config{TotalSlots: 2, LiveReservedSlots: 0, TotalCost: 120})
	l, err := c.Acquire(context.Background(), Request{Workload: WorkloadBatch, Slots: 1, Cost: 80})
	if err != nil {
		t.Fatalf("batch acquire: %v", err)
	}
	defer l.Release()
	if _, err := c.Acquire(context.Background(), Request{Workload: WorkloadLive, Slots: 1, Cost: 50}); err != ErrCapacityExceeded {
		t.Fatalf("err=%v want %v", err, ErrCapacityExceeded)
	}
}
