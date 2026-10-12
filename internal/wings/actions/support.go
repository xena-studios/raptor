package actions

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/support"
)

// Support access (docs/PANEL.md#support-access): the owner grants it with
// their passkey; anyone who manages the node can end it or turn it off.
const (
	SupportGrant   = "support.grant"
	SupportRevoke  = "support.revoke"
	SupportList    = "support.list"
	SupportDisable = "support.disable"
	NodeLogs       = "node.logs"
)

// SupportRevokeParams are support.revoke's params: one grant, or all.
type SupportRevokeParams struct {
	ID  string `json:"id,omitempty"`
	All bool   `json:"all,omitempty"`
}

// SupportDisableParams are support.disable's params.
type SupportDisableParams struct {
	Off bool `json:"off"`
}

// LogsParams are node.logs' params.
type LogsParams struct {
	Lines int `json:"lines,omitempty"` // default 200, at most 2000
}

// RegisterSupport adds the support actions and node.logs. Granting is
// signed by an owner's passkey (never a delegation); ending access and
// turning it off only take access away, so they aren't.
func RegisterSupport(x *command.Executor, m *support.Manager) {
	x.Register(SupportGrant, command.Handler{Signed: command.Always, OwnerOnly: command.Always, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var g support.Grant
		if err := decode(e, &g); err != nil {
			return nil, err
		}
		return nil, m.Add(ctx, g, e.UserID)
	}})
	x.Register(SupportRevoke, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p SupportRevokeParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if p.All {
			n, err := m.RevokeAll(ctx, e.UserID)
			return map[string]int64{"revoked": n}, err
		}
		if p.ID == "" {
			return nil, errors.New("say which support access to end")
		}
		return nil, m.Revoke(ctx, p.ID, e.UserID)
	}})
	x.Register(SupportList, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, _ command.Envelope) (any, error) {
		grants, err := m.List(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"grants": grants, "disabled": m.Disabled(ctx)}, nil
	}})
	x.Register(SupportDisable, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p SupportDisableParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		return nil, m.SetDisabled(ctx, p.Off, e.UserID)
	}})
	// Wings' own log, for support (and the owner): what it did and why
	// things failed. Never a server's console or files.
	x.Register(NodeLogs, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var p LogsParams
		if err := decodeOptional(e, &p); err != nil {
			return nil, err
		}
		n := p.Lines
		if n <= 0 {
			n = 200
		}
		n = min(n, 2000)
		out, err := exec.CommandContext(ctx, "journalctl", "-u", "raptor-wings", "-n", strconv.Itoa(n), "--no-pager", "-o", "short-iso").Output() //nolint:gosec // fixed arguments
		if err != nil {
			return nil, errors.New("the Wings log couldn't be read (journalctl)")
		}
		return map[string]string{"log": string(bytes.ToValidUTF8(out, []byte("?")))}, nil
	}})
}
