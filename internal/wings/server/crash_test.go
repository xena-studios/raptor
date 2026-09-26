package server

import (
	"testing"
	"time"
)

func TestCrashTracker(t *testing.T) {
	var c crashTracker
	t0 := time.Unix(10000, 0)
	rec := func(at time.Duration, runningSince time.Time) (time.Duration, bool) {
		return c.record(t0.Add(at), runningSince, crashWindow, crashDelays, crashLoopAt)
	}
	if d, loop := rec(0, time.Time{}); d != 0 || loop {
		t.Fatalf("1st crash: %s %v", d, loop)
	}
	if d, loop := rec(time.Minute, time.Time{}); d != 10*time.Second || loop {
		t.Fatalf("2nd crash: %s %v", d, loop)
	}
	if _, loop := rec(2*time.Minute, time.Time{}); !loop {
		t.Fatal("3rd crash within 10 minutes should be a crash loop")
	}

	// Crashes spread out beyond the window don't add up.
	c.reset()
	rec(0, time.Time{})
	rec(11*time.Minute, time.Time{})
	if _, loop := rec(22*time.Minute, time.Time{}); loop {
		t.Fatal("crashes 11 minutes apart aren't a loop")
	}

	// Running for the full window resets the counter.
	c.reset()
	rec(0, time.Time{})
	rec(time.Minute, time.Time{})
	if d, loop := rec(12*time.Minute, t0.Add(time.Minute)); loop || d != 0 {
		t.Fatalf("after 11 minutes running: %s %v", d, loop)
	}
}
