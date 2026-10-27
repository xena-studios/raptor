package billing

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestBillable(t *testing.T) {
	for _, c := range []struct {
		name  string
		nodes int
		w     *Waiver
		want  int
		next  bool
	}{
		{"no waiver", 3, nil, 3, false},
		{"no nodes", 0, nil, 0, false},
		{"some free", 3, &Waiver{FreeNodes: 1}, 2, false},
		{"as many free as nodes", 2, &Waiver{FreeNodes: 2}, 0, false},
		{"more free than nodes", 2, &Waiver{FreeNodes: 5}, 0, true},
		{"all", 7, &Waiver{AllNodes: true}, 0, true},
		{"storage only", 2, &Waiver{Storage: true}, 2, false},
	} {
		if got := Billable(c.nodes, c.w); got != c.want {
			t.Errorf("%s: billable %d, want %d", c.name, got, c.want)
		}
		if c.w != nil && c.w.CoversNext(c.nodes) != c.next {
			t.Errorf("%s: covers the next node: %v", c.name, !c.next)
		}
	}
	if (*Waiver)(nil).StorageFree() || !(&Waiver{Storage: true}).StorageFree() {
		t.Error("StorageFree")
	}
}

// A waiver past its end date is over, even before the sweep ends it.
func TestWaiverOf(t *testing.T) {
	now := time.Now()
	id := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	if w := waiverOf(pgtype.UUID{}, true, 0, false, pgtype.Timestamptz{}, now); w != nil {
		t.Errorf("no row: %v", w)
	}
	if w := waiverOf(id, true, 0, false, pgtype.Timestamptz{}, now); w == nil || !w.AllNodes || !w.EndsAt.IsZero() {
		t.Errorf("no end date: %v", w)
	}
	if w := waiverOf(id, false, 2, false, pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}, now); w == nil || w.FreeNodes != 2 {
		t.Errorf("ending later: %v", w)
	}
	if w := waiverOf(id, false, 2, false, pgtype.Timestamptz{Time: now, Valid: true}, now); w != nil {
		t.Errorf("ended: %v", w)
	}
}
