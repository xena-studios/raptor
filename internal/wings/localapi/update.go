package localapi

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/update"
)

// Updates is the self-updater (*update.Updater).
type Updates interface {
	Check(ctx context.Context, version string) (update.Plan, error)
	Apply(ctx context.Context, version, actor string) (update.Plan, error)
	Status() (update.State, error)
}

// Update checks for or installs another Wings version.
func (s *Service) Update(ctx context.Context, req *localv1.UpdateRequest) (*localv1.UpdateResponse, error) {
	if s.Updates == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, update.ErrUnsupported)
	}
	var (
		p   update.Plan
		err error
	)
	if req.GetCheck() {
		p, err = s.Updates.Check(ctx, req.GetVersion())
	} else {
		if !IsRoot(ctx) {
			return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only root can update Wings"))
		}
		// A new version is only judged healthy once the runtime is up, so
		// an update now would fail its trial for reasons of its own.
		if _, err := s.serverManager(); err != nil {
			return nil, err
		}
		p, err = s.Updates.Apply(ctx, req.GetVersion(), Caller(ctx))
	}
	switch {
	case errors.Is(err, update.ErrBusy):
		return nil, connect.NewError(connect.CodeAborted, err)
	case errors.Is(err, update.ErrUnsupported):
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	case err != nil:
		return nil, err
	}
	return &localv1.UpdateResponse{
		Current:   p.Current,
		Target:    p.Target,
		Channel:   p.Channel,
		Available: p.Available,
		Started:   p.Available && !req.GetCheck(),
	}, nil
}

func (s *Service) updateStatus() *localv1.UpdateStatus {
	if s.Updates == nil {
		return nil
	}
	st, err := s.Updates.Status()
	if err != nil {
		return &localv1.UpdateStatus{Error: "can't read the update state: " + err.Error()}
	}
	if st.Status == "" {
		return nil
	}
	out := &localv1.UpdateStatus{Status: st.Status, From: st.From, To: st.To, Error: st.Error, StartedAt: timestamppb.New(st.Started)}
	if !st.Finished.IsZero() {
		out.FinishedAt = timestamppb.New(st.Finished)
	}
	return out
}
