package command

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// Key management actions. They're always signed, and only by an owner key:
// the Panel can relay them but never make them up.
const (
	ActionKeysAdd    = "keys.add"
	ActionKeysRemove = "keys.remove"
)

func isKeyAction(a string) bool { return strings.HasPrefix(a, "keys.") }

// KeyParams are the params of keys.add.
type KeyParams struct {
	CredentialID []byte   `json:"credential_id"`
	UserID       string   `json:"user_id"`
	PublicKey    []byte   `json:"public_key"` // COSE_Key, from the passkey's registration
	Role         string   `json:"role"`       // "owner" or "delegate"
	ServerID     string   `json:"server_id,omitempty"`
	Actions      []string `json:"actions,omitempty"`    // delegates: the signed actions allowed
	ExpiresAt    int64    `json:"expires_at,omitempty"` // delegates: unix seconds, 0 = never
	Name         string   `json:"name,omitempty"`
}

// RemoveKeyParams are the params of keys.remove.
type RemoveKeyParams struct {
	CredentialID []byte `json:"credential_id"`
}

func (x *Executor) keyHandlers() map[string]Handler {
	return map[string]Handler{
		ActionKeysAdd: {Signed: Always, Run: func(ctx context.Context, e Envelope) (any, error) {
			var p KeyParams
			if err := json.Unmarshal(e.Params, &p); err != nil {
				return nil, fmt.Errorf("params: %w", err)
			}
			return nil, AddKey(ctx, x.DB, p, e.Signature.CredentialID, x.now())
		}},
		ActionKeysRemove: {Signed: Always, Run: func(ctx context.Context, e Envelope) (any, error) {
			var p RemoveKeyParams
			if err := json.Unmarshal(e.Params, &p); err != nil {
				return nil, fmt.Errorf("params: %w", err)
			}
			return nil, RemoveKey(ctx, x.DB, p.CredentialID)
		}},
	}
}

// AddKey trusts a passkey. addedBy is the credential that signed the
// addition, or nil when root pins a key locally (enrollment, `raptor keys
// reset`).
func AddKey(ctx context.Context, db *store.DB, p KeyParams, addedBy []byte, now time.Time) error {
	if len(p.CredentialID) == 0 || len(p.CredentialID) > 1023 || p.UserID == "" {
		return errors.New("key needs a credential ID and a user")
	}
	if _, err := webauthncose.ParsePublicKey(p.PublicKey); err != nil {
		return fmt.Errorf("public key: %w", err)
	}
	switch p.Role {
	case "owner":
		if p.ServerID != "" || len(p.Actions) > 0 || p.ExpiresAt != 0 {
			return errors.New("owner keys can't be scoped")
		}
	case "delegate":
		if len(p.Actions) == 0 {
			return errors.New("a delegation needs at least one action")
		}
		if slices.ContainsFunc(p.Actions, isKeyAction) {
			return errors.New("key management can't be delegated")
		}
	default:
		return fmt.Errorf("unknown role %q", p.Role)
	}
	actions, _ := json.Marshal(p.Actions)
	if p.Actions == nil {
		actions = []byte("[]")
	}
	var exp sql.NullInt64
	if p.ExpiresAt > 0 {
		exp = sql.NullInt64{Int64: p.ExpiresAt * 1000, Valid: true}
	}
	return db.Write.InsertTrustedKey(ctx, store.InsertTrustedKeyParams{
		CredentialID: p.CredentialID, UserID: p.UserID, PublicKey: p.PublicKey, Role: p.Role,
		ServerID: p.ServerID, Actions: string(actions), Name: p.Name, ExpiresAt: exp,
		AddedBy: addedBy, AddedAt: now.UnixMilli(),
	})
}

// RemoveKey stops trusting a passkey. The last owner key can't be removed
// remotely (root can reset keys on the box).
func RemoveKey(ctx context.Context, db *store.DB, credentialID []byte) error {
	return db.WriteTx(ctx, func(q *store.Queries) error {
		k, err := q.GetTrustedKey(ctx, credentialID)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("key isn't trusted")
		}
		if err != nil {
			return err
		}
		if k.Role == "owner" {
			if n, err := q.CountOwnerKeys(ctx); err != nil || n <= 1 {
				return errors.Join(ErrLastOwnerKey, err)
			}
		}
		_, err = q.DeleteTrustedKey(ctx, credentialID)
		return err
	})
}
