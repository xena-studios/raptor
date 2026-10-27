package localapi

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/databases"
	"github.com/xena-studios/raptor/internal/wings/networks"
)

// Databases is the part of the database service the local API uses
// (*databases.Service).
type Databases interface {
	List(ctx context.Context) ([]databases.Instance, error)
	Usage(id string) (databases.Usage, bool)
	Check(ctx context.Context, id string) (databases.Check, error)
}

// Networks lists the owner's networks (*networks.Service).
type Networks interface {
	List(ctx context.Context) ([]networks.Network, error)
}

// SetDatabases makes the networks and database instances available, once
// the runtime is ready.
func (s *Service) SetDatabases(n Networks, d Databases) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.networks, s.databases = n, d
}

func (s *Service) databaseService() (Networks, Databases, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.databases == nil || s.networks == nil {
		return nil, nil, connect.NewError(connect.CodeUnavailable, errors.New("the container runtime isn't ready yet; see raptor-wings logs"))
	}
	return s.networks, s.databases, nil
}

func clamp32(n int) int32 { return int32(min(max(n, 0), math.MaxInt32)) } //nolint:gosec // bounded

// ListNetworks lists the owner's networks.
func (s *Service) ListNetworks(ctx context.Context, _ *localv1.ListNetworksRequest) (*localv1.ListNetworksResponse, error) {
	nets, _, err := s.databaseService()
	if err != nil {
		return nil, err
	}
	list, err := nets.List(ctx)
	if err != nil {
		return nil, err
	}
	out := &localv1.ListNetworksResponse{}
	for _, n := range list {
		out.Networks = append(out.Networks, &localv1.NetworkInfo{
			Id: n.ID, Name: n.Name, DockerName: networks.DockerName(n.ID), Subnet: n.Subnet, Ipv6Subnet: n.IPv6Subnet,
			Internet: n.Internet, Servers: clamp32(len(n.Servers)),
		})
	}
	return out, nil
}

// ListDatabaseInstances lists the database instances, and with check asks
// the running ones to answer, in parallel.
func (s *Service) ListDatabaseInstances(ctx context.Context, req *localv1.ListDatabaseInstancesRequest) (*localv1.ListDatabaseInstancesResponse, error) {
	nets, dbs, err := s.databaseService()
	if err != nil {
		return nil, err
	}
	list, err := dbs.List(ctx)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	if all, err := nets.List(ctx); err == nil {
		for _, n := range all {
			names[n.ID] = n.Name
		}
	}
	out := &localv1.ListDatabaseInstancesResponse{}
	var wg sync.WaitGroup
	for _, in := range list {
		info := &localv1.DatabaseInstanceInfo{
			Id: in.ID, Name: in.Name, Engine: in.Engine, Version: in.Version, State: in.State,
			DesiredState: in.DesiredState, Error: in.Error, Host: in.Host, Databases: clamp32(in.Databases),
			MemoryLimitBytes: in.Limits.MemoryMiB << 20, DiskLimitBytes: in.Limits.DiskMiB << 20,
		}
		for _, id := range in.Networks {
			info.Networks = append(info.Networks, &localv1.InstanceNetwork{
				Id: id, Name: names[id], DockerName: networks.DockerName(id), Address: in.Addresses[id],
			})
		}
		if u, ok := dbs.Usage(in.ID); ok && in.State == databases.StateRunning {
			info.SampledAt = timestamppb.New(u.At)
			info.MemoryBytes, info.DiskBytes = u.MemoryBytes, u.DiskBytes
			info.Connections, info.MaxConnections = clamp32(u.Connections), clamp32(u.MaxConnections)
		}
		out.Instances = append(out.Instances, info)
		if !req.GetCheck() || in.State != databases.StateRunning {
			continue
		}
		wg.Go(func() {
			c, err := dbs.Check(ctx, in.ID)
			if err != nil {
				info.CheckError = err.Error()
				return
			}
			info.Checked, info.Answering, info.CheckError = true, c.Answering, c.Error
			info.Networks = nil
			for _, n := range c.Networks {
				info.Networks = append(info.Networks, &localv1.InstanceNetwork{
					Id: n.ID, Name: names[n.ID], DockerName: n.Docker, Address: n.Address, Attached: n.Attached, ActualAddress: n.Actual,
				})
			}
		})
	}
	wg.Wait()
	sort.SliceStable(out.Instances, func(a, b int) bool { return out.Instances[a].GetName() < out.Instances[b].GetName() })
	return out, nil
}
