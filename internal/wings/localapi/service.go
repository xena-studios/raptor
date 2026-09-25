package localapi

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
)

// DockerVersioner reports the Docker daemon version.
type DockerVersioner interface {
	Version(ctx context.Context) (string, error)
}

// Service implements the local API.
type Service struct {
	NodeID    string
	PanelURL  string
	StartedAt time.Time
	Docker    DockerVersioner
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
