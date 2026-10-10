package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Recovering backups (docs/WINGS.md#recovering-backups): a read-only
// destination for backups this node didn't make (another node's, read with
// that node's key) or doesn't have any more (its own storage as it was at a
// point in time). The node lists what's there; those backups are restored
// like a deleted server's.

// JobScan lists a recovered destination's backups.
const JobScan = "backup.scan"

// EventRecovered: a recovered destination's backups were listed.
const EventRecovered = "backup.recovered"

// RecoverParams are what to recover: a destination (with the key of the
// backups there), or one of this node's destinations as it was.
type RecoverParams struct {
	// Destination, with RepoPassword: another node's backups.
	Destination *Destination `json:"destination,omitempty"`
	// FromDestinationID and PointInTime: this node's own destination, as
	// it was then (S3-compatible and Raptor Backup Storage keep old
	// versions).
	FromDestinationID string     `json:"from_destination_id,omitempty"`
	PointInTime       *time.Time `json:"point_in_time,omitempty"`
}

type scanPayload struct {
	DestinationID string `json:"destination_id"`
}

// Recover adds a recovered destination and queues listing its backups. It
// returns the destination's ID and the job's.
func (m *Manager) Recover(ctx context.Context, p RecoverParams, user string) (string, string, error) {
	var d Destination
	switch {
	case p.FromDestinationID != "":
		if p.PointInTime == nil || p.PointInTime.After(m.o.Now()) {
			return "", "", fmt.Errorf("%w: pick a time in the past to look back at", ErrInvalid)
		}
		r, err := m.o.Store.Read.GetBackupDestination(ctx, p.FromDestinationID)
		if err != nil {
			return "", "", ErrDestination
		}
		src, err := destFromRow(r)
		if err != nil {
			return "", "", err
		}
		if src.Type != engine.S3 && src.Type != engine.Raptor {
			return "", "", fmt.Errorf("%w: %w", ErrInvalid, engine.ErrNoPointInTime)
		}
		at := p.PointInTime.UTC().Truncate(time.Second)
		d = src
		d.Name = fmt.Sprintf("%s as of %s", src.Name, at.Format("2006-01-02 15:04 UTC"))
		d.PointInTime, d.RepoPassword = &at, src.RepoPassword // this node's own key, unless src is recovered too
	case p.Destination != nil:
		d = p.Destination.clone()
		if d.RepoPassword == "" {
			return "", "", fmt.Errorf("%w: recovering another node's backups needs that node's backup key", ErrInvalid)
		}
		if d.Type == engine.Raptor || d.Type == engine.Local {
			return "", "", fmt.Errorf("%w: another node's Raptor Backup Storage is recovered as S3-compatible storage", ErrInvalid)
		}
		d.PointInTime = nil
	default:
		return "", "", fmt.Errorf("%w: say what to recover", ErrInvalid)
	}
	d.ID, d.ReadOnly, d.Status = "", true, nil
	// Validated as the destination it reads (the read-only fields aside).
	check := d.clone()
	check.ReadOnly, check.RepoPassword, check.PointInTime = false, "", nil
	if err := m.validate(&check); err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	cfg, err := d.config()
	if err != nil {
		return "", "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", "", err
	}
	d.ID = id.String()
	now := m.o.Now().UnixMilli()
	var pit sql.NullInt64
	if d.PointInTime != nil {
		pit = sql.NullInt64{Int64: d.PointInTime.UnixMilli(), Valid: true}
	}
	var jobID string
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.InsertRecoveredDestination(ctx, store.InsertRecoveredDestinationParams{
			ID: d.ID, Name: check.Name, Type: d.Type, Config: string(cfg), RepoPassword: d.RepoPassword,
			PointInTime: pit, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		jobID, err = m.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobScan, Payload: scanPayload{DestinationID: d.ID}})
		if err != nil {
			return err
		}
		_, err = events.AppendTx(ctx, q, events.Event{Type: EventDestination, Data: map[string]any{"destination": d.Redacted(), "user": user}})
		return err
	})
	if err != nil {
		return "", "", err
	}
	m.o.Events.Wake()
	m.o.Jobs.Wake()
	return d.ID, jobID, nil
}

// scanJob lists a recovered destination's backups as recovered backups.
// Run again, it adds only ones it hasn't listed.
func (m *Manager) scanJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p scanPayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	out, err := m.run(ctx, p.DestinationID, request{Op: opList}, nil, log)
	m.recordOutcome(ctx, p.DestinationID, err)
	if err != nil {
		return nil, Explain(err)
	}
	var found []engine.Found
	if err := json.Unmarshal(out, &found); err != nil {
		return nil, err
	}
	known, err := m.o.Store.Read.DestinationSnapshots(ctx, p.DestinationID)
	if err != nil {
		return nil, err
	}
	added := 0
	err = m.o.Store.WriteTx(context.WithoutCancel(ctx), func(q *store.Queries) error {
		for _, f := range found {
			if slices.Contains(known, f.SnapshotID) {
				continue
			}
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			at := sql.NullInt64{Int64: f.At.UnixMilli(), Valid: true}
			if err := q.InsertRecoveredBackup(ctx, store.InsertRecoveredBackupParams{
				ID: id.String(), ServerID: f.ServerID, DestinationID: p.DestinationID, SnapshotID: f.SnapshotID,
				Size: f.Size, Files: f.Files, CreatedBy: hookUser, CreatedAt: f.At.UnixMilli(), FinishedAt: at,
			}); err != nil {
				return err
			}
			added++
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventRecovered, Data: map[string]any{
			"destination_id": p.DestinationID, "found": len(found), "added": added,
		}})
		return err
	})
	if err != nil {
		return nil, err
	}
	m.o.Events.Wake()
	_, _ = fmt.Fprintf(log, "found %d backups, %d new\n", len(found), added)
	return map[string]int{"found": len(found), "added": added}, nil
}

// Orphan is a backup of a server that isn't on this node: a deleted
// server's, or one recovered from another node.
type Orphan struct {
	*Backup
	DestinationName string `json:"destination_name"`
}

// Orphans lists backups of servers that aren't on this node, newest first.
// They can be restored onto one of its servers, with an owner's passkey.
func (m *Manager) Orphans(ctx context.Context) ([]Orphan, error) {
	all, err := m.List(ctx, "")
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	dests, err := m.o.Store.Read.ListBackupDestinations(ctx)
	if err != nil {
		return nil, err
	}
	for _, d := range dests {
		names[d.ID] = d.Name
	}
	var out []Orphan
	for _, b := range all {
		if b.Status != StatusOK {
			continue
		}
		if _, err := m.o.Servers.Status(b.ServerID); err == nil {
			continue
		}
		out = append(out, Orphan{Backup: b, DestinationName: names[b.DestinationID]})
	}
	return out, nil
}
