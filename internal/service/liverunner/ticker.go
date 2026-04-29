package liverunner

import "time"

// tickerLike is the small surface drainEncoder needs from time.Ticker.
// Pulled out so a fake can be wired in tests if we ever need
// deterministic drain-encoder timing.
type tickerLike interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

func newTicker() tickerLike {
	return &realTicker{t: time.NewTicker(100 * time.Millisecond)}
}
