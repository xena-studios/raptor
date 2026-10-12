// Package support is Raptor staff's access to a node (docs/PANEL.md#support-access):
// only what the node's owner granted, with their passkey, for as long as
// they said. Staff commands name their grant (in the Panel's signed
// grant), and Wings checks each one here: the grant exists, isn't over or
// revoked, belongs to that staff member, and its level covers the action.
// Staff can never send passkey-signed actions, and there's no shell.
package support

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Levels.
const (
	Diagnostics = nodecmd.SupportDiagnostics
	Operate     = nodecmd.SupportOperate
	Manage      = nodecmd.SupportManage
)

// MaxDuration is the longest a grant lasts.
const MaxDuration = 7 * 24 * time.Hour

// disabledKey holds "1" in kv when the owner turned support access off.
const disabledKey = "support.disabled"

// LevelFor returns the lowest level that allows action, or 0 if staff may
// never use it.
func LevelFor(action string) int { return nodecmd.SupportLevel(action) }

// Errors.
var (
	ErrDisabled = errors.New("this node's owner turned support access off")
	ErrNoGrant  = errors.New("no active support access for this staff member on this node")
	ErrLevel    = errors.New("the support access granted doesn't cover this")
	ErrInvalid  = errors.New("invalid support grant")
)

// Grant is support access, as the owner signed it.
type Grant struct {
	ID         string `json:"id"`
	StaffID    string `json:"staff_id"`
	StaffEmail string `json:"staff_email"`
	Level      int    `json:"level"`
	Reason     string `json:"reason"`
	Ticket     string `json:"ticket"`
	ExpiresAt  int64  `json:"expires_at"` // unix ms
	// Set by Wings.
	GrantedBy string `json:"granted_by,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	RevokedAt int64  `json:"revoked_at,omitempty"`
	RevokedBy string `json:"revoked_by,omitempty"`
}

// Manager keeps the grants.
type Manager struct {
	DB  *store.DB
	Log *slog.Logger
	Now func() time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return m.Log
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Disabled reports whether the owner turned support access off.
func (m *Manager) Disabled(ctx context.Context) bool {
	v, err := m.DB.Read.GetKV(ctx, disabledKey)
	return err == nil && string(v) == "1"
}

// SetDisabled turns support access off (revoking every grant) or on.
func (m *Manager) SetDisabled(ctx context.Context, off bool, by string) error {
	v := "0"
	if off {
		v = "1"
		if _, err := m.RevokeAll(ctx, by); err != nil {
			return err
		}
	}
	return m.DB.Write.SetKV(ctx, store.SetKVParams{Key: disabledKey, Value: []byte(v)})
}

// Add stores a grant the owner signed. by is the owner who signed it.
func (m *Manager) Add(ctx context.Context, g Grant, by string) error {
	if m.Disabled(ctx) {
		return ErrDisabled
	}
	now := m.now()
	switch {
	case uuid.Validate(g.ID) != nil:
		return invalid("id is a UUID")
	case uuid.Validate(g.StaffID) != nil:
		return invalid("staff_id is a UUID")
	case g.StaffEmail == "" || len(g.StaffEmail) > 254:
		return invalid("staff_email is needed")
	case g.Level < Diagnostics || g.Level > Manage:
		return invalid("level is 1, 2, or 3")
	case strings.TrimSpace(g.Reason) == "" || utf8.RuneCountInString(g.Reason) > 500:
		return invalid("give a reason, up to 500 characters")
	case strings.TrimSpace(g.Ticket) == "" || utf8.RuneCountInString(g.Ticket) > 100:
		return invalid("give the ticket, up to 100 characters")
	case g.ExpiresAt <= now.UnixMilli():
		return invalid("it ends in the past")
	case time.UnixMilli(g.ExpiresAt).Sub(now) > MaxDuration+time.Minute:
		return invalid("support access lasts at most 7 days")
	}
	err := m.DB.Write.InsertSupportGrant(ctx, store.InsertSupportGrantParams{
		ID: g.ID, StaffID: g.StaffID, StaffEmail: g.StaffEmail, Level: int64(g.Level), Reason: strings.TrimSpace(g.Reason),
		Ticket: strings.TrimSpace(g.Ticket), GrantedBy: by, CreatedAt: now.UnixMilli(), ExpiresAt: g.ExpiresAt,
	})
	if err != nil {
		return err
	}
	m.log().Warn("support access granted", "grant", g.ID, "staff", g.StaffEmail, "level", g.Level,
		"until", time.UnixMilli(g.ExpiresAt).UTC().Format(time.RFC3339), "by", by, "ticket", g.Ticket)
	return nil
}

// Revoke ends a grant now.
func (m *Manager) Revoke(ctx context.Context, id, by string) error {
	n, err := m.DB.Write.RevokeSupportGrant(ctx, store.RevokeSupportGrantParams{
		RevokedAt: sql.NullInt64{Int64: m.now().UnixMilli(), Valid: true}, RevokedBy: by, ID: id,
	})
	if err == nil && n > 0 {
		m.log().Warn("support access revoked", "grant", id, "by", by)
	}
	return err
}

// RevokeAll ends every active grant.
func (m *Manager) RevokeAll(ctx context.Context, by string) (int64, error) {
	now := m.now().UnixMilli()
	n, err := m.DB.Write.RevokeAllSupportGrants(ctx, store.RevokeAllSupportGrantsParams{
		RevokedAt: sql.NullInt64{Int64: now, Valid: true}, RevokedBy: by, ExpiresAt: now,
	})
	if err == nil && n > 0 {
		m.log().Warn("all support access revoked", "grants", n, "by", by)
	}
	return n, err
}

func grantOf(r store.SupportGrant) Grant {
	g := Grant{
		ID: r.ID, StaffID: r.StaffID, StaffEmail: r.StaffEmail, Level: int(r.Level), Reason: r.Reason, Ticket: r.Ticket,
		ExpiresAt: r.ExpiresAt, GrantedBy: r.GrantedBy, CreatedAt: r.CreatedAt, RevokedBy: r.RevokedBy,
	}
	if r.RevokedAt.Valid {
		g.RevokedAt = r.RevokedAt.Int64
	}
	return g
}

// List returns the newest 50 grants, active or not.
func (m *Manager) List(ctx context.Context) ([]Grant, error) {
	rows, err := m.DB.Read.ListSupportGrants(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Grant, 0, len(rows))
	for _, r := range rows {
		out = append(out, grantOf(r))
	}
	return out, nil
}

// Active reports whether a grant is in force now.
func (g Grant) Active(now time.Time) bool {
	return g.RevokedAt == 0 && g.ExpiresAt > now.UnixMilli()
}

// Check allows a staff member's command under a grant.
func (m *Manager) Check(ctx context.Context, grantID, staffID, action string) error {
	if m.Disabled(ctx) {
		return ErrDisabled
	}
	r, err := m.DB.Read.GetSupportGrant(ctx, grantID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoGrant
	}
	if err != nil {
		return err
	}
	g := grantOf(r)
	if g.StaffID != staffID || !g.Active(m.now()) {
		return ErrNoGrant
	}
	need := LevelFor(action)
	if need == 0 || need > g.Level {
		return fmt.Errorf("%w (%s needs level %d)", ErrLevel, action, max(need, Manage+1))
	}
	return nil
}
