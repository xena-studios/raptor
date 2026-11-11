package storage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/mail"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Reminders before a turned-off folder's data is deleted
// (docs/PANEL.md#raptor-backup-storage): the org's owners are emailed
// when it's turned off (or its node removed), 7 days before, and the day
// before.

// reminder is one of them: its name in backup_storage.notified, and how
// long before the deletion it's due.
type reminder struct {
	name   string
	before time.Duration
}

var reminders = []reminder{
	{"off", PurgeAfter},
	{"7d", 7 * 24 * time.Hour},
	{"1d", 24 * time.Hour},
}

// due is the latest reminder due at now for data deleted at purge that
// hasn't gone out, if any. Reminders missed (the Panel down for a while)
// aren't sent late as well: only the latest one goes.
func due(notified []string, purge, now time.Time) (string, bool) {
	for i := len(reminders) - 1; i >= 0; i-- {
		r := reminders[i]
		if now.Before(purge.Add(-r.before)) {
			continue
		}
		if slices.Contains(notified, r.name) {
			return "", false
		}
		return r.name, true
	}
	return "", false
}

// remind sends the reminders due. It marks one sent before sending, so a
// failure means one missed email, never a stream of them.
func (s *Service) remind(ctx context.Context, q *store.Queries, now time.Time) error {
	if s.Mailer == nil {
		return nil
	}
	rows, err := q.BackupStorageToRemind(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		purge := r.DisabledAt.Time.Add(PurgeAfter)
		name, ok := due(r.Notified, purge, now)
		if !ok {
			continue
		}
		// This one and every earlier one: they're past.
		notified := slices.Clone(r.Notified)
		for _, x := range reminders {
			if !slices.Contains(notified, x.name) {
				notified = append(notified, x.name)
			}
			if x.name == name {
				break
			}
		}
		if err := q.SetBackupStorageNotified(ctx, store.SetBackupStorageNotifiedParams{ID: r.ID, Notified: notified}); err != nil {
			return err
		}
		s.sendReminder(ctx, q, r, name, purge)
	}
	return nil
}

func (s *Service) sendReminder(ctx context.Context, q *store.Queries, r store.BackupStorageToRemindRow, name string, purge time.Time) {
	org, err := q.OrgName(ctx, r.OrgID)
	if err != nil {
		s.log().Warn("a backup storage reminder failed", "node", uuid.UUID(r.NodeID.Bytes), "err", err)
		return
	}
	to, err := q.OrgOwnerEmails(ctx, r.OrgID)
	if err != nil {
		s.log().Warn("a backup storage reminder failed", "node", uuid.UUID(r.NodeID.Bytes), "err", err)
		return
	}
	e := reminderEmail(org, r.NodeName, r.NodeRemoved, name, purge, s.backupsURL(r.OrgID))
	for _, addr := range to {
		if err := s.Mailer.Send(ctx, addr, e); err != nil {
			s.log().Warn("a backup storage reminder failed", "node", uuid.UUID(r.NodeID.Bytes), "err", err)
		}
	}
}

func (s *Service) backupsURL(org pgtype.UUID) string {
	app := strings.TrimRight(s.AppURL, "/")
	if app == "" {
		app = "https://app.raptorpanel.net"
	}
	return app + "/orgs/" + uuid.UUID(org.Bytes).String() + "/settings/backups"
}

// reminderEmail is a reminder.
func reminderEmail(org, node string, removed bool, name string, purge time.Time, url string) mail.Email {
	if node == "" {
		node = "a node"
	}
	date := purge.UTC().Format("January 2, 2006")
	e := mail.Email{Kicker: "backup storage"}
	switch name {
	case "off":
		e.Subject = fmt.Sprintf("Raptor: %s's offsite backups are deleted on %s", node, date)
		e.Title = "Offsite backups are deleted on " + date
		if removed {
			e.Body = append(e.Body, fmt.Sprintf("%s was removed from your org %s, so its backups in Raptor Backup Storage are no longer kept up.", node, org))
		} else {
			e.Body = append(e.Body, fmt.Sprintf("Raptor Backup Storage was turned off for %s in your org %s.", node, org))
		}
		e.Body = append(e.Body, fmt.Sprintf("Its backups there are kept until %s, then deleted for good.", date))
	case "7d":
		e.Subject = fmt.Sprintf("Raptor: %s's offsite backups are deleted in 7 days", node)
		e.Title = "Offsite backups are deleted in 7 days"
		e.Body = append(e.Body, fmt.Sprintf("The backups of %s (in your org %s) in Raptor Backup Storage are deleted for good on %s.", node, org, date))
	default:
		e.Subject = fmt.Sprintf("Raptor: %s's offsite backups are deleted tomorrow", node)
		e.Title = "Offsite backups are deleted tomorrow"
		e.Body = append(e.Body, fmt.Sprintf("The backups of %s (in your org %s) in Raptor Backup Storage are deleted for good on %s.", node, org, date))
	}
	if removed {
		e.Body = append(e.Body, "To keep them, recover them onto another node before then: on that node's Backups tab, Recover backups.")
	} else {
		e.Body = append(e.Body, "To keep them, turn Raptor Backup Storage back on for the node before then (its Backups tab), or recover them onto another node from that node's Backups tab.")
	}
	e.Button = &mail.Link{Label: "Open backups", URL: url}
	e.Note = "Nothing to do if you don't need them."
	return e
}
