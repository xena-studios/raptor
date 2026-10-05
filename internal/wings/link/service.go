package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/events"
)

// service is NodeService: what the Panel calls on the node.
type service struct {
	nodev1connect.UnimplementedNodeServiceHandler
	l *Link
}

// maxEnvelope bounds a command's JSON. File writes from the editor (up to
// 4 MiB, base64 in params) are the largest.
const maxEnvelope = 8 << 20

func (s *service) Execute(ctx context.Context, req *nodev1.ExecuteRequest) (*nodev1.ExecuteResponse, error) {
	if s.l.cfg.Commands == nil || (s.l.cfg.CommandsReady != nil && !s.l.cfg.CommandsReady()) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the node is starting up (its container runtime isn't ready)"))
	}
	if len(req.GetEnvelope()) > maxEnvelope {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("command too large"))
	}
	var e command.Envelope
	if err := json.Unmarshal(req.GetEnvelope(), &e); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("command: %w", err))
	}
	// A command runs to the end even if the connection drops while it runs:
	// the Panel's retry (same command ID) then gets the stored outcome
	// instead of finding it half done.
	res, err := s.l.cfg.Commands.Execute(context.WithoutCancel(ctx), e)
	out := &nodev1.ExecuteResponse{Result: res.Value, Duplicate: res.Duplicate}
	var run *command.RunError
	switch {
	case err == nil:
	case errors.As(err, &run):
		out.Error = run.Error()
	default:
		return nil, connect.NewError(commandCode(err), err)
	}
	return out, nil
}

// commandCode maps the executor's refusals to Connect codes.
func commandCode(err error) connect.Code {
	switch {
	case errors.Is(err, command.ErrInProgress):
		return connect.CodeAborted
	case errors.Is(err, command.ErrUnknownAction):
		return connect.CodeUnimplemented
	case errors.Is(err, command.ErrExpired):
		return connect.CodeDeadlineExceeded
	case errors.Is(err, command.ErrConflict):
		return connect.CodeAlreadyExists
	case errors.Is(err, command.ErrNotEnrolled), errors.Is(err, command.ErrNoTrustedKeys):
		return connect.CodeFailedPrecondition
	case errors.Is(err, command.ErrBadGrant), errors.Is(err, command.ErrSignatureNeeded),
		errors.Is(err, command.ErrUntrustedKey), errors.Is(err, command.ErrSignatureInvalid):
		return connect.CodePermissionDenied
	}
	return connect.CodeUnknown
}

// Events batches.
const (
	defaultEvents = 500
	maxEvents     = 1000
)

func (s *service) Events(ctx context.Context, req *nodev1.EventsRequest) (*nodev1.EventsResponse, error) {
	o := s.l.cfg.Events
	if o == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("events aren't available"))
	}
	after := req.GetAfterSeq()
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultEvents
	}
	limit = min(limit, maxEvents)
	last, err := o.Last(ctx)
	if err != nil {
		return nil, err
	}
	// The Panel being ahead of the node means the node's log restarted (a
	// state.db restored from a snapshot): its mirror needs rebuilding.
	if after > last {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("the Panel has events up to %d but the node only up to %d: a snapshot is needed", after, last))
	}
	evs, err := o.Since(ctx, after, limit)
	if errors.Is(err, events.ErrGap) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if err != nil {
		return nil, err
	}
	// Asking for events after a sequence number acknowledges everything up
	// to it.
	if err := o.Ack(ctx, after); err != nil {
		return nil, err
	}
	out := &nodev1.EventsResponse{LastSeq: last, Events: make([]*nodev1.Event, 0, len(evs))}
	for _, e := range evs {
		data := []byte("{}")
		if len(e.Data) > 0 {
			if data, err = json.Marshal(e.Data); err != nil {
				return nil, err
			}
		}
		out.Events = append(out.Events, &nodev1.Event{
			Seq: e.Seq, Type: e.Type, ServerId: e.ServerID, Version: e.Version, At: e.At.UnixMilli(), Data: data,
		})
	}
	return out, nil
}

// announceEvents tells the Panel whenever there are new events, so it pulls
// them without polling: once on connecting, then after every append
// (coalesced).
func (l *Link) announceEvents(ctx context.Context, s *nodelink.Session, panel nodev1connect.PanelServiceClient) {
	o := l.cfg.Events
	var sent int64 = -1
	for {
		changed := o.Changed()
		last, err := o.Last(ctx)
		if err == nil && last != sent {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err = panel.EventsAvailable(cctx, &nodev1.EventsAvailableRequest{LastSeq: last})
			cancel()
			if err == nil {
				sent = last
			}
		}
		retry := (<-chan time.Time)(nil)
		if err != nil {
			if ctx.Err() == nil {
				l.log.Debug("announcing events failed", "err", err)
			}
			retry = time.After(5 * time.Second)
		}
		select {
		case <-changed:
			// Let a burst of appends land before announcing.
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		case <-retry:
		case <-s.Done():
			return
		case <-ctx.Done():
			return
		}
	}
}
