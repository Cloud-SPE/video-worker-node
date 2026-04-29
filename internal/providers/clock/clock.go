// Package clock provides a small Clock interface so service code can be
// tested with a fake clock (deterministic time / timer behaviour).
package clock

import "time"

// Clock is the minimal time abstraction used by service / runner code.
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
	Sleep(d time.Duration)
}

// Timer is a controllable timer (mirrors time.Timer's surface).
type Timer interface {
	Chan() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// System returns a Clock backed by the real wall clock.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time             { return time.Now() }
func (systemClock) Sleep(d time.Duration)      { time.Sleep(d) }
func (systemClock) NewTimer(d time.Duration) Timer {
	t := time.NewTimer(d)
	return realTimer{t: t}
}

type realTimer struct{ t *time.Timer }

func (r realTimer) Chan() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool             { return r.t.Stop() }
func (r realTimer) Reset(d time.Duration) bool { return r.t.Reset(d) }
