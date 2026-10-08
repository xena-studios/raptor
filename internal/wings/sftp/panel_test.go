package sftp

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
)

type fakePanel struct {
	nodev1connect.UnimplementedPanelServiceHandler
	res *nodev1.SFTPLoginResponse
	err error
	got *nodev1.SFTPLoginRequest
}

func (f *fakePanel) EventsAvailable(context.Context, *nodev1.EventsAvailableRequest) (*nodev1.EventsAvailableResponse, error) {
	return nil, nil
}

func (f *fakePanel) SFTPLogin(_ context.Context, req *nodev1.SFTPLoginRequest) (*nodev1.SFTPLoginResponse, error) {
	f.got = req
	return f.res, f.err
}

func TestPanelAuth(t *testing.T) {
	ctx := context.Background()
	l := Login{Username: "alice", ServerID: "srv"}
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	f := &fakePanel{res: &nodev1.SFTPLoginResponse{UserId: "u1", Permissions: []string{"sftp", "files.read"}, ExpiresAt: timestamppb.New(exp)}}
	a := PanelAuth{Panel: func() nodev1connect.PanelServiceClient { return f }}

	// The Panel checks the password every time; the grant carries its
	// expiry.
	g, err := a.Password(ctx, l, "hunter2")
	if err != nil || g.UserID != "u1" || !g.Has("files.read") || g.Has("files.write") || !g.ExpiresAt.Equal(exp) {
		t.Fatalf("grant: %+v, %v", g, err)
	}
	if f.got.GetUsername() != "alice" || f.got.GetServerId() != "srv" || f.got.GetPassword() != "hunter2" {
		t.Errorf("asked %v", f.got)
	}
	// The Panel saying no is a denial; not being able to ask isn't, but the
	// login fails all the same.
	f.err = connect.NewError(connect.CodePermissionDenied, errors.New("no"))
	if _, err := a.Password(ctx, l, "hunter2"); !errors.Is(err, ErrDenied) {
		t.Errorf("denied: %v", err)
	}
	f.err = connect.NewError(connect.CodeUnavailable, errors.New("down"))
	if _, err := a.Password(ctx, l, "hunter2"); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrDenied) {
		t.Errorf("unavailable: %v", err)
	}
	disconnected := PanelAuth{Panel: func() nodev1connect.PanelServiceClient { return nil }}
	if _, err := disconnected.Password(ctx, l, "hunter2"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("disconnected: %v", err)
	}
}
