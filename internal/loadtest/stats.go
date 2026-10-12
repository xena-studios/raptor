package loadtest

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Stats is what the fleet saw, for the run's reports.
type Stats struct {
	mu          sync.Mutex
	up          int
	connects    int
	dialErrors  map[string]int
	rpcErrors   map[string]int
	calls       map[string]int
	events      int
	pulled      int
	handshakes  []time.Duration
	deliveries  []time.Duration
	lastConnect time.Time
}

// NewStats starts counting.
func NewStats() *Stats {
	return &Stats{dialErrors: map[string]int{}, rpcErrors: map[string]int{}, calls: map[string]int{}}
}

func (s *Stats) connected(handshake time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.up++
	s.connects++
	s.handshakes = append(s.handshakes, handshake)
	s.lastConnect = time.Now()
}

func (s *Stats) disconnected() {
	s.mu.Lock()
	s.up--
	s.mu.Unlock()
}

// errKey is an error's first line, so the same failure counts together.
func errKey(err error) string {
	k, _, _ := strings.Cut(err.Error(), "\n")
	if len(k) > 120 {
		k = k[:120]
	}
	return k
}

func (s *Stats) dialFailed(err error) {
	s.mu.Lock()
	s.dialErrors[errKey(err)]++
	s.mu.Unlock()
}

func (s *Stats) rpcFailed(rpc string, err error) {
	s.mu.Lock()
	s.rpcErrors[rpc+": "+errKey(err)]++
	s.mu.Unlock()
}

func (s *Stats) call(rpc string) {
	s.mu.Lock()
	s.calls[rpc]++
	s.mu.Unlock()
}

func (s *Stats) event() {
	s.mu.Lock()
	s.events++
	s.mu.Unlock()
}

func (s *Stats) delivered(d time.Duration) {
	s.mu.Lock()
	s.pulled++
	s.deliveries = append(s.deliveries, d)
	s.mu.Unlock()
}

// Snapshot is the counts so far.
type Snapshot struct {
	Up, Connects, Events, Pulled int
	Calls, DialErrors, RPCErrors map[string]int
	Handshake, Delivery          Percentiles
}

// Percentiles of a set of durations.
type Percentiles struct {
	N             int
	P50, P95, P99 time.Duration
	Max           time.Duration
}

func percentiles(d []time.Duration) Percentiles {
	if len(d) == 0 {
		return Percentiles{}
	}
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(p float64) time.Duration { return s[min(len(s)-1, int(p*float64(len(s))))] }
	return Percentiles{N: len(s), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: s[len(s)-1]}
}

// Snapshot copies the counts.
func (s *Stats) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := func(m map[string]int) map[string]int {
		out := make(map[string]int, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return Snapshot{
		Up: s.up, Connects: s.connects, Events: s.events, Pulled: s.pulled,
		Calls: cp(s.calls), DialErrors: cp(s.dialErrors), RPCErrors: cp(s.rpcErrors),
		Handshake: percentiles(s.handshakes), Delivery: percentiles(s.deliveries),
	}
}

func (p Percentiles) String() string {
	if p.N == 0 {
		return "none yet"
	}
	r := func(d time.Duration) time.Duration { return d.Round(time.Millisecond) }
	return fmt.Sprintf("p50 %s, p95 %s, p99 %s, max %s (%d)", r(p.P50), r(p.P95), r(p.P99), r(p.Max), p.N)
}

// String is a report for people.
func (s Snapshot) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "linked %d (%d connects) · events made %d, pulled %d\n", s.Up, s.Connects, s.Events, s.Pulled)
	fmt.Fprintf(&b, "  handshake: %s\n", s.Handshake)
	fmt.Fprintf(&b, "  event delivery (made → pulled by the Panel): %s\n", s.Delivery)
	if len(s.Calls) > 0 {
		keys := slices.Sorted(func(yield func(string) bool) {
			for k := range s.Calls {
				if !yield(k) {
					return
				}
			}
		})
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s %d", k, s.Calls[k]))
		}
		fmt.Fprintf(&b, "  Panel's calls: %s\n", strings.Join(parts, ", "))
	}
	for k, v := range s.DialErrors {
		fmt.Fprintf(&b, "  dial error ×%d: %s\n", v, k)
	}
	for k, v := range s.RPCErrors {
		fmt.Fprintf(&b, "  error ×%d: %s\n", v, k)
	}
	return b.String()
}
