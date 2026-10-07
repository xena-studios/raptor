package sftp

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/crypto/ssh"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
)

// PanelAuth checks logins with the Panel over the node connection. Logins
// are key-only: accounts have no passwords (docs/DECISIONS.md #198).
type PanelAuth struct {
	// Panel returns a client while the node is connected, nil otherwise.
	Panel func() nodev1connect.PanelServiceClient
}

// ErrNoPasswords is the answer to a password login.
var ErrNoPasswords = errors.New("SFTP takes SSH keys only: add yours in your Raptor account's Security settings")

// Password implements Authenticator.
func (PanelAuth) Password(context.Context, Login, string) (Grant, error) {
	return Grant{}, errors.Join(ErrDenied, ErrNoPasswords)
}

// PublicKey implements Authenticator.
func (a PanelAuth) PublicKey(ctx context.Context, l Login, key ssh.PublicKey) (Grant, error) {
	c := a.Panel()
	if c == nil {
		return Grant{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.SFTPLogin(ctx, &nodev1.SFTPLoginRequest{Username: l.Username, ServerId: l.ServerID, PublicKey: key.Marshal()})
	if err == nil {
		return Grant{UserID: res.GetUserId(), Permissions: res.GetPermissions()}, nil
	}
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeUnauthenticated, connect.CodeInvalidArgument:
		return Grant{}, ErrDenied
	}
	// Couldn't ask: key logins fall back to the cache.
	return Grant{}, errors.Join(ErrUnavailable, err)
}
