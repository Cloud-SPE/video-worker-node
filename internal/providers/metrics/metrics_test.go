package metrics

import (
	"sync"
	"testing"
	"time"
)

type spy struct {
	mu    sync.Mutex
	calls int
}

func (s *spy) CounterAdd(string, map[string]string, float64)       {}
func (s *spy) HistogramObserve(string, map[string]string, float64) { s.mu.Lock(); s.calls++; s.mu.Unlock() }
func (s *spy) GaugeSet(string, map[string]string, float64)         {}

func TestNoOp(t *testing.T) {
	t.Parallel()
	r := NoOp()
	r.CounterAdd("foo", nil, 1)
	r.HistogramObserve("foo", nil, 0.5)
	r.GaugeSet("foo", nil, 7)
}

func TestTimer(t *testing.T) {
	t.Parallel()
	s := &spy{}
	tm := StartTimer(s, NameJobPhaseDuration, nil)
	time.Sleep(1 * time.Millisecond)
	tm.Stop()
	if s.calls != 1 {
		t.Fatalf("calls=%d want 1", s.calls)
	}
	// Nil safety
	var nilT *Timer
	nilT.Stop()
}

func TestTimerNilRecorder(t *testing.T) {
	t.Parallel()
	tm := StartTimer(nil, NameJobsTotal, map[string]string{"a": "b"})
	tm.Stop()
}
