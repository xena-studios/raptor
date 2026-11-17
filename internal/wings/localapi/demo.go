package localapi

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	limits "github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/wings/demo"
)

// Demo is the demo node's sandboxes (*demo.Manager).
type Demo interface {
	List() []demo.Sandbox
}

// SetDemoManager makes the sandboxes visible, once the runtime is ready.
func (s *Service) SetDemoManager(d Demo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.demo = d
}

var errNoState = connect.NewError(connect.CodeUnavailable, errors.New("wings' state isn't available"))

// GetDemo implements LocalService.
func (s *Service) GetDemo(ctx context.Context, _ *localv1.GetDemoRequest) (*localv1.GetDemoResponse, error) {
	if s.State == nil {
		return nil, errNoState
	}
	c, err := demo.Load(ctx, s.State)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return &localv1.GetDemoResponse{}, nil
	}
	res := &localv1.GetDemoResponse{
		Enabled: true, Key: base64.StdEncoding.EncodeToString(c.Key),
		MaxSandboxes: int32(c.MaxSandboxes), PortMin: int32(c.PortMin), PortMax: int32(c.PortMax), //nolint:gosec // validated ranges
		Templates: int32(c.Templates), //nolint:gosec // at most 32
	}
	s.mu.RLock()
	d := s.demo
	s.mu.RUnlock()
	if d != nil {
		for _, b := range d.List() {
			sb := &localv1.DemoSandbox{
				ServerId: b.ServerID, EggId: b.EggID, Extended: b.Extended, Warm: b.Warm,
				CreatedAt: timestamppb.New(time.Unix(b.CreatedAt, 0)), Deadline: timestamppb.New(time.Unix(b.Deadline, 0)),
			}
			if b.ReadyAt != 0 {
				sb.ReadyAt = timestamppb.New(time.Unix(b.ReadyAt, 0))
			}
			res.Sandboxes = append(res.Sandboxes, sb)
		}
	}
	return res, nil
}

// SetDemo implements LocalService. Root only: trusting the Panel's demo
// key is the box owner's decision, never the Panel's
// (docs/SECURITY-MODEL.md#the-demo-key). Wings restarts to apply it.
func (s *Service) SetDemo(ctx context.Context, req *localv1.SetDemoRequest) (*localv1.SetDemoResponse, error) {
	if err := requireRoot(ctx, "make this node the demo's"); err != nil {
		return nil, err
	}
	if s.State == nil {
		return nil, errNoState
	}
	if !req.GetEnabled() {
		if err := demo.Save(ctx, s.State, nil); err != nil {
			return nil, err
		}
		s.restart()
		return &localv1.SetDemoResponse{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.GetKey()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("the demo key must be base64 (from `panel staff demo node`)"))
	}
	c := &demo.Config{
		Key: key, MaxSandboxes: int(req.GetMaxSandboxes()),
		PortMin: int(req.GetPortMin()), PortMax: int(req.GetPortMax()), Templates: int(req.GetTemplates()),
	}
	switch c.Templates {
	case 0:
		c.Templates = limits.DefaultTemplates
	case -1:
		c.Templates = 0
	}
	if err := demo.Save(ctx, s.State, c); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	s.restart()
	return &localv1.SetDemoResponse{}, nil
}

// restart restarts Wings after the answer is sent; servers keep running.
func (s *Service) restart() {
	if s.Restart == nil {
		return
	}
	time.AfterFunc(500*time.Millisecond, s.Restart)
}
