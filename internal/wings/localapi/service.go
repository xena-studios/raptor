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
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/storage"
)

// DockerVersioner reports the Docker daemon version.
type DockerVersioner interface {
	Version(ctx context.Context) (string, error)
}

// Servers is the part of the server manager the local API uses
// (*server.Manager).
type Servers interface {
	ShutdownAll(ctx context.Context) (int, error)
	List() map[string]server.State
	Get(ctx context.Context, id string) (*server.Server, error)
	Usage(ctx context.Context, id string) (server.Usage, error)
	Power(ctx context.Context, id string, a server.PowerAction, user string) error
	Status(id string) (server.Status, error)
	SendCommand(id, user, cmd string) error
	Logs(ctx context.Context, id string, tail int, follow bool) (<-chan containers.Line, <-chan error, error)
}

// Service implements the local API.
type Service struct {
	NodeID    string
	PanelURL  string
	StartedAt time.Time
	Docker    DockerVersioner
	Storage   *storage.Volume // nil in tests

	mu      sync.RWMutex
	servers Servers
	backups Backups
	jobs    Jobs
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
	if v := s.Storage; v != nil {
		resp.Storage = &localv1.StorageStatus{Path: v.Path, Quotas: !v.Soft, Ready: true}
		if err := v.Check(); err != nil {
			resp.Storage.Ready, resp.Storage.Error = false, err.Error()
		}
	}
	s.mu.RLock()
	srv := s.servers
	s.mu.RUnlock()
	if srv != nil {
		resp.Servers = &localv1.ServerCounts{}
		for _, st := range srv.List() {
			resp.Servers.Total++
			if st == server.Starting || st == server.Running || st == server.Stopping {
				resp.Servers.Up++
			}
		}
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
