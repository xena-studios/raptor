package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// The owner's networks, from the node's Networks tab (docs/NETWORKS.md).
const (
	NodeNetworks      = "node.networks" // a read: the networks and Docker's others
	NodeNetworkSave   = "node.network.save"
	NodeNetworkDelete = "node.network.delete"
	// ServerNetworks puts a server on networks. Apart from server.update,
	// so who may change a server's startup can't move it between them.
	ServerNetworks = "server.networks"
)

// NetworksResult is node.networks' result.
type NetworksResult struct {
	Networks []networks.Network `json:"networks"`
	// Host is every network on the machine's Docker, Raptor's or not.
	Host []containers.HostNetwork `json:"host"`
}

// NetworkSaveParams are node.network.save's params: a network with no ID
// is created. Internet access and reaching the machine default to on.
type NetworkSaveParams struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Subnet     string `json:"subnet,omitempty"`
	Internet   *bool  `json:"internet,omitempty"`
	IPv6       bool   `json:"ipv6,omitempty"`
	MTU        int    `json:"mtu,omitempty"`
	HostAccess *bool  `json:"host_access,omitempty"`
}

// NetworkParams are node.network.delete's params.
type NetworkParams struct {
	ID string `json:"id"`
}

// ServerNetworksParams are server.networks' params: its networks, the
// primary first; none puts it back on the server network.
type ServerNetworksParams struct {
	Networks []string `json:"networks"`
}

// RegisterNetworks adds the network actions. None is signed: they delete
// no files and run no new code, and a network with servers on it can't be
// deleted. The Panel lets only admins and owners send them.
func RegisterNetworks(x *command.Executor, n *networks.Service, m *server.Manager) {
	x.Register(NodeNetworks, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, _ command.Envelope) (any, error) {
		list, err := n.List(ctx)
		if err != nil {
			return nil, err
		}
		host, err := n.Host(ctx)
		if err != nil {
			return nil, err
		}
		if host == nil {
			host = []containers.HostNetwork{}
		}
		return NetworksResult{Networks: list, Host: host}, nil
	}})
	x.Register(NodeNetworkSave, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p NetworkSaveParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return n.Save(ctx, networks.Network{
			ID: p.ID, Name: p.Name, Subnet: p.Subnet, Internet: p.Internet == nil || *p.Internet,
			IPv6: p.IPv6, MTU: p.MTU, HostAccess: p.HostAccess == nil || *p.HostAccess,
		})
	}})
	x.Register(NodeNetworkDelete, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p NetworkParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, n.Delete(ctx, p.ID)
	}})
	x.Register(ServerNetworks, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		var p ServerNetworksParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, m.SetNetworks(ctx, e.ServerID, p.Networks)
	}})
}
