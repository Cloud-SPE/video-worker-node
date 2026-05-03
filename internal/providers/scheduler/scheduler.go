// Package scheduler owns host-local GPU admission for video workloads.
//
// First cut is deliberately simple: slot-based admission with
// preset-derived cost weighting and reserved headroom for live. Batch
// workloads wait for capacity; live workloads fail fast when no slot or
// cost budget is available. The API is shaped so richer cost models
// (VRAM / preset weight) can land without changing every caller.
package scheduler

import (
	"context"
	"errors"
	"sync"
)

// Workload distinguishes batch work from live work.
type Workload string

const (
	WorkloadBatch Workload = "batch"
	WorkloadLive  Workload = "live"
)

// ErrCapacityExceeded is returned when a fail-fast request cannot be
// admitted immediately.
var ErrCapacityExceeded = errors.New("scheduler: capacity exceeded")

// Request describes one admission attempt.
type Request struct {
	Workload Workload
	Slots    int
	Cost     int
}

// Snapshot reports current scheduler occupancy.
type Snapshot struct {
	TotalSlots        int
	LiveReservedSlots int
	TotalCost         int
	LiveReservedCost  int
	ActiveSlots       int
	ActiveBatchSlots  int
	ActiveLiveSlots   int
	ActiveCost        int
	ActiveBatchCost   int
	ActiveLiveCost    int
	QueuedBatch       int
}

// Lease represents one active admission.
type Lease interface {
	Release()
}

// Controller is the runner-facing scheduler surface.
type Controller interface {
	Acquire(ctx context.Context, req Request) (Lease, error)
	Snapshot() Snapshot
}

// Config wires a slot scheduler.
type Config struct {
	TotalSlots        int
	LiveReservedSlots int
	TotalCost         int
	LiveReservedCost  int
}

// New returns a Controller. TotalSlots <= 0 yields a no-op scheduler.
func New(cfg Config) Controller {
	if cfg.TotalSlots <= 0 {
		return noopController{}
	}
	if cfg.TotalCost <= 0 {
		cfg.TotalCost = cfg.TotalSlots * 100
	}
	if cfg.LiveReservedSlots < 0 {
		cfg.LiveReservedSlots = 0
	}
	if cfg.LiveReservedCost < 0 {
		cfg.LiveReservedCost = 0
	}
	// A reserve equal to total slots would make batch impossible forever.
	if cfg.LiveReservedSlots >= cfg.TotalSlots {
		cfg.LiveReservedSlots = cfg.TotalSlots - 1
	}
	maxLiveReservedCost := cfg.TotalCost - 100
	if maxLiveReservedCost < 0 {
		maxLiveReservedCost = 0
	}
	if cfg.LiveReservedCost == 0 {
		cfg.LiveReservedCost = cfg.LiveReservedSlots * 100
	}
	if cfg.LiveReservedCost > maxLiveReservedCost {
		cfg.LiveReservedCost = maxLiveReservedCost
	}
	return newSlotController(cfg)
}

type noopController struct{}

func (noopController) Acquire(_ context.Context, _ Request) (Lease, error) { return noopLease{}, nil }
func (noopController) Snapshot() Snapshot                                  { return Snapshot{} }

type noopLease struct{}

func (noopLease) Release() {}

type slotController struct {
	total            int
	liveReserved     int
	totalCost        int
	liveCostReserved int

	mu              sync.Mutex
	cond            *sync.Cond
	active          int
	activeBatch     int
	activeLive      int
	activeCost      int
	activeBatchCost int
	activeLiveCost  int
	queuedBatch     int
}

func newSlotController(cfg Config) *slotController {
	c := &slotController{
		total:            cfg.TotalSlots,
		liveReserved:     cfg.LiveReservedSlots,
		totalCost:        cfg.TotalCost,
		liveCostReserved: cfg.LiveReservedCost,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

func (c *slotController) Acquire(ctx context.Context, req Request) (Lease, error) {
	slots := req.Slots
	if slots <= 0 {
		slots = 1
	}
	cost := req.Cost
	if cost <= 0 {
		cost = slots * 100
	}
	if slots > c.total {
		return nil, ErrCapacityExceeded
	}
	if cost > c.totalCost {
		return nil, ErrCapacityExceeded
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if req.Workload == WorkloadLive {
		if !c.canAllocateLive(slots, cost) {
			return nil, ErrCapacityExceeded
		}
		c.active += slots
		c.activeLive += slots
		c.activeCost += cost
		c.activeLiveCost += cost
		return &slotLease{controller: c, workload: req.Workload, slots: slots, cost: cost}, nil
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			c.cond.Broadcast()
			c.mu.Unlock()
		case <-done:
		}
	}()
	defer close(done)

	c.queuedBatch++
	defer func() { c.queuedBatch-- }()
	for !c.canAllocateBatch(slots, cost) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.cond.Wait()
	}
	c.active += slots
	c.activeBatch += slots
	c.activeCost += cost
	c.activeBatchCost += cost
	return &slotLease{controller: c, workload: req.Workload, slots: slots, cost: cost}, nil
}

func (c *slotController) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		TotalSlots:        c.total,
		LiveReservedSlots: c.liveReserved,
		TotalCost:         c.totalCost,
		LiveReservedCost:  c.liveCostReserved,
		ActiveSlots:       c.active,
		ActiveBatchSlots:  c.activeBatch,
		ActiveLiveSlots:   c.activeLive,
		ActiveCost:        c.activeCost,
		ActiveBatchCost:   c.activeBatchCost,
		ActiveLiveCost:    c.activeLiveCost,
		QueuedBatch:       c.queuedBatch,
	}
}

func (c *slotController) canAllocateLive(slots, cost int) bool {
	return c.active+slots <= c.total && c.activeCost+cost <= c.totalCost
}

func (c *slotController) canAllocateBatch(slots, cost int) bool {
	return c.active+slots <= c.total-c.liveReserved &&
		c.activeCost+cost <= c.totalCost-c.liveCostReserved
}

type slotLease struct {
	controller *slotController
	workload   Workload
	slots      int
	cost       int
	once       sync.Once
}

func (l *slotLease) Release() {
	l.once.Do(func() {
		l.controller.mu.Lock()
		defer l.controller.mu.Unlock()
		l.controller.active -= l.slots
		l.controller.activeCost -= l.cost
		if l.workload == WorkloadLive {
			l.controller.activeLive -= l.slots
			l.controller.activeLiveCost -= l.cost
		} else {
			l.controller.activeBatch -= l.slots
			l.controller.activeBatchCost -= l.cost
		}
		l.controller.cond.Broadcast()
	})
}
