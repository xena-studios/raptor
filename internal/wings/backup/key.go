package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// The backup key is the node's repository password: every destination's
// backups are encrypted with it, and without it they can't be read
// (docs/WINGS.md#backup-key). Where copies of it live is the owner's
// choice.

// Key modes.
const (
	// KeyPanel: the Panel keeps an encrypted copy (the default), so
	// backups can be recovered on another machine if this one dies.
	KeyPanel = "panel"
	// KeyOwner: only the owner has a copy, from downloading it. Lose it and
	// a dead node's backups are lost too.
	KeyOwner = "owner"
)

// keyModeName holds the mode in kv; no row is KeyPanel.
const keyModeName = "backup.key_mode"

// EventKeyMode: the key mode changed.
const EventKeyMode = "backup.key.mode"

// ErrOwnerKey means the key isn't handed out: the owner keeps it.
var ErrOwnerKey = errors.New("this node's backup key is kept by its owner only; it isn't given to Raptor")

// KeyMode returns who keeps copies of the backup key.
func (m *Manager) KeyMode(ctx context.Context) (string, error) {
	v, err := m.o.Store.Read.GetKV(ctx, keyModeName)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyPanel, nil
	}
	if err != nil {
		return "", err
	}
	return string(v), nil
}

// SetKeyMode changes who keeps copies of the backup key.
func (m *Manager) SetKeyMode(ctx context.Context, mode string) error {
	if mode != KeyPanel && mode != KeyOwner {
		return fmt.Errorf("%w: key mode %q", ErrInvalid, mode)
	}
	err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.SetKV(ctx, store.SetKVParams{Key: keyModeName, Value: []byte(mode)}); err != nil {
			return err
		}
		_, err := events.AppendTx(ctx, q, events.Event{Type: EventKeyMode, Data: map[string]any{"mode": mode}})
		return err
	})
	if err == nil {
		m.o.Events.Wake()
	}
	return err
}

// Key is the backup key, and a fingerprint to tell copies apart.
type Key struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// KeyFingerprint identifies a key without revealing it.
func KeyFingerprint(key string) string {
	h := sha256.Sum256([]byte("raptor backup key\x00" + key))
	return hex.EncodeToString(h[:6])
}

// ExportKey returns the key for the Panel to keep, unless the owner keeps it.
func (m *Manager) ExportKey(ctx context.Context) (Key, error) {
	mode, err := m.KeyMode(ctx)
	if err != nil {
		return Key{}, err
	}
	if mode != KeyPanel {
		return Key{}, ErrOwnerKey
	}
	return m.ShowKey(ctx)
}

// ShowKey returns the key whatever the mode: for the owner to save (a
// signed command).
func (m *Manager) ShowKey(ctx context.Context) (Key, error) {
	pw, err := m.password(ctx)
	if err != nil {
		return Key{}, err
	}
	return Key{Key: pw, Fingerprint: KeyFingerprint(pw)}, nil
}
