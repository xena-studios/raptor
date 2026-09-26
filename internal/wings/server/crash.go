package server

import "time"

// Crash policy (docs/SERVERS.md#crash-policy).
const (
	crashWindow   = 10 * time.Minute // crashes counted within this window
	crashLoopAt   = 3                // this many crashes in the window = crash loop
	crashLastLogs = 50               // console lines included in a crash event
)

// crashDelays[n] is the wait before restarting after the (n+1)th crash in the
// window. The crashLoopAt-th crash doesn't restart.
var crashDelays = []time.Duration{0, 10 * time.Second}

// crashTracker counts recent crashes.
type crashTracker struct {
	times []time.Time
}

// record registers a crash at now. runningSince is when the server last
// reached "running" (zero if it never did). It returns how long to wait
// before restarting, or loop=true if the server is crash looping.
//
// The counter resets once a server has been running for the whole window,
// so a server that crashes once a day never counts as looping.
func (c *crashTracker) record(now, runningSince time.Time, window time.Duration, delays []time.Duration, loopAt int) (delay time.Duration, loop bool) {
	if !runningSince.IsZero() && now.Sub(runningSince) >= window {
		c.times = nil
	}
	recent := c.times[:0]
	for _, t := range c.times {
		if now.Sub(t) < window {
			recent = append(recent, t)
		}
	}
	c.times = append(recent, now)
	n := len(c.times)
	if n >= loopAt {
		return 0, true
	}
	return delays[min(n-1, len(delays)-1)], false
}

func (c *crashTracker) reset() { c.times = nil }
