package sftp

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
)

// PanelAuth checks logins with the Panel over the node connection: the
// temporary passwords users turn on per server (docs/DECISIONS.md #222,
// #223). Accounts themselves have no passwords.
type PanelAuth struct {
	// Panel returns a client while the node is connected, nil otherwise.
	Panel func() nodev1connect.PanelServiceClient
}

// Password implements Authenticator. Passwords are only ever checked by the
// Panel: they aren't cached, so they don't work while it's unreachable.
func (a PanelAuth) Password(ctx context.Context, l Login, password string) (Grant, error) {
	return a.ask(ctx, &nodev1.SFTPLoginRequest{Username: l.Username, ServerId: l.ServerID, Password: password})
}

func (a PanelAuth) ask(ctx context.Context, req *nodev1.SFTPLoginRequest) (Grant, error) {
	c := a.Panel()
	if c == nil {
		return Grant{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := c.SFTPLogin(ctx, req)
	if err == nil {
		g := Grant{UserID: res.GetUserId(), Permissions: res.GetPermissions()}
		if res.GetExpiresAt() != nil {
			g.ExpiresAt = res.GetExpiresAt().AsTime()
		}
		return g, nil
	}
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied, connect.CodeNotFound, connect.CodeUnauthenticated, connect.CodeInvalidArgument:
		return Grant{}, ErrDenied
	}
	// Couldn't ask: the login fails.
	return Grant{}, errors.Join(ErrUnavailable, err)
}
