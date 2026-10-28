package billing

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/hosted"
)

// Raptor Backup Storage, charged (DECISIONS #335): each measured day, an
// org's use past what's included (10 GB per linked node, pooled) goes to
// Polar as one event, in GB-months: the GB over that day, over the days in
// its month. A meter sums them and a metered price on the node product
// charges $0.012 each at the end of the period. Off until the meter is
// configured.

// StorageEvent is the events' name, which the meter filters on.
const StorageEvent = "backup_storage"

// storageWindow is how far back the sweep bills measured days: today's
// and yesterday's, so a missed day is caught up but turning billing on
// doesn't bill the past.
const storageWindow = 1

// StorageBilling reports whether Backup Storage past what's included is
// charged.
func (s *Service) StorageBilling() bool { return s.Enabled() && s.StorageMeterID != "" }

// StorageOver is what an org stores past what its nodes include, in bytes.
func StorageOver(used int64, nodes int) int64 {
	return max(used-int64(nodes)*hosted.IncludedBytesPerNode, 0)
}

// GBMonths is a day's share of a month of storing over bytes (1 GB is
// 1e9 bytes, as the TB price is per 1e12).
func GBMonths(over int64, day time.Time) float64 {
	days := time.Date(day.Year(), day.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	v := float64(over) / 1e9 / float64(days)
	return math.Round(v*1e9) / 1e9
}

// meterStorage sends Polar each org's use past what's included, for every
// day measured since yesterday and not sent or skipped yet. Only orgs on a
// plan in force with the storage price are sent; the rest are recorded as
// skipped, and why.
func (s *Service) meterStorage(ctx context.Context) error {
	if !s.StorageBilling() {
		return nil
	}
	q := store.New(s.DB)
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	rows, err := q.StorageDaysToBill(ctx, pgtype.Date{Time: today.AddDate(0, 0, -storageWindow), Valid: true})
	if err != nil {
		return err
	}
	var events []Event
	var sent []store.AddStorageBillingParams
	for _, r := range rows {
		over := StorageOver(r.Bytes, int(r.Nodes))
		p := store.AddStorageBillingParams{OrgID: r.OrgID, Day: r.Day, UsedBytes: r.Bytes, OverBytes: over}
		switch {
		case over == 0:
			p.Skipped = "included"
		case r.Waived:
			p.Skipped = "waived"
		case r.Status != "active" && r.Status != "trialing":
			p.Skipped = "no_plan"
		case !r.StoragePriced:
			p.Skipped = "not_on_plan"
		default:
			org := uuid.UUID(r.OrgID.Bytes).String()
			day := r.Day.Time.Format(time.DateOnly)
			p.GbMonths = GBMonths(over, r.Day.Time)
			events = append(events, Event{
				Name: StorageEvent, Customer: org, ExternalID: StorageEvent + ":" + org + ":" + day,
				Timestamp: r.Day.Time,
				Metadata: map[string]any{
					"gb_months": p.GbMonths, "day": day, "used_bytes": r.Bytes, "over_bytes": over,
					"included_bytes": int64(r.Nodes) * hosted.IncludedBytesPerNode,
				},
			})
			sent = append(sent, p)
			continue
		}
		if err := q.AddStorageBilling(ctx, p); err != nil {
			return err
		}
	}
	if len(events) == 0 {
		return nil
	}
	// All or nothing: if Polar fails, the next sweep sends them again, and
	// Polar skips any it already has by their external IDs.
	inserted, dups, err := s.Polar.IngestEvents(ctx, events)
	if err != nil {
		return err
	}
	at := pgtype.Timestamptz{Time: s.now(), Valid: true}
	for _, p := range sent {
		p.SentAt = at
		if err := q.AddStorageBilling(ctx, p); err != nil {
			return err
		}
	}
	s.log().Info("billing: backup storage sent to Polar", "events", len(events), "new", inserted, "repeated", dups)
	return nil
}
