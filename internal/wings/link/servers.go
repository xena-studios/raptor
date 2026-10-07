package link

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/schedule"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Servers is the part of the server manager the mirror reads
// (*server.Manager).
type Servers interface {
	List() map[string]server.State
	Get(ctx context.Context, id string) (*server.Server, error)
	Status(id string) (server.Status, error)
}

// Schedules lists a server's schedules (*schedule.Scheduler).
type Schedules interface {
	List(ctx context.Context, serverID string) ([]*schedule.Schedule, error)
}

// Backups lists a server's backups (*backup.Manager).
type Backups interface {
	List(ctx context.Context, serverID string) ([]*backup.Backup, error)
}

// Jobs lists a server's jobs (jobs.Engine).
type Jobs interface {
	List(ctx context.Context, serverID string, limit int) ([]jobs.Job, error)
}

// jobsPerServer is how many of a server's recent jobs the Panel mirrors.
const jobsPerServer = 50

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
		if err := s.addSchedulesAndBackups(ctx, pb); err != nil {
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

func (s *service) addSchedulesAndBackups(ctx context.Context, pb *nodev1.Server) error {
	if s.l.cfg.Schedules != nil {
		if sch := s.l.cfg.Schedules(); sch != nil {
			list, err := sch.List(ctx, pb.GetId())
			if err != nil {
				return err
			}
			for _, sc := range list {
				def, err := json.Marshal(sc.Definition)
				if err != nil {
					return err
				}
				pb.Schedules = append(pb.Schedules, &nodev1.Schedule{
					Id: sc.ID, Name: sc.Name, Enabled: sc.Enabled, Version: sc.Version,
					NextRun: unixMilli(sc.NextRun), LastRun: unixMilli(sc.LastRun), Definition: def,
				})
			}
		}
	}
	if s.l.cfg.Backups != nil {
		if bk := s.l.cfg.Backups(); bk != nil {
			list, err := bk.List(ctx, pb.GetId())
			if err != nil {
				return err
			}
			for _, b := range list {
				pb.Backups = append(pb.Backups, &nodev1.Backup{
					Id: b.ID, Kind: b.Kind, Status: b.Status, Locked: b.Locked, Size: b.Size, Files: b.Files,
					DestinationId: b.DestinationID, Error: b.Error, Warning: b.Warning, CreatedBy: b.CreatedBy,
					CreatedAt: unixMilli(b.CreatedAt), FinishedAt: unixMilli(b.FinishedAt), ExpiresAt: unixMilli(b.ExpiresAt),
				})
			}
		}
	}
	if s.l.cfg.Jobs != nil {
		list, err := s.l.cfg.Jobs.List(ctx, pb.GetId(), jobsPerServer)
		if err != nil {
			return err
		}
		for _, j := range list {
			pb.Jobs = append(pb.Jobs, &nodev1.Job{
				Id: j.ID, Type: j.Type, Status: j.Status, Attempts: int32(min(j.Attempts, math.MaxInt32)), Error: j.Error, //nolint:gosec // bounded
				CreatedAt: unixMilli(j.CreatedAt), StartedAt: unixMilli(j.StartedAt), FinishedAt: unixMilli(j.FinishedAt),
			})
		}
	}
	return nil
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
