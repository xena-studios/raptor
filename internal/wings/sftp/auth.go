package sftp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// Permissions a login needs (docs/PANEL.md#permissions): sftp to log in,
// files.read to read, files.write to change anything.
const (
	PermSFTP  = "sftp"
	PermRead  = "files.read"
	PermWrite = "files.write"
)

// Errors returned by an Authenticator.
var (
	// ErrDenied: the Panel rejected the login.
	ErrDenied = errors.New("login denied")
	// ErrUnavailable: the Panel couldn't be asked. Key logins fall back to
	// the cache; password logins fail.
	ErrUnavailable = errors.New("panel unreachable")
)

// Login is who asked to log in, and to which server.
type Login struct {
	Username string // the part before the dot
	ServerID string
	Addr     netip.Addr
}

// Grant is what the Panel allows a login to do.
type Grant struct {
	UserID      string   `json:"user_id"`
	Permissions []string `json:"permissions"`
}

// Has reports whether the grant includes a permission.
func (g Grant) Has(perm string) bool { return slices.Contains(g.Permissions, perm) }

// Authenticator checks logins with the Panel over the node connection
// (Phase 3). Wings never stores passwords.
type Authenticator interface {
	Password(ctx context.Context, l Login, password string) (Grant, error)
	PublicKey(ctx context.Context, l Login, key ssh.PublicKey) (Grant, error)
}

// NoPanel is the Authenticator until the node connection exists: the Panel
// is always unreachable, so only cached keys log in.
type NoPanel struct{}

// Password implements Authenticator.
func (NoPanel) Password(context.Context, Login, string) (Grant, error) {
	return Grant{}, ErrUnavailable
}

// PublicKey implements Authenticator.
func (NoPanel) PublicKey(context.Context, Login, ssh.PublicKey) (Grant, error) {
	return Grant{}, ErrUnavailable
}

// keyCacheMaxAge bounds how long a key the Panel hasn't confirmed keeps
// working. The Panel syncs key and permission changes while it's connected,
// so this only limits access that was revoked during a very long outage.
const keyCacheMaxAge = 30 * 24 * time.Hour

// KeyCache remembers public keys the Panel accepted, with their grants, so
// key logins keep working while the Panel is unreachable (docs/DECISIONS.md
// #29).
type KeyCache struct {
	DB  *store.DB
	Now func() time.Time // for tests
}

func (c *KeyCache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns the cached grant for a key, if the Panel confirmed it recently
// enough.
func (c *KeyCache) Get(ctx context.Context, l Login, key ssh.PublicKey) (Grant, bool, error) {
	row, err := c.DB.Read.GetSFTPKey(ctx, store.GetSFTPKeyParams{Username: l.Username, ServerID: l.ServerID, Fingerprint: ssh.FingerprintSHA256(key)})
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, false, nil
	}
	if err != nil {
		return Grant{}, false, err
	}
	// The fingerprint is a hash; compare the key itself too.
	if string(row.PublicKey) != string(key.Marshal()) {
		return Grant{}, false, nil
	}
	if c.now().Sub(time.UnixMilli(row.ConfirmedAt)) > keyCacheMaxAge {
		return Grant{}, false, nil
	}
	g := Grant{UserID: row.UserID}
	if err := json.Unmarshal([]byte(row.Permissions), &g.Permissions); err != nil {
		return Grant{}, false, fmt.Errorf("cached permissions: %w", err)
	}
	return g, true, nil
}

// Put records that the Panel accepted a key with a grant.
func (c *KeyCache) Put(ctx context.Context, l Login, key ssh.PublicKey, g Grant) error {
	perms, err := json.Marshal(g.Permissions)
	if err != nil {
		return err
	}
	return c.DB.WriteTx(ctx, func(q *store.Queries) error {
		return q.UpsertSFTPKey(ctx, store.UpsertSFTPKeyParams{
			Username: l.Username, ServerID: l.ServerID, Fingerprint: ssh.FingerprintSHA256(key),
			PublicKey: key.Marshal(), UserID: g.UserID, Permissions: string(perms),
			ConfirmedAt: c.now().UnixMilli(),
		})
	})
}

// Delete forgets a key the Panel rejected.
func (c *KeyCache) Delete(ctx context.Context, l Login, key ssh.PublicKey) error {
	return c.DB.WriteTx(ctx, func(q *store.Queries) error {
		return q.DeleteSFTPKey(ctx, store.DeleteSFTPKeyParams{Username: l.Username, ServerID: l.ServerID, Fingerprint: ssh.FingerprintSHA256(key)})
	})
}

// Prune drops keys the Panel hasn't confirmed within keyCacheMaxAge.
func (c *KeyCache) Prune(ctx context.Context) (int64, error) {
	var n int64
	err := c.DB.WriteTx(ctx, func(q *store.Queries) error {
		var err error
		n, err = q.PruneSFTPKeys(ctx, c.now().Add(-keyCacheMaxAge).UnixMilli())
		return err
	})
	return n, err
}

// parseUsername splits an SFTP username, "user.serverid", at its last dot:
// Panel usernames may contain dots, server IDs never do.
func parseUsername(s string) (user, server string, ok bool) {
	i := strings.LastIndexByte(s, '.')
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
