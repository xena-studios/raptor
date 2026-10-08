package commands

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// Consoles streams consoles from nodes wherever they're connected
// (*nodes.Router).
type Consoles interface {
	Console(ctx context.Context, nodeID string, envelope []byte, out func(nodes.ConsoleBatch) error) error
}

// consoleAction is what watching a console is called in grants and
// permissions (Wings' actions.ServerConsole).
const consoleAction = "server.console"

// Console streams a server's console to the signed-in user, if they may
// watch it (console.read), calling out for each batch until the node hangs
// up or ctx ends. Access is checked once, when the stream opens: callers
// reopen long streams to check it again.
func (s *Service) Console(ctx context.Context, nodeID, serverID string, out func(nodes.ConsoleBatch) error) error {
	if s.Consoles == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("consoles aren't available"))
	}
	node, err := uuid.Parse(nodeID)
	if err != nil || serverID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("which server?"))
	}
	env := nodecmd.Envelope{NodeID: node.String(), Action: consoleAction, ServerID: serverID}
	err = s.Auth.AsUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		if _, err := s.authorize(ctx, q, sess, pgtype.UUID{Bytes: node, Valid: true}, consoleAction, serverID); err != nil {
			return err
		}
		env.UserID = uuid.UUID(sess.UserID.Bytes).String()
		return nil
	})
	if err != nil {
		return err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	env.CommandID, env.ExpiresAt = id.String(), s.now().Add(DefaultLifetime).Unix()
	if err := nodecmd.SignGrant(s.PanelKey, &env); err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return s.Consoles.Console(ctx, env.NodeID, raw, out)
}

// ConsoleRecheck is how often a long console stream is reopened, which
// checks the user may still watch it.
const ConsoleRecheck = 5 * time.Minute
