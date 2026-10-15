// Package thresholds evaluates an org's threshold alert rules on the node,
// which has the metrics the Panel doesn't (docs/WINGS.md#threshold-alerts):
// CPU, memory, or disk above a percent for a while, for a server or the
// machine. The Panel sends the rules (node.alerts); a rule that starts or
// stops holding raises alert.fired or alert.resolved, which reach the Panel
// with the other events and are sent from there (docs/PANEL.md#alerts).
package thresholds

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Rule is a threshold rule as the Panel sends it.
type Rule struct {
	ID         string  `json:"id"`
	Condition  string  `json:"condition"`
	ServerID   string  `json:"server_id,omitempty"`
	Threshold  float64 `json:"threshold"`
	ForSeconds int     `json:"for_seconds"`
}

// Conditions a node evaluates; server ones apply to each server (or the one
// named), node ones to the machine.
var (
	serverConditions = []string{"cpu_high", "memory_high", "disk_high"}
	nodeConditions   = []string{"node_cpu_high", "node_memory_high", "node_disk_high"}
)

// Limits.
const (
	maxRules = 200
	// resolveAfter is how long a firing rule must stay under its threshold
	// before it's resolved, so a value hovering at it doesn't flap.
	resolveAfter = time.Minute
	checkEvery   = metrics.SampleEvery
	rulesKey     = "alerts.rules"
	firingKey    = "alerts.firing"
)

// Metrics is what the evaluator reads (*metrics.Collector).
type Metrics interface {
	Latest(id string) metrics.Point
	HostLatest() metrics.HostPoint
}

// Servers is what it needs from the server manager.
type Servers interface {
	List() map[string]server.State
	Get(ctx context.Context, id string) (*server.Server, error)
}

// Host is the machine's size.
type Host struct {
	CPUs        int
	MemoryBytes int64
	// DiskBytes is the servers' filesystem's size.
	DiskBytes int64
}

// Evaluator checks the rules every sample.
type Evaluator struct {
	DB      *store.DB
	Outbox  *events.Outbox
	Metrics Metrics
	Servers Servers
	Host    func() Host
	Log     *slog.Logger
	Now     func() time.Time

	mu    sync.Mutex
	rules []Rule
	state map[key]*state
}

type key struct{ rule, target string }

type state struct {
	above, below time.Time // when the value went over, or back under
	firing       bool
}

func (e *Evaluator) init() {
	if e.state == nil {
		e.state = map[key]*state{}
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Log == nil {
		e.Log = slog.New(slog.DiscardHandler)
	}
}

// Validate checks rules from the Panel.
func Validate(rules []Rule) error {
	if len(rules) > maxRules {
		return fmt.Errorf("at most %d rules", maxRules)
	}
	for _, r := range rules {
		if r.ID == "" {
			return errors.New("a rule needs an id")
		}
		node := slices.Contains(nodeConditions, r.Condition)
		if !node && !slices.Contains(serverConditions, r.Condition) {
			return fmt.Errorf("rule %s: unknown condition %q", r.ID, r.Condition)
		}
		if node && r.ServerID != "" {
			return fmt.Errorf("rule %s: %s is about the node, not a server", r.ID, r.Condition)
		}
		if r.Threshold <= 0 || r.Threshold > 100 || r.ForSeconds < 0 || r.ForSeconds > 86400 {
			return fmt.Errorf("rule %s: threshold or duration out of range", r.ID)
		}
	}
	return nil
}

// Load reads the rules and what was firing when Wings stopped, so a restart
// neither repeats an alert nor misses its resolution.
func (e *Evaluator) Load(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	raw, err := e.DB.Read.GetKV(ctx, rulesKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &e.rules); err != nil {
		return err
	}
	if raw, err = e.DB.Read.GetKV(ctx, firingKey); err == nil {
		var firing [][2]string
		if json.Unmarshal(raw, &firing) == nil {
			now := e.Now()
			for _, f := range firing {
				e.state[key{f[0], f[1]}] = &state{firing: true, above: now}
			}
		}
	}
	return nil
}

// Set replaces the rules. What a removed rule had firing is forgotten (it
// isn't resolved: the rule is gone).
func (e *Evaluator) Set(ctx context.Context, rules []Rule) error {
	if err := Validate(rules); err != nil {
		return err
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if err := e.DB.Write.SetKV(ctx, store.SetKVParams{Key: rulesKey, Value: raw}); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	old := map[string]Rule{}
	for _, r := range e.rules {
		old[r.ID] = r
	}
	keep := map[string]bool{}
	for _, r := range rules {
		// An unchanged rule keeps its state; a changed one starts over.
		if o, ok := old[r.ID]; ok && o == r {
			keep[r.ID] = true
		}
	}
	for k := range e.state {
		if !keep[k.rule] {
			delete(e.state, k)
		}
	}
	e.rules = rules
	return e.saveFiring(ctx)
}

// Rules returns the rules in force.
func (e *Evaluator) Rules() []Rule {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.rules)
}

func (e *Evaluator) saveFiring(ctx context.Context) error {
	firing := [][2]string{}
	for k, s := range e.state {
		if s.firing {
			firing = append(firing, [2]string{k.rule, k.target})
		}
	}
	slices.SortFunc(firing, func(a, b [2]string) int {
		if a[0] != b[0] {
			return compare(a[0], b[0])
		}
		return compare(a[1], b[1])
	})
	raw, _ := json.Marshal(firing)
	return e.DB.Write.SetKV(context.WithoutCancel(ctx), store.SetKVParams{Key: firingKey, Value: raw})
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Run checks the rules every sample until ctx ends.
func (e *Evaluator) Run(ctx context.Context) {
	t := time.NewTicker(checkEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Check(ctx)
		}
	}
}

// reading is one value a rule compares, for one target.
type reading struct {
	target string // a server ID, or "" for the machine
	value  float64
}

func percent(n, of float64) float64 {
	if of <= 0 {
		return 0
	}
	return n / of * 100
}

// readings are a rule's values now, one per target it applies to.
func (e *Evaluator) readings(ctx context.Context, r Rule, h Host, limits map[string]containers.Limits) []reading {
	hp := e.Metrics.HostLatest()
	switch r.Condition {
	case "node_cpu_high":
		return []reading{{"", hp.CPUAvg}}
	case "node_memory_high":
		return []reading{{"", percent(float64(hp.MemoryAvg), float64(h.MemoryBytes))}}
	case "node_disk_high":
		return []reading{{"", percent(float64(hp.DiskBytes), float64(h.DiskBytes))}}
	}
	var ids []string
	if r.ServerID != "" {
		ids = []string{r.ServerID}
	} else {
		for id := range e.Servers.List() {
			ids = append(ids, id)
		}
		slices.Sort(ids)
	}
	out := make([]reading, 0, len(ids))
	for _, id := range ids {
		l, ok := limits[id]
		if !ok {
			s, err := e.Servers.Get(ctx, id)
			if err != nil {
				continue // gone
			}
			l = s.Limits
			limits[id] = l
		}
		p := e.Metrics.Latest(id)
		var v float64
		switch r.Condition {
		case "cpu_high":
			// Of its CPU limit, or of the whole machine without one.
			if l.CPUPercent > 0 {
				v = percent(p.CPUAvg, float64(l.CPUPercent))
			} else {
				v = percent(p.CPUAvg, float64(max(h.CPUs, 1)*100))
			}
		case "memory_high":
			mem := l.MemoryMiB << 20
			if mem <= 0 {
				mem = h.MemoryBytes
			}
			v = percent(float64(p.MemoryAvg), float64(mem))
		case "disk_high":
			if l.DiskMiB <= 0 {
				continue // no limit: the node's disk rules cover it
			}
			v = percent(float64(p.DiskBytes), float64(l.DiskMiB<<20))
		}
		out = append(out, reading{id, v})
	}
	return out
}

// Check compares every rule with the latest sample, raising alert.fired
// when one has held for its duration and alert.resolved when it has been
// back under for a minute.
func (e *Evaluator) Check(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	if len(e.rules) == 0 {
		return
	}
	var h Host
	if e.Host != nil {
		h = e.Host()
	}
	now := e.Now()
	limits := map[string]containers.Limits{}
	seen := map[key]bool{}
	changed := false
	for _, r := range e.rules {
		for _, rd := range e.readings(ctx, r, h, limits) {
			k := key{r.ID, rd.target}
			seen[k] = true
			s := e.state[k]
			if s == nil {
				s = &state{}
				e.state[k] = s
			}
			if rd.value >= r.Threshold {
				s.below = time.Time{}
				if s.above.IsZero() {
					s.above = now
				}
				if !s.firing && now.Sub(s.above) >= time.Duration(r.ForSeconds)*time.Second {
					s.firing, changed = true, true
					e.raise(ctx, "alert.fired", r, rd)
				}
				continue
			}
			s.above = time.Time{}
			if !s.firing {
				continue
			}
			if s.below.IsZero() {
				s.below = now
			}
			if now.Sub(s.below) >= resolveAfter {
				s.firing, s.below, changed = false, time.Time{}, true
				e.raise(ctx, "alert.resolved", r, rd)
			}
		}
	}
	// A server that's gone takes its state with it.
	for k, s := range e.state {
		if !seen[k] {
			delete(e.state, k)
			changed = changed || s.firing
		}
	}
	if changed {
		if err := e.saveFiring(ctx); err != nil {
			e.Log.Error("recording threshold alerts failed", "err", err)
		}
	}
}

func (e *Evaluator) raise(ctx context.Context, typ string, r Rule, rd reading) {
	_, err := e.Outbox.Append(context.WithoutCancel(ctx), events.Event{Type: typ, ServerID: rd.target, Data: map[string]any{
		"rule_id": r.ID, "condition": r.Condition, "value": rd.value, "threshold": r.Threshold, "for_seconds": r.ForSeconds,
	}})
	if err != nil {
		e.Log.Error("recording a threshold alert failed", "rule", r.ID, "err", err)
	}
}
