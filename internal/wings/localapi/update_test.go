package localapi

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/update"
)

type fakeUpdates struct{ applied []string }

func (f *fakeUpdates) Check(context.Context, string) (update.Plan, error) {
	return update.Plan{Current: "1.0.0", Target: "1.1.0", Channel: "stable", Available: true}, nil
}

func (f *fakeUpdates) Apply(ctx context.Context, v, actor string) (update.Plan, error) {
	f.applied = append(f.applied, actor)
	return f.Check(ctx, v)
}

func (f *fakeUpdates) Status() (update.State, error) {
	return update.State{Status: update.StatusTrial, From: "1.0.0", To: "1.1.0"}, nil
}

func TestUpdate(t *testing.T) {
	root := context.WithValue(context.WithValue(context.Background(), rootKey{}, true), callerKey{}, "local:root")
	u := &fakeUpdates{}
	s := &Service{Updates: u}

	// Anyone with socket access can check.
	res, err := s.Update(context.Background(), &localv1.UpdateRequest{Check: true})
	if err != nil || res.GetTarget() != "1.1.0" || res.GetStarted() {
		t.Fatalf("check: %v %v", res, err)
	}
	if _, err := s.Update(context.Background(), &localv1.UpdateRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-root: %v", err)
	}
	if _, err := s.Update(root, &localv1.UpdateRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("runtime not ready: %v", err)
	}
	s.SetServers(fakeServers{})
	res, err = s.Update(root, &localv1.UpdateRequest{})
	if err != nil || !res.GetStarted() || len(u.applied) != 1 || u.applied[0] != "local:root" {
		t.Fatalf("apply: %v %v %v", res, err, u.applied)
	}
	if st := s.updateStatus(); st.GetStatus() != "trial" || st.GetTo() != "1.1.0" {
		t.Fatalf("status: %v", st)
	}
}
