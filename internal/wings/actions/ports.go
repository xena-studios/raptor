package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/netcheck"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// ServerPorts is server.ports: the server's allocations, and the ports its
// game listens on inside its container, for the Panel's connection test.
const ServerPorts = "server.ports"

// PortServers is what server.ports needs from the server manager.
type PortServers interface {
	Get(ctx context.Context, id string) (*server.Server, error)
	Status(id string) (server.Status, error)
	ContainerPid(ctx context.Context, id string) (int, error)
}

// PortsReport is server.ports' answer.
type PortsReport struct {
	State       string              `json:"state"`
	Allocations []server.Allocation `json:"allocations"`
	// Listening is what the game listens on; empty while it isn't running.
	Listening []netcheck.Socket `json:"listening"`
	// Checked is whether Listening could be read (the container runs).
	Checked bool `json:"checked"`
}

// RegisterPorts adds server.ports. It isn't signed: it changes nothing.
func RegisterPorts(x *command.Executor, servers PortServers) {
	x.Register(ServerPorts, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		srv, err := servers.Get(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		st, err := servers.Status(e.ServerID)
		if err != nil {
			return nil, err
		}
		r := PortsReport{State: string(st.State), Allocations: srv.Allocations, Listening: []netcheck.Socket{}}
		pid, err := servers.ContainerPid(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		if pid > 0 {
			if socks, err := netcheck.Listening(pid); err == nil {
				r.Listening, r.Checked = socks, true
			}
		}
		return r, nil
	}})
}
