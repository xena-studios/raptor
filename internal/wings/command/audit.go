package command

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// keepAudit is how long the audit log is kept.
const keepAudit = 365 * 24 * time.Hour

// Audit outcomes.
const (
	AuditRunning  = "running"
	AuditOK       = "ok"
	AuditFailed   = "failed"
	AuditRejected = "rejected"
)

// AuditEntry is one signed dangerous action (run or rejected), or a local
// key reset, from the node's own records.
type AuditEntry struct {
	ID           int64
	At           time.Time
	CommandID    string
	Action       string
	ServerID     string
	UserID       string
	CredentialID []byte
	KeyName      string
	Fingerprint  string // of the signing key, if it was trusted
	CommandHash  []byte // what the passkey signed
	Outcome      string
	Detail       string
}

// KeyFingerprint identifies a passkey for people: the first 80 bits of the
// SHA-256 of its COSE public key, in base32 groups ("ABCD-EFGH-…"). The
// browser shows the same fingerprint, so owners can compare them at
// enrollment and when pairing after `raptor keys reset`.
func KeyFingerprint(coseKey []byte) string {
	sum := sha256.Sum256(coseKey)
	s := base32.StdEncoding.EncodeToString(sum[:])[:20]
	var groups []string
	for i := 0; i < len(s); i += 4 {
		groups = append(groups, s[i:i+4])
	}
	return strings.Join(groups, "-")
}

// audit records a signed command. It returns the row's ID (0 if recording
// failed, which is logged: the command itself isn't held up).
func (x *Executor) audit(ctx context.Context, e Envelope, hash []byte, outcome, detail string) int64 {
	var cred []byte
	name := ""
	if e.Signature != nil {
		cred = e.Signature.CredentialID
		if k, err := x.DB.Write.GetTrustedKey(ctx, cred); err == nil {
			name = k.Name
		}
	}
	id, err := x.DB.Write.InsertAudit(context.WithoutCancel(ctx), store.InsertAuditParams{
		At: x.now().UnixMilli(), CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, UserID: e.UserID,
		CredentialID: cred, KeyName: name, CommandHash: hash, Outcome: outcome, Detail: detail,
	})
	if err != nil {
		x.log().Error("recording a signed command in the audit log failed", "command", e.CommandID, "err", err)
		return 0
	}
	if outcome != AuditRunning {
		x.notify(ctx, id)
	}
	return id
}

func (x *Executor) finishAudit(ctx context.Context, id int64, runErr error) {
	if id == 0 {
		return
	}
	outcome, detail := AuditOK, ""
	if runErr != nil {
		outcome, detail = AuditFailed, runErr.Error()
	}
	if err := x.DB.Write.FinishAudit(context.WithoutCancel(ctx), store.FinishAuditParams{Outcome: outcome, Detail: detail, ID: id}); err != nil {
		x.log().Error("recording a signed command's outcome failed", "audit", id, "err", err)
		return
	}
	x.notify(ctx, id)
}

// notify passes a finished audit entry to OnAudit (node notifications).
func (x *Executor) notify(ctx context.Context, id int64) {
	if x.OnAudit == nil {
		return
	}
	r, err := x.DB.Write.GetAudit(context.WithoutCancel(ctx), id)
	if err != nil {
		return
	}
	x.OnAudit(auditEntry(r, fingerprints(ctx, x.DB)))
}

// ListAudit returns audit entries since a time, newest first, at most
// limit.
func ListAudit(ctx context.Context, db *store.DB, since time.Time, limit int) ([]AuditEntry, error) {
	rows, err := db.Read.ListAudit(ctx, store.ListAuditParams{Since: since.UnixMilli(), Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	fp := fingerprints(ctx, db)
	out := make([]AuditEntry, 0, len(rows))
	for _, r := range rows {
		out = append(out, auditEntry(r, fp))
	}
	return out, nil
}

// fingerprints maps trusted keys' credential IDs to their fingerprints.
func fingerprints(ctx context.Context, db *store.DB) map[string]string {
	fp := map[string]string{}
	if ks, err := db.Read.ListTrustedKeys(ctx); err == nil {
		for _, k := range ks {
			fp[string(k.CredentialID)] = KeyFingerprint(k.PublicKey)
		}
	}
	return fp
}

func auditEntry(r store.AuditLog, fp map[string]string) AuditEntry {
	return AuditEntry{
		ID: r.ID, At: time.UnixMilli(r.At), CommandID: r.CommandID, Action: r.Action, ServerID: r.ServerID,
		UserID: r.UserID, CredentialID: r.CredentialID, KeyName: r.KeyName, Fingerprint: fp[string(r.CredentialID)],
		CommandHash: r.CommandHash, Outcome: r.Outcome, Detail: r.Detail,
	}
}

// TrustedKey is a passkey this node trusts, for `raptor keys list`.
type TrustedKey struct {
	CredentialID []byte
	Fingerprint  string
	UserID       string
	Name         string
	Role         string // owner or delegate
	ServerID     string // delegates: "" = every server
	Actions      string // delegates: JSON list
	ExpiresAt    time.Time
	AddedBy      string // the fingerprint of the key that added it; "" = pinned on the box
	AddedAt      time.Time
	SignCount    int64
}

// ListKeys returns the trusted keys and delegations.
func ListKeys(ctx context.Context, db *store.DB) ([]TrustedKey, error) {
	rows, err := db.Read.ListTrustedKeys(ctx)
	if err != nil {
		return nil, err
	}
	fp := map[string]string{}
	for _, r := range rows {
		fp[string(r.CredentialID)] = KeyFingerprint(r.PublicKey)
	}
	out := make([]TrustedKey, 0, len(rows))
	for _, r := range rows {
		k := TrustedKey{
			CredentialID: r.CredentialID, Fingerprint: fp[string(r.CredentialID)], UserID: r.UserID, Name: r.Name,
			Role: r.Role, ServerID: r.ServerID, Actions: r.Actions, AddedAt: time.UnixMilli(r.AddedAt), SignCount: r.SignCount,
		}
		if r.ExpiresAt.Valid {
			k.ExpiresAt = time.UnixMilli(r.ExpiresAt.Int64)
		}
		if r.AddedBy != nil {
			k.AddedBy = fp[string(r.AddedBy)]
			if k.AddedBy == "" {
				k.AddedBy = "a key no longer trusted"
			}
		}
		out = append(out, k)
	}
	return out, nil
}
