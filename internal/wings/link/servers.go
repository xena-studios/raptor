package link

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Servers is the part of the server manager the mirror reads
// (*server.Manager).
type Servers interface {
	List() map[string]server.State
	Get(ctx context.Context, id string) (*server.Server, error)
	Status(id string) (server.Status, error)
}

// maxServersPerCall bounds GetServers with named IDs.
const maxServersPerCall = 1000

func (s *service) GetServers(ctx context.Context, req *nodev1.GetServersRequest) (*nodev1.GetServersResponse, error) {
	var m Servers
	if s.l.cfg.Servers != nil {
		m = s.l.cfg.Servers()
	}
	if m == nil || s.l.cfg.Events == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("the node is starting up (its container runtime isn't ready)"))
	}
	if len(req.GetIds()) > maxServersPerCall {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("too many servers in one call"))
	}
	// The sequence number first: what's read after it reflects at least
	// every event up to it.
	last, err := s.l.cfg.Events.Last(ctx)
	if err != nil {
		return nil, err
	}
	ids := req.GetIds()
	if len(ids) == 0 {
		for id := range m.List() {
			ids = append(ids, id)
		}
	}
	out := &nodev1.GetServersResponse{LastSeq: last}
	for _, id := range ids {
		srv, err := m.Get(ctx, id)
		if errors.Is(err, server.ErrNotFound) {
			out.Missing = append(out.Missing, id)
			continue
		}
		if err != nil {
			return nil, err
		}
		state := ""
		if st, err := m.Status(id); err == nil {
			state = string(st.State)
		}
		pb, err := serverProto(srv, state)
		if err != nil {
			return nil, err
		}
		out.Servers = append(out.Servers, pb)
	}
	return out, nil
}

func serverProto(s *server.Server, state string) (*nodev1.Server, error) {
	cfg, err := json.Marshal(struct {
		Image       string              `json:"image"`
		Startup     string              `json:"startup"`
		Variables   map[string]string   `json:"variables"`
		Limits      any                 `json:"limits"`
		Settings    server.Settings     `json:"settings"`
		HostNetwork bool                `json:"host_network"`
		Allocations []server.Allocation `json:"allocations"`
		EggHash     string              `json:"egg_hash"`
	}{s.Image, s.Startup, s.Variables, s.Limits, s.Settings, s.HostNetwork, s.Allocations, s.EggHash})
	if err != nil {
		return nil, err
	}
	egg := ""
	if e := s.Egg(); e != nil {
		egg = e.Name
	}
	return &nodev1.Server{
		Id: s.ID, Name: s.Name, Version: s.Version, State: state, DesiredState: s.DesiredState,
		InstallState: s.InstallState, InstallError: s.InstallError, EggName: egg, EggSource: s.EggSource,
		Config: cfg, CreatedAt: s.CreatedAt.UnixMilli(), UpdatedAt: s.UpdatedAt.UnixMilli(),
	}, nil
}
