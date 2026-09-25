package localapi

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

type fakeDocker struct{ err error }

func (f fakeDocker) Version(context.Context) (string, error) { return "29.0.0", f.err }

func TestGetStatusOverSocket(t *testing.T) {
	// macOS limits Unix socket paths to 104 bytes; t.TempDir() can exceed it.
	dir, err := os.MkdirTemp("", "wsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "wings.sock")

	log := slog.New(slog.DiscardHandler)
	srv, err := Listen(context.Background(), path, "", &Service{NodeID: "node1", StartedAt: time.Now(), Docker: fakeDocker{}}, log)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o007 != 0 {
		t.Errorf("socket is accessible to others: %v", perm)
	}

	resp, err := Dial(path).GetStatus(context.Background(), &localv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetNodeId() != "node1" || !resp.GetDocker().GetReachable() {
		t.Fatalf("resp = %v", resp)
	}
	if runtime.GOOS == "linux" && resp.GetCaller() == "local:unknown" {
		t.Errorf("caller not attributed via SO_PEERCRED: %q", resp.GetCaller())
	}
}

func TestGetStatusDockerDown(t *testing.T) {
	s := &Service{Docker: fakeDocker{err: errors.New("cannot connect")}}
	resp, err := s.GetStatus(context.Background(), &localv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetDocker().GetReachable() || resp.GetDocker().GetError() == "" {
		t.Fatalf("docker = %v", resp.GetDocker())
	}
}
