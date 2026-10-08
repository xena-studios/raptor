package sftp

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"time"
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
	// ErrUnavailable: the Panel couldn't be asked, so the login fails.
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
	// ExpiresAt ends the session (a temporary password's expiry); zero for
	// none.
	ExpiresAt time.Time `json:"-"`
}

// Has reports whether the grant includes a permission.
func (g Grant) Has(perm string) bool { return slices.Contains(g.Permissions, perm) }

// Authenticator checks logins with the Panel over the node connection
// (Phase 3). Wings never stores passwords.
type Authenticator interface {
	Password(ctx context.Context, l Login, password string) (Grant, error)
}

// NoPanel is the Authenticator until the node connection exists: the Panel
// is always unreachable, so nobody logs in.
type NoPanel struct{}

// Password implements Authenticator.
func (NoPanel) Password(context.Context, Login, string) (Grant, error) {
	return Grant{}, ErrUnavailable
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
