// Package actions maps commands from the Panel to the server manager, and
// says which of them must be signed with the user's passkey
// (docs/SECURITY-MODEL.md#passkey-signed-commands).
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Action names.
const (
	ServerCreate    = "server.create"
	ServerUpdate    = "server.update"
	ServerDelete    = "server.delete"
	ServerReinstall = "server.reinstall"
	ServerStart     = "server.start"
	ServerStop      = "server.stop"
	ServerRestart   = "server.restart"
	ServerKill      = "server.kill"
	ServerCommand   = "server.command"
)

// ServerConfig is a server's configuration as sent by the Panel.
type ServerConfig struct {
	Name        string              `json:"name"`
	Egg         []byte              `json:"egg"` // the egg file (JSON or YAML)
	EggSource   string              `json:"egg_source,omitempty"`
	Image       string              `json:"image,omitempty"`
	Startup     string              `json:"startup,omitempty"`
	Variables   map[string]string   `json:"variables,omitempty"`
	Limits      containers.Limits   `json:"limits"`
	Settings    *server.Settings    `json:"settings,omitempty"`
	HostNetwork bool                `json:"host_network,omitempty"`
	Allocations []server.Allocation `json:"allocations"`
}

func (c ServerConfig) toConfig() server.Config {
	settings := server.DefaultSettings()
	if c.Settings != nil {
		settings = *c.Settings
	}
	return server.Config{
		Name: c.Name, Egg: c.Egg, EggSource: c.EggSource, Image: c.Image, Startup: c.Startup,
		Variables: c.Variables, Limits: c.Limits, Settings: settings, HostNetwork: c.HostNetwork,
		Allocations: c.Allocations,
	}
}

// CreateParams are the params of server.create.
type CreateParams struct {
	ServerConfig
	StartAfterInstall bool `json:"start_after_install,omitempty"`
}

// CommandParams are the params of server.command.
type CommandParams struct {
	Command string `json:"command"`
}

// Register adds every server action to the executor.
func Register(x *command.Executor, m *server.Manager) {
	needServer := func(e command.Envelope) error {
		if e.ServerID == "" {
			return errors.New("command needs a server_id")
		}
		return nil
	}
	power := func(fn func(context.Context, string) error) command.Handler {
		return command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
			if err := needServer(e); err != nil {
				return nil, err
			}
			return nil, fn(ctx, e.ServerID)
		}}
	}

	// Creating a server chooses an egg, which is choosing what code runs:
	// always signed.
	x.Register(ServerCreate, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p CreateParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		id, err := m.Create(ctx, p.toConfig(), server.CreateOptions{StartAfterInstall: p.StartAfterInstall})
		if err != nil {
			return nil, err
		}
		return map[string]string{"server_id": id}, nil
	}})

	// An update is signed when it changes the egg, image, or startup command.
	x.Register(ServerUpdate, command.Handler{
		Signed: func(ctx context.Context, e command.Envelope) (bool, error) {
			var p ServerConfig
			if err := decode(e, &p); err != nil {
				return false, err
			}
			if err := needServer(e); err != nil {
				return false, err
			}
			return m.ChangesCode(ctx, e.ServerID, p.toConfig())
		},
		Run: func(ctx context.Context, e command.Envelope) (any, error) {
			var p ServerConfig
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			return nil, m.Update(ctx, e.ServerID, p.toConfig())
		},
	})

	x.Register(ServerDelete, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		return nil, m.Delete(ctx, e.ServerID)
	}})

	// A plain reinstall re-runs the egg the owner already approved. ("Wipe
	// and reinstall", when it exists, will be signed.)
	x.Register(ServerReinstall, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := needServer(e); err != nil {
			return nil, err
		}
		job, err := m.Install(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		return map[string]string{"job_id": job}, nil
	}})

	x.Register(ServerStart, power(m.Start))
	x.Register(ServerStop, power(m.Stop))
	x.Register(ServerRestart, power(m.Restart))
	x.Register(ServerKill, power(m.Kill))

	x.Register(ServerCommand, command.Handler{Signed: command.Never, Run: func(_ context.Context, e command.Envelope) (any, error) {
		var p CommandParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if err := needServer(e); err != nil {
			return nil, err
		}
		return nil, m.SendCommand(e.ServerID, e.UserID, p.Command)
	}})
}

func decode(e command.Envelope, v any) error {
	if len(e.Params) == 0 {
		return errors.New("command has no params")
	}
	if err := json.Unmarshal(e.Params, v); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}
