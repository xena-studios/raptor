package nodes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Router lets any Panel instance reach any node (docs/ARCHITECTURE.md#panel):
// each instance records the nodes it holds, and a command for a node another
// instance holds is forwarded to it through Postgres: the request is a row,
// LISTEN/NOTIFY says it's there and that it's answered.
type Router struct {
	DB  *pgxpool.Pool
	Hub *Hub
	// ID is this instance's (NewInstanceID).
	ID  string
	Log *slog.Logger

	mu       sync.Mutex
	waiters  map[int64]chan struct{}
	watchers map[string]*watcher // console streams watched on other instances
	serving  map[string]*served  // console streams run for other instances
	done     chan struct{}
}

// Timing. An instance that hasn't said it's alive for staleAfter is treated
// as gone, and its nodes as not connected.
const (
	heartbeat  = 5 * time.Second
	staleAfter = 20 * time.Second
	pollEvery  = 2 * time.Second
	// handleTimeout bounds running a forwarded command (a stop can take the
	// server's stop timeout, up to 10 minutes).
	handleTimeout = 15 * time.Minute
)

// NewInstanceID is a random instance ID, usable in a channel name.
func NewInstanceID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }

func channel(instance string) string { return "panel_" + instance }

func (r *Router) q() *store.Queries { return store.New(r.DB) }

// Start announces the instance and listens for forwarded requests and
// answers until ctx ends.
func (r *Router) Start(ctx context.Context) error {
	if r.Log == nil {
		r.Log = slog.New(slog.DiscardHandler)
	}
	r.waiters = map[int64]chan struct{}{}
	r.watchers = map[string]*watcher{}
	r.serving = map[string]*served{}
	r.done = make(chan struct{})
	if err := r.q().InstanceAlive(ctx, r.ID); err != nil {
		return err
	}
	conn, err := r.listen(ctx)
	if err != nil {
		return err
	}
	go r.loop(ctx, conn)
	go r.housekeeping(ctx)
	return nil
}

func (r *Router) listen(ctx context.Context) (*pgxpool.Conn, error) {
	conn, err := r.DB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel(r.ID)}.Sanitize()); err != nil {
		conn.Release()
		return nil, err
	}
	return conn, nil
}

// loop receives notifications: "req:<id>" (run a request) and "res:<id>"
// (a request of ours was answered), and console streams' (console.go). If the listening connection breaks it
// listens again; the fallback poll covers the gap.
func (r *Router) loop(ctx context.Context, conn *pgxpool.Conn) {
	defer close(r.done)
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			conn.Release()
			if ctx.Err() != nil {
				return
			}
			r.Log.Warn("panel instance: listening failed; listening again", "err", err)
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				if conn, err = r.listen(ctx); err == nil {
					break
				}
			}
			continue
		}
		kind, idStr, _ := strings.Cut(n.Payload, ":")
		if r.consoleNotification(ctx, kind, idStr) {
			continue
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		switch kind {
		case "req":
			go r.handle(ctx, id)
		case "res":
			r.wake(id)
		}
	}
}

func (r *Router) housekeeping(ctx context.Context) {
	t := time.NewTicker(heartbeat)
	defer t.Stop()
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			q := r.q()
			if err := q.InstanceAlive(ctx, r.ID); err != nil {
				r.Log.Warn("panel instance heartbeat failed", "err", err)
			}
			_ = q.PruneInstances(ctx, (10 * staleAfter).Seconds())
			_ = q.PruneNodeRequests(ctx)
		case <-poll.C:
			// Requests whose notification was missed.
			ids, err := r.q().PendingNodeRequests(ctx, r.ID)
			if err == nil {
				for _, id := range ids {
					go r.handle(ctx, id)
				}
			}
		}
	}
}

// Stop forgets this instance's nodes and the instance itself, so others stop
// forwarding to it at once instead of waiting for it to go stale.
func (r *Router) Stop(ctx context.Context) {
	q := r.q()
	_ = q.ClearInstanceConnections(ctx, r.ID)
	_ = q.InstanceGone(ctx, r.ID)
}

// Connected and Disconnected keep the record of which instance holds which
// node; call them from the hub's OnConnect and OnDisconnect.
func (r *Router) Connected(ctx context.Context, h nodelink.Hello) {
	id, err := uuid.Parse(h.NodeID)
	if err != nil {
		return
	}
	if err := r.q().SetNodeConnection(ctx, store.SetNodeConnectionParams{NodeID: pgUUID(id), InstanceID: r.ID}); err != nil {
		r.Log.Error("recording which instance holds a node", "node", h.NodeID, "err", err)
	}
}

// Disconnected: see Connected.
func (r *Router) Disconnected(ctx context.Context, nodeID string) {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return
	}
	_ = r.q().ClearNodeConnection(ctx, store.ClearNodeConnectionParams{NodeID: pgUUID(id), InstanceID: r.ID})
}

const methodExecute = "execute"

// Execute sends a command to a node wherever it's connected, retrying with
// the same command ID (Wings runs it once) until the node answers or ctx
// ends: here if this instance holds the node, otherwise through the
// instance that does.
func (r *Router) Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error) {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad node ID"))
	}
	wait := 100 * time.Millisecond
	for {
		if c, ok := r.Hub.Conn(nodeID); ok {
			res, err := c.Node.Execute(ctx, &nodev1.ExecuteRequest{Envelope: envelope})
			if err == nil || !retryable(err) {
				return res, err
			}
		} else if holder, err := r.q().NodeHolder(ctx, store.NodeHolderParams{NodeID: pgUUID(id), StaleSecs: staleAfter.Seconds()}); err == nil && holder != r.ID {
			res, err := r.forward(ctx, holder, id, envelope)
			if err == nil || !retryableCode(connect.CodeOf(err)) {
				return res, err
			}
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("node is offline"))
		}
		wait = min(wait*2, 2*time.Second)
	}
}

func retryableCode(c connect.Code) bool {
	return c == connect.CodeUnavailable || c == connect.CodeAborted || c == connect.CodeUnknown
}

// forward asks the holder to run a command and waits for its answer, giving
// up (to retry) if the holder goes away.
func (r *Router) forward(ctx context.Context, holder string, node [16]byte, envelope []byte) (*nodev1.ExecuteResponse, error) {
	q := r.q()
	reqID, err := q.CreateNodeRequest(ctx, store.CreateNodeRequestParams{
		NodeID: pgUUID(node), Origin: r.ID, Target: holder, Method: methodExecute, Request: envelope,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	ch := make(chan struct{}, 1)
	r.mu.Lock()
	r.waiters[reqID] = ch
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.waiters, reqID)
		r.mu.Unlock()
	}()
	if _, err := r.DB.Exec(ctx, "SELECT pg_notify($1, $2)", channel(holder), fmt.Sprintf("req:%d", reqID)); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		select {
		case <-ch:
		case <-t.C:
			// The answer's notification may have been missed, or the holder
			// may be gone.
			if h, err := q.NodeHolder(ctx, store.NodeHolderParams{NodeID: pgUUID(node), StaleSecs: staleAfter.Seconds()}); err != nil || h != holder {
				row, err := q.GetNodeRequest(ctx, reqID)
				if err != nil || !row.DoneAt.Valid {
					return nil, connect.NewError(connect.CodeUnavailable, errors.New("the instance holding the node went away"))
				}
			}
		case <-ctx.Done():
			return nil, connect.NewError(connect.CodeUnavailable, ctx.Err())
		}
		row, err := q.GetNodeRequest(ctx, reqID)
		if err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		if !row.DoneAt.Valid {
			continue
		}
		if row.ErrorCode != "" {
			var code connect.Code
			if err := code.UnmarshalText([]byte(row.ErrorCode)); err != nil {
				code = connect.CodeUnknown
			}
			return nil, connect.NewError(code, errors.New(row.Error))
		}
		var res nodev1.ExecuteResponse
		if err := proto.Unmarshal(row.Response, &res); err != nil {
			return nil, err
		}
		return &res, nil
	}
}

func (r *Router) wake(id int64) {
	r.mu.Lock()
	ch := r.waiters[id]
	r.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// handle runs a request forwarded to this instance, once, and answers it.
func (r *Router) handle(ctx context.Context, id int64) {
	q := r.q()
	row, err := q.TakeNodeRequest(ctx, store.TakeNodeRequestParams{ID: id, Target: r.ID})
	if err != nil {
		return // someone else's, or already taken
	}
	var resp []byte
	code, msg := "", ""
	nodeID := UUIDString(row.NodeID)
	c, ok := r.Hub.Conn(nodeID)
	switch {
	case !ok:
		code, msg = connect.CodeUnavailable.String(), "the node isn't connected to this instance any more"
	case row.Method != methodExecute:
		code, msg = connect.CodeUnimplemented.String(), "unknown method "+row.Method
	default:
		hctx, cancel := context.WithTimeout(ctx, handleTimeout)
		res, err := c.Node.Execute(hctx, &nodev1.ExecuteRequest{Envelope: row.Request})
		cancel()
		if err != nil {
			code, msg = connect.CodeOf(err).String(), err.Error()
			var ce *connect.Error
			if errors.As(err, &ce) {
				msg = ce.Message()
			}
		} else if resp, err = proto.Marshal(res); err != nil {
			code, msg = connect.CodeInternal.String(), err.Error()
		}
	}
	origin, err := q.AnswerNodeRequest(context.WithoutCancel(ctx), store.AnswerNodeRequestParams{ID: id, Response: resp, ErrorCode: code, Error: msg})
	if err != nil {
		r.Log.Error("answering a forwarded request", "id", id, "err", err)
		return
	}
	_, _ = r.DB.Exec(context.WithoutCancel(ctx), "SELECT pg_notify($1, $2)", channel(origin), fmt.Sprintf("res:%d", id))
}
