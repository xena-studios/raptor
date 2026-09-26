// Package events is Wings' event outbox (docs/ARCHITECTURE.md#mirror-sync).
// Every change on the node is appended with a monotonic sequence number; the
// Panel reads everything after the last number it acknowledged, so its mirror
// catches up after any disconnect.
package events

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// Event is one recorded change.
type Event struct {
	Seq      int64
	Type     string
	ServerID string
	Version  int64 // the server's config version, when it applies
	At       time.Time
	Data     map[string]any
}

// Retention: acknowledged events are kept for a while (for debugging and
// `raptor audit`); unacknowledged ones up to a cap, so a node that has never
// reached a Panel can't grow its database forever. If the Panel falls behind
// the oldest kept event, it asks for a full snapshot instead.
const (
	keepAcked  = 7 * 24 * time.Hour
	maxEvents  = 100_000
	ackedKey   = "events.acked_seq"
	pruneBatch = 10_000
)

// Outbox appends events and serves them to readers.
type Outbox struct {
	db *store.DB

	mu     sync.Mutex
	notify chan struct{} // closed and replaced on every append
}

// New returns an outbox on the state database.
func New(db *store.DB) *Outbox {
	return &Outbox{db: db, notify: make(chan struct{})}
}

// Append records an event in its own transaction.
func (o *Outbox) Append(ctx context.Context, e Event) (int64, error) {
	var seq int64
	err := o.db.WriteTx(ctx, func(q *store.Queries) error {
		var err error
		seq, err = AppendTx(ctx, q, e)
		return err
	})
	if err == nil {
		o.wake()
	}
	return seq, err
}

// AppendTx records an event inside the caller's transaction, so the event
// exists if and only if the change it describes was committed. Call Wake
// after the transaction commits.
func AppendTx(ctx context.Context, q *store.Queries, e Event) (int64, error) {
	if e.Type == "" {
		return 0, errors.New("event without a type")
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	data := "{}"
	if len(e.Data) > 0 {
		b, err := json.Marshal(e.Data)
		if err != nil {
			return 0, err
		}
		data = string(b)
	}
	return q.AppendEvent(ctx, store.AppendEventParams{
		Type: e.Type, ServerID: e.ServerID, Version: e.Version, At: e.At.UnixMilli(), Data: data,
	})
}

// Wake tells waiting readers that events were appended.
func (o *Outbox) Wake() { o.wake() }

func (o *Outbox) wake() {
	o.mu.Lock()
	close(o.notify)
	o.notify = make(chan struct{})
	o.mu.Unlock()
}

// Changed returns a channel that's closed on the next append.
func (o *Outbox) Changed() <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.notify
}

// ErrGap means events after seq were already pruned: the reader must rebuild
// from a snapshot instead.
var ErrGap = errors.New("events were pruned; a full snapshot is needed")

// Since returns up to limit events after seq, oldest first.
func (o *Outbox) Since(ctx context.Context, seq int64, limit int) ([]Event, error) {
	first, err := o.db.Read.FirstEventSeq(ctx)
	if err != nil {
		return nil, err
	}
	if first > 0 && seq < first-1 {
		return nil, ErrGap
	}
	rows, err := o.db.Read.EventsSince(ctx, store.EventsSinceParams{Seq: seq, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		e := Event{Seq: r.Seq, Type: r.Type, ServerID: r.ServerID, Version: r.Version, At: time.UnixMilli(r.At)}
		if r.Data != "{}" {
			if err := json.Unmarshal([]byte(r.Data), &e.Data); err != nil {
				return nil, err
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// Last returns the newest sequence number (0 if there are none).
func (o *Outbox) Last(ctx context.Context) (int64, error) { return o.db.Read.LastEventSeq(ctx) }

// Ack records that the Panel has everything up to seq.
func (o *Outbox) Ack(ctx context.Context, seq int64) error {
	cur, err := o.Acked(ctx)
	if err != nil {
		return err
	}
	if seq <= cur {
		return nil // acks never move backwards
	}
	last, err := o.Last(ctx)
	if err != nil {
		return err
	}
	if seq > last {
		return errors.New("ack beyond the last event")
	}
	return o.db.Write.SetKV(ctx, store.SetKVParams{Key: ackedKey, Value: []byte(strconv.FormatInt(seq, 10))})
}

// Acked returns the last acknowledged sequence number.
func (o *Outbox) Acked(ctx context.Context) (int64, error) {
	v, err := o.db.Read.GetKV(ctx, ackedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil // nothing acknowledged yet
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(string(v), 10, 64)
}

// Prune drops acknowledged events older than the retention window, and the
// oldest events beyond the cap whether acknowledged or not.
func (o *Outbox) Prune(ctx context.Context, now time.Time) (int64, error) {
	acked, err := o.Acked(ctx)
	if err != nil {
		return 0, err
	}
	last, err := o.Last(ctx)
	if err != nil {
		return 0, err
	}
	// Everything acknowledged and old enough.
	upTo := int64(0)
	if acked > 0 {
		rows, err := o.db.Read.EventsSince(ctx, store.EventsSinceParams{Seq: 0, Limit: pruneBatch})
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if r.Seq > acked || now.Sub(time.UnixMilli(r.At)) < keepAcked {
				break
			}
			upTo = r.Seq
		}
	}
	// The cap.
	upTo = max(upTo, last-maxEvents)
	if upTo <= 0 {
		return 0, nil
	}
	return o.db.Write.PruneEvents(ctx, upTo)
}
