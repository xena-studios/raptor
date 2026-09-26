package localapi

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

type fakeServers struct {
	n   int
	err error
}

func (f fakeServers) ShutdownAll(context.Context) (int, error) { return f.n, f.err }

func TestShutdownServers(t *testing.T) {
	root := context.WithValue(context.Background(), rootKey{}, true)
	s := &Service{}

	_, err := s.ShutdownServers(context.Background(), &localv1.ShutdownServersRequest{})
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-root: %v", err)
	}
	_, err = s.ShutdownServers(root, &localv1.ShutdownServersRequest{})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("runtime not ready: %v", err)
	}

	s.SetServers(fakeServers{n: 2, err: errors.Join(errors.New("a: stuck"), errors.New("b: stuck"))})
	res, err := s.ShutdownServers(root, &localv1.ShutdownServersRequest{})
	if err != nil || res.GetStopped() != 2 || len(res.GetErrors()) != 2 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}
