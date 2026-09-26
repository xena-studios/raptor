package localapi

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"

	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

// DockerVersioner reports the Docker daemon version.
type DockerVersioner interface {
	Version(ctx context.Context) (string, error)
}

// Servers is the part of the server manager the local API uses.
type Servers interface {
	ShutdownAll(ctx context.Context) (int, error)
}

// Service implements the local API.
type Service struct {
	NodeID    string
	PanelURL  string
	StartedAt time.Time
	Docker    DockerVersioner

	mu      sync.RWMutex
	servers Servers
}

// SetServers makes the server manager available (it's created once the
// container runtime is ready, which can be after the API starts).
func (s *Service) SetServers(srv Servers) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers = srv
}

func (s *Service) serverManager() (Servers, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.servers == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the container runtime isn't ready yet; see raptor-wings logs"))
	}
	return s.servers, nil
}

// ShutdownServers stops every running server for a host shutdown.
func (s *Service) ShutdownServers(ctx context.Context, _ *localv1.ShutdownServersRequest) (*localv1.ShutdownServersResponse, error) {
	if !IsRoot(ctx) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("only root can stop all servers"))
	}
	srv, err := s.serverManager()
	if err != nil {
		return nil, err
	}
	n, err := srv.ShutdownAll(ctx)
	resp := &localv1.ShutdownServersResponse{Stopped: int32(min(n, math.MaxInt32))} //nolint:gosec // bounded above
	if err != nil {
		resp.Errors = strings.Split(err.Error(), "\n")
	}
	return resp, nil
}

// GetStatus reports node health.
func (s *Service) GetStatus(ctx context.Context, _ *localv1.GetStatusRequest) (*localv1.GetStatusResponse, error) {
	resp := &localv1.GetStatusResponse{
		Version:   buildinfo.Version,
		Commit:    buildinfo.Commit,
		NodeId:    s.NodeID,
		PanelUrl:  s.PanelURL,
		StartedAt: timestamppb.New(s.StartedAt),
		Caller:    Caller(ctx),
		Docker:    &localv1.DockerStatus{},
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if v, err := s.Docker.Version(dctx); err != nil {
		resp.Docker.Error = err.Error()
	} else {
		resp.Docker.Reachable = true
		resp.Docker.Version = v
	}
	return resp, nil
}
