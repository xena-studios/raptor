package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/wings/command"
)

// ConsoleAction is the action a console stream's grant names (the same as
// actions.ServerConsole, which link can't import).
const ConsoleAction = "server.console"

// Console streaming: lines are batched, at most consoleBatch to a message
// and consoleFlush apart, so a busy server sends a few messages a second
// rather than one per line.
const (
	consoleBatch = 200
	consoleFlush = 50 * time.Millisecond
)

func (s *service) Console(ctx context.Context, req *nodev1.ConsoleRequest, stream *connect.ServerStream[nodev1.ConsoleResponse]) error {
	var m Servers
	if s.l.cfg.Servers != nil {
		m = s.l.cfg.Servers()
	}
	if m == nil || s.l.cfg.Commands == nil {
		return connect.NewError(connect.CodeUnavailable, errors.New("the node is starting up (its container runtime isn't ready)"))
	}
	var e command.Envelope
	if err := json.Unmarshal(req.GetEnvelope(), &e); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("console request: %w", err))
	}
	if err := s.l.cfg.Commands.AuthorizeStream(e, ConsoleAction); err != nil {
		return connect.NewError(commandCode(err), err)
	}
	st, err := m.Status(e.ServerID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	history, sub, unsubscribe := st.Console.Subscribe()
	defer unsubscribe()

	// The history, in batches; the first message is marked even if empty,
	// so the viewer knows it's caught up.
	for i := 0; i == 0 || i < len(history); i += consoleBatch {
		batch := history[i:min(i+consoleBatch, len(history))]
		if err := stream.Send(&nodev1.ConsoleResponse{Lines: batch, History: true}); err != nil {
			return err
		}
	}
	for {
		var batch []string
		select {
		case <-ctx.Done():
			return nil
		case line, ok := <-sub.C:
			if !ok {
				return nil // the server was deleted
			}
			batch = append(batch, line)
		}
		flush := time.After(consoleFlush)
	collect:
		for len(batch) < consoleBatch {
			select {
			case line, ok := <-sub.C:
				if !ok {
					break collect
				}
				batch = append(batch, line)
			case <-flush:
				break collect
			case <-ctx.Done():
				return nil
			}
		}
		if err := stream.Send(&nodev1.ConsoleResponse{Lines: batch}); err != nil {
			return err
		}
	}
}
