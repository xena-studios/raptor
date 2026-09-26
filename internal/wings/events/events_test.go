package events

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/store"
)

func open(t *testing.T) (*Outbox, *store.DB) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(db), db
}

func TestAppendSinceAck(t *testing.T) {
	ctx := context.Background()
	o, _ := open(t)
	changed := o.Changed()
	for i := range 5 {
		seq, err := o.Append(ctx, Event{Type: "server.state", ServerID: "s1", Data: map[string]any{"n": i}})
		if err != nil || seq != int64(i+1) {
			t.Fatalf("append %d: seq %d, %v", i, seq, err)
		}
	}
	select {
	case <-changed:
	default:
		t.Fatal("readers weren't woken")
	}

	got, err := o.Since(ctx, 2, 10)
	if err != nil || len(got) != 3 || got[0].Seq != 3 || got[0].Data["n"] != float64(2) {
		t.Fatalf("since 2: %+v, %v", got, err)
	}
	if err := o.Ack(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := o.Ack(ctx, 1); err != nil { // never moves backwards
		t.Fatal(err)
	}
	if a, _ := o.Acked(ctx); a != 3 {
		t.Fatalf("acked = %d", a)
	}
	if err := o.Ack(ctx, 99); err == nil {
		t.Fatal("ack beyond the last event accepted")
	}
}

func TestAppendTxIsAtomic(t *testing.T) {
	ctx := context.Background()
	o, db := open(t)
	boom := errors.New("boom")
	err := db.WriteTx(ctx, func(q *store.Queries) error {
		if _, err := AppendTx(ctx, q, Event{Type: "server.created", ServerID: "s1"}); err != nil {
			return err
		}
		return boom // the change failed, so the event must not exist
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if last, _ := o.Last(ctx); last != 0 {
		t.Fatalf("event from a rolled-back transaction survived (last=%d)", last)
	}
}

func TestPruneAndGap(t *testing.T) {
	ctx := context.Background()
	o, _ := open(t)
	old := time.Now().Add(-8 * 24 * time.Hour)
	for range 3 {
		if _, err := o.Append(ctx, Event{Type: "x", At: old}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.Append(ctx, Event{Type: "x"}); err != nil {
		t.Fatal(err)
	}

	// Nothing acknowledged: nothing pruned (under the cap).
	if n, _ := o.Prune(ctx, time.Now()); n != 0 {
		t.Fatalf("pruned %d unacknowledged events", n)
	}
	if err := o.Ack(ctx, 4); err != nil {
		t.Fatal(err)
	}
	// Acknowledged and old: 1–3 go; 4 is recent and stays.
	if n, err := o.Prune(ctx, time.Now()); n != 3 || err != nil {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if _, err := o.Since(ctx, 0, 10); !errors.Is(err, ErrGap) {
		t.Fatalf("reading pruned events: %v, want ErrGap", err)
	}
	if got, err := o.Since(ctx, 3, 10); err != nil || len(got) != 1 {
		t.Fatalf("since 3: %v %v", got, err)
	}
	// A seq is never reused after pruning.
	if seq, _ := o.Append(ctx, Event{Type: "x"}); seq != 5 {
		t.Fatalf("seq after prune = %d", seq)
	}
}
