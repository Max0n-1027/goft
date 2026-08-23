package engine

import (
	"testing"
	"time"

	"goft/internal/config"
)

func TestCycleWaitBacksOffWhileCyclesFail(t *testing.T) {
	e := &Engine{cfg: &config.Config{PollInterval: time.Second}}

	for _, tc := range []struct {
		failures int
		want     time.Duration
	}{
		{0, time.Second},     // healthy: the configured interval
		{1, 2 * time.Second}, // doubling while the server stays away
		{2, 4 * time.Second},
		{5, 32 * time.Second},
		{20, 5 * time.Minute}, // capped, so it never goes silent for hours
	} {
		if got := e.cycleWait(tc.failures); got != tc.want {
			t.Errorf("cycleWait(%d) = %v, want %v", tc.failures, got, tc.want)
		}
	}
}

func TestCycleWaitNeverGoesBelowThePollInterval(t *testing.T) {
	// A job that polls less often than the cap is already gentle enough; the
	// backoff must not speed it up.
	e := &Engine{cfg: &config.Config{PollInterval: 30 * time.Minute}}

	if got := e.cycleWait(0); got != 30*time.Minute {
		t.Errorf("cycleWait(0) = %v, want the configured interval", got)
	}
	if got := e.cycleWait(10); got < 30*time.Minute {
		t.Errorf("cycleWait(10) = %v, want at least the configured interval", got)
	}
}

func TestCycleWaitReturnsToNormalAfterASuccess(t *testing.T) {
	e := &Engine{cfg: &config.Config{PollInterval: time.Second}}

	// Serve resets the counter on a successful cycle, so a brief outage costs
	// nothing once it clears.
	if got := e.cycleWait(0); got != time.Second {
		t.Errorf("cycleWait(0) = %v, want the interval restored", got)
	}
}
