package billing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Waivers (DECISIONS #334): staff give an org some of its nodes free, or
// all of them, and optionally its Raptor Backup Storage past what's
// included. Polar is charged for the rest: seats are the nodes less the
// free ones, and an org whose nodes are all free needs no plan.

// WaiverReminder is how long before a waiver's end date its org's owners
// are told.
const WaiverReminder = 3 * 24 * time.Hour

// Waiver is a waiver in force.
type Waiver struct {
	ID        uuid.UUID
	AllNodes  bool
	FreeNodes int
	Storage   bool
	// EndsAt is when it runs out; zero: until staff end it.
	EndsAt time.Time
}

// Free is how many of nodes it makes free (none for a nil waiver).
func (w *Waiver) Free(nodes int) int {
	switch {
	case w == nil:
		return 0
	case w.AllNodes:
		return nodes
	}
	return min(w.FreeNodes, nodes)
}

// Billable is how many of an org's nodes are charged: the seats its plan
// needs.
func Billable(nodes int, w *Waiver) int { return max(nodes-w.Free(nodes), 0) }

// CoversNext reports whether one more node would be free too.
func (w *Waiver) CoversNext(nodes int) bool { return Billable(nodes+1, w) == 0 }

// StorageFree reports whether Backup Storage past what's included is free.
func (w *Waiver) StorageFree() bool { return w != nil && w.Storage }

func waiverOf(id pgtype.UUID, all bool, free int32, storage bool, ends pgtype.Timestamptz, now time.Time) *Waiver {
	if !id.Valid {
		return nil
	}
	w := &Waiver{ID: id.Bytes, AllNodes: all, FreeNodes: int(free), Storage: storage}
	if ends.Valid {
		// Past its end, it's over even before the sweep says so.
		if !now.Before(ends.Time) {
			return nil
		}
		w.EndsAt = ends.Time
	}
	return w
}

// ActiveWaiver is the org's waiver in force, or nil.
func (s *Service) ActiveWaiver(ctx context.Context, org uuid.UUID) (*Waiver, error) {
	row, err := store.New(s.DB).ActiveWaiver(ctx, pgID(org))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return waiverOf(row.ID, row.AllNodes, row.FreeNodes, row.Storage, row.EndsAt, s.now()), nil
}

// sweepWaiver ends an org's waiver at its end date, telling the owners a
// few days before and then, and returns the waiver still in force.
func (s *Service) sweepWaiver(ctx context.Context, q *store.Queries, r store.BillingOrgsRow, now time.Time) (*Waiver, error) {
	if !r.WaiverID.Valid {
		return nil, nil
	}
	w := &Waiver{ID: r.WaiverID.Bytes, AllNodes: r.WaiverAllNodes, FreeNodes: int(r.WaiverFreeNodes), Storage: r.WaiverStorage}
	if !r.WaiverEndsAt.Valid {
		return w, nil
	}
	w.EndsAt = r.WaiverEndsAt.Time
	org := uuid.UUID(r.OrgID.Bytes)
	date := w.EndsAt.UTC().Format("January 2, 2006")
	what := s.waiverWords(w)
	if !now.Before(w.EndsAt) {
		n, err := q.EndWaiver(ctx, store.EndWaiverParams{ID: r.WaiverID, EndedAt: pgtype.Timestamptz{Time: now, Valid: true}})
		if err != nil {
			return nil, err
		}
		if n > 0 {
			s.ledger(ctx, q, r.OrgID, "waiver.ended", w.Free(int(r.Nodes)), 0, "", "", map[string]any{"waiver": w.ID, "why": "end_date"})
			s.email(ctx, q, r.OrgID, "Raptor: your free nodes have ended",
				fmt.Sprintf("Raptor gave your org %%s %s until %s. That's ended, so from today it's charged like any org: %s.\n\nIf the org doesn't have a plan, an owner can start one in Settings, Billing:\n%s\n\nReply to this email if that's not what you expected.\n",
					what, date, chargedWords(w), s.billingURL(org)))
		}
		return nil, nil
	}
	if now.After(w.EndsAt.Add(-WaiverReminder)) && !slices.Contains(r.WaiverNotified, "reminder") {
		s.email(ctx, q, r.OrgID, "Raptor: your free nodes end on "+date,
			fmt.Sprintf("Raptor gives your org %%s %s until %s. After that, it's charged like any org: %s.\n\nIf the org doesn't have a plan by then, an owner can start one in Settings, Billing:\n%s\n\nReply to this email if that's not what you expected.\n",
				what, date, chargedWords(w), s.billingURL(org)))
		if err := q.SetWaiverNotified(ctx, store.SetWaiverNotifiedParams{ID: r.WaiverID, Notified: append(slices.Clone(r.WaiverNotified), "reminder")}); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// waiverWords says what a waiver gives, for emails.
func (s *Service) waiverWords(w *Waiver) string {
	var nodes string
	switch {
	case w.AllNodes:
		nodes = "all its nodes free"
	case w.FreeNodes == 1:
		nodes = "1 node free"
	case w.FreeNodes > 1:
		nodes = fmt.Sprintf("%d nodes free", w.FreeNodes)
	}
	switch {
	case w.Storage && nodes != "":
		return nodes + ", and its Raptor Backup Storage past what's included,"
	case w.Storage:
		return "its Raptor Backup Storage past what's included free"
	}
	return nodes
}

// chargedWords says what's charged once a waiver ends.
func chargedWords(w *Waiver) string {
	nodes := fmt.Sprintf("$%d a node a month", NodePriceCents/100)
	storage := "Backup Storage past the included 10 GB a node at $0.012 per GB a month"
	switch {
	case w.Storage && (w.AllNodes || w.FreeNodes > 0):
		return nodes + ", and " + storage
	case w.Storage:
		return storage
	}
	return nodes
}
