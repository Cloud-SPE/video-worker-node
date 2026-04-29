package clock

import (
	"testing"
	"time"
)

func TestSystemClockNow(t *testing.T) {
	t.Parallel()
	c := System()
	got := c.Now()
	if got.IsZero() {
		t.Fatal("System().Now() returned zero time")
	}
}

func TestSystemClockSleepAndTimer(t *testing.T) {
	t.Parallel()
	c := System()
	c.Sleep(1 * time.Millisecond)
	timer := c.NewTimer(1 * time.Millisecond)
	select {
	case <-timer.Chan():
	case <-time.After(50 * time.Millisecond):
		t.Fatal("timer didn't fire")
	}
	timer.Stop()
	timer.Reset(1 * time.Millisecond)
	<-timer.Chan()
}
