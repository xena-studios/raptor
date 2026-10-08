package sftp

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/crypto/ssh"
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
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	key, _ := ssh.NewPublicKey(pub)
	l := Login{Username: "alice", ServerID: "srv"}
	f := &fakePanel{res: &nodev1.SFTPLoginResponse{UserId: "u1", Permissions: []string{"sftp", "files.read"}}}
	a := PanelAuth{Panel: func() nodev1connect.PanelServiceClient { return f }}

	g, err := a.PublicKey(ctx, l, key)
	if err != nil || g.UserID != "u1" || !g.Has("files.read") || g.Has("files.write") {
		t.Fatalf("grant: %+v, %v", g, err)
	}
	if f.got.GetUsername() != "alice" || f.got.GetServerId() != "srv" || string(f.got.GetPublicKey()) != string(key.Marshal()) {
		t.Errorf("asked %v", f.got)
	}
	// The Panel saying no is a denial (the cached key is dropped)...
	f.err = connect.NewError(connect.CodePermissionDenied, errors.New("no"))
	if _, err := a.PublicKey(ctx, l, key); !errors.Is(err, ErrDenied) {
		t.Errorf("denied: %v", err)
	}
	// ...not being able to ask isn't (the cache answers).
	f.err = connect.NewError(connect.CodeUnavailable, errors.New("down"))
	if _, err := a.PublicKey(ctx, l, key); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrDenied) {
		t.Errorf("unavailable: %v", err)
	}
	disconnected := PanelAuth{Panel: func() nodev1connect.PanelServiceClient { return nil }}
	if _, err := disconnected.PublicKey(ctx, l, key); !errors.Is(err, ErrUnavailable) {
		t.Errorf("disconnected: %v", err)
	}
	// Temporary passwords: asked about every time (never cached), and the
	// grant carries their expiry.
	if _, err := a.Password(ctx, l, "hunter2"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("password while the Panel is down: %v", err)
	}
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	f.err, f.res = nil, &nodev1.SFTPLoginResponse{UserId: "u1", Permissions: []string{"sftp"}, ExpiresAt: timestamppb.New(exp)}
	g, err = a.Password(ctx, l, "hunter2")
	if err != nil || !g.ExpiresAt.Equal(exp) || f.got.GetPassword() != "hunter2" || len(f.got.GetPublicKey()) != 0 {
		t.Errorf("password: %+v, %v (asked %v)", g, err, f.got)
	}
}
