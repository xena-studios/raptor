package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// MaxTargets is how many destinations a server's backups can go to.
const MaxTargets = 5

// Target is a destination a server's backups go to, and how many of them
// it keeps there.
type Target struct {
	DestinationID string `json:"destination_id"`
	Retention
}

// Policy is a server's backup settings: where its backups go, in order (the
// first is the primary), and what they leave out.
type Policy struct {
	Targets []Target `json:"targets"`
	Ignore  []string `json:"ignore,omitempty"` // gitignore-style patterns
}

// UnmarshalJSON also takes the settings as they were before several
// destinations: one destination_id and its keep values.
func (p *Policy) UnmarshalJSON(b []byte) error {
	var v struct {
		Targets       []Target `json:"targets"`
		Ignore        []string `json:"ignore"`
		DestinationID string   `json:"destination_id"`
		Retention
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.Targets, p.Ignore = v.Targets, v.Ignore
	if p.Targets == nil && (v.DestinationID != "" || v.Retention != (Retention{})) {
		p.Targets = []Target{{DestinationID: v.DestinationID, Retention: v.Retention}}
	}
	return nil
}

// DefaultPolicy is used by servers without settings of their own.
func DefaultPolicy() Policy {
	return Policy{Targets: []Target{{DestinationID: LocalDestination, Retention: DefaultRetention}}}
}

// Primary is the first target: where safety backups go.
func (p Policy) Primary() Target {
	if len(p.Targets) == 0 {
		return DefaultPolicy().Targets[0]
	}
	return p.Targets[0]
}

// Target returns the target for a destination.
func (p Policy) Target(destID string) (Target, bool) {
	i := slices.IndexFunc(p.Targets, func(t Target) bool { return t.DestinationID == destID })
	if i < 0 {
		return Target{}, false
	}
	return p.Targets[i], true
}

// Offsite is the first target that isn't the node's own disk, or the
// primary if there's none: a deleted server's final backup goes there, so
// it outlives the node.
func (p Policy) Offsite() Target {
	for _, t := range p.Targets {
		if t.DestinationID != LocalDestination {
			return t
		}
	}
	return p.Primary()
}

func (p *Policy) validate() error {
	if len(p.Targets) == 0 {
		return errors.New("choose at least one destination")
	}
	if len(p.Targets) > MaxTargets {
		return fmt.Errorf("backups can go to at most %d destinations", MaxTargets)
	}
	seen := map[string]bool{}
	for i := range p.Targets {
		t := &p.Targets[i]
		if t.DestinationID == "" {
			t.DestinationID = LocalDestination
		}
		if seen[t.DestinationID] {
			return errors.New("each destination can be chosen once")
		}
		seen[t.DestinationID] = true
		if err := t.validate(); err != nil {
			return err
		}
	}
	if len(p.Ignore) > maxIgnore {
		return fmt.Errorf("at most %d ignore patterns", maxIgnore)
	}
	for _, s := range p.Ignore {
		if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\n\r\x00") || len(s) > 1000 {
			return fmt.Errorf("bad ignore pattern %q", s)
		}
	}
	return nil
}

// KeepsLess reports whether p would let retention delete backups cur keeps:
// a destination both keep backups on has a lower keep value. (A destination
// p drops keeps its backups: retention stops running there.)
func (p Policy) KeepsLess(cur Policy) bool {
	for _, old := range cur.Targets {
		if t, ok := p.Target(old.DestinationID); ok && t.KeepsLess(old.Retention) {
			return true
		}
	}
	return false
}

// Added is the destinations p sends backups to that cur doesn't.
func (p Policy) Added(cur Policy) []string {
	var out []string
	for _, t := range p.Targets {
		if _, ok := cur.Target(t.DestinationID); !ok {
			out = append(out, t.DestinationID)
		}
	}
	return out
}

// Policy returns a server's backup settings.
func (m *Manager) Policy(ctx context.Context, serverID string) (Policy, error) {
	r, err := m.o.Store.Read.GetBackupPolicy(ctx, serverID)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, err
	}
	var p Policy
	if err := json.Unmarshal([]byte(r.Ignore), &p.Ignore); err != nil {
		return Policy{}, err
	}
	targets, err := m.o.Store.Read.ListBackupTargets(ctx, serverID)
	if err != nil {
		return Policy{}, err
	}
	for _, t := range targets {
		p.Targets = append(p.Targets, Target{DestinationID: t.DestinationID, Retention: Retention{
			KeepLast: int(t.KeepLast), KeepDaily: int(t.KeepDaily), KeepWeekly: int(t.KeepWeekly), KeepMonthly: int(t.KeepMonthly),
		}})
	}
	if len(p.Targets) == 0 { // written by a Wings before several destinations
		p.Targets = []Target{{DestinationID: r.DestinationID, Retention: Retention{
			KeepLast: int(r.KeepLast), KeepDaily: int(r.KeepDaily), KeepWeekly: int(r.KeepWeekly), KeepMonthly: int(r.KeepMonthly),
		}}}
	}
	return p, nil
}

// SetPolicy replaces a server's backup settings. Existing backups stay
// where they are; retention applies to them from the next backup.
func (m *Manager) SetPolicy(ctx context.Context, serverID string, p Policy) error {
	if err := p.validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if _, err := m.o.Servers.Status(serverID); err != nil {
		return err
	}
	ignore, err := json.Marshal(p.Ignore)
	if err != nil {
		return err
	}
	primary := p.Primary()
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		for _, t := range p.Targets {
			if _, err := q.GetBackupDestination(ctx, t.DestinationID); err != nil {
				return ErrDestination
			}
		}
		// The primary target in the old columns too, for a rollback.
		if err := q.UpsertBackupPolicy(ctx, store.UpsertBackupPolicyParams{
			ServerID: serverID, DestinationID: primary.DestinationID,
			KeepLast: int64(primary.KeepLast), KeepDaily: int64(primary.KeepDaily), KeepWeekly: int64(primary.KeepWeekly), KeepMonthly: int64(primary.KeepMonthly),
			Ignore: string(ignore), UpdatedAt: m.o.Now().UnixMilli(),
		}); err != nil {
			return err
		}
		if err := q.DeleteBackupTargets(ctx, serverID); err != nil {
			return err
		}
		for i, t := range p.Targets {
			if err := q.InsertBackupTarget(ctx, store.InsertBackupTargetParams{
				ServerID: serverID, DestinationID: t.DestinationID, Position: int64(i),
				KeepLast: int64(t.KeepLast), KeepDaily: int64(t.KeepDaily), KeepWeekly: int64(t.KeepWeekly), KeepMonthly: int64(t.KeepMonthly),
			}); err != nil {
				return err
			}
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventPolicy, ServerID: serverID, Data: map[string]any{"policy": p}})
		return err
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}
