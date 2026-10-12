package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/system"
)

// The machine's own settings, from the node's System tab
// (docs/WINGS.md#system-settings).
const (
	NodeSystem   = "node.system"
	NodeHostname = "node.hostname"
	NodeTime     = "node.time"
	NodeFirewall = "node.firewall"
	NodeReboot   = "node.reboot"
)

// HostnameParams are node.hostname's params.
type HostnameParams struct {
	Hostname string `json:"hostname"`
}

// TimeParams are node.time's params: either or both.
type TimeParams struct {
	Timezone string `json:"timezone,omitempty"`
	NTP      *bool  `json:"ntp,omitempty"`
}

// RegisterSystem adds the system actions. Firewall changes and reboots are
// passkey-signed: either can cut players off, or open the machine up. The
// hostname and time zone aren't: they change names, not who gets in.
func RegisterSystem(x *command.Executor, m *system.Manager) {
	x.Register(NodeSystem, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, _ command.Envelope) (any, error) {
		return m.State(ctx)
	}})
	x.Register(NodeHostname, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p HostnameParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		note, err := m.SetHostname(ctx, p.Hostname)
		if err != nil {
			return nil, err
		}
		return map[string]string{"note": note}, nil
	}})
	x.Register(NodeTime, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p TimeParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if p.Timezone == "" && p.NTP == nil {
			return nil, errors.New("say what to change: the time zone or clock sync")
		}
		return nil, m.SetTime(ctx, p.Timezone, p.NTP)
	}})
	x.Register(NodeFirewall, command.Handler{Signed: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p firewall.HostRules
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, m.SetFirewall(ctx, p)
	}})
	x.Register(NodeReboot, command.Handler{Signed: command.Always, Run: func(ctx context.Context, _ command.Envelope) (any, error) {
		return nil, m.Reboot(ctx)
	}})
}
