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
	// Username is the login (t-…): the whole username, or the part before
	// the dot for the older user.serverid form.
	Username string
	// ServerID is the server the username named; empty when it named
	// none, until the Panel says which server the password is for.
	ServerID string
	Addr     netip.Addr
}

// Grant is what the Panel allows a login to do.
type Grant struct {
	UserID      string   `json:"user_id"`
	Permissions []string `json:"permissions"`
	// ServerID is the server the login is for.
	ServerID string `json:"server_id"`
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

// parseUsername splits an SFTP username. The login alone ("t-…") names no
// server: the Panel says which one its password is for. The older form,
// "user.serverid", is split at its last dot: logins may contain dots,
// server IDs never do.
func parseUsername(s string) (user, server string, ok bool) {
	if s == "" {
		return "", "", false
	}
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return s, "", true
	}
	if i == 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}
