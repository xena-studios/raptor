package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/wings/addons"
	"github.com/xena-studios/raptor/internal/wings/command"
)

// Add-on actions (docs/EGGS.md#add-ons).
const (
	AddonsList    = "addons.list"    // a read: the server's plugins, mods, or datapacks, with their hashes
	AddonsInstall = "addons.install" // download one from Modrinth, checked against its SHA-512
)

// AddonsParams are an add-on action's params.
type AddonsParams struct {
	Kind string `json:"kind"`
	addons.Install
}

// RegisterAddons adds the add-on actions. Like file writes, installing
// needs only the Panel's grant: Wings fetches only from Modrinth's CDN, and
// keeps only the file whose hash was asked for.
func RegisterAddons(x *command.Executor, s *addons.Service) {
	handler := func(readOnly bool, run func(context.Context, command.Envelope, AddonsParams) (any, error)) command.Handler {
		return command.Handler{Signed: command.Never, ReadOnly: readOnly, Run: func(ctx context.Context, e command.Envelope) (any, error) {
			if e.ServerID == "" {
				return nil, errors.New("command needs a server_id")
			}
			var p AddonsParams
			if err := decode(e, &p); err != nil {
				return nil, err
			}
			return run(ctx, e, p)
		}}
	}
	x.Register(AddonsList, handler(true, func(ctx context.Context, e command.Envelope, p AddonsParams) (any, error) {
		return s.List(ctx, e.ServerID, p.Kind)
	}))
	x.Register(AddonsInstall, handler(false, func(ctx context.Context, e command.Envelope, p AddonsParams) (any, error) {
		p.Install.Kind = p.Kind
		return s.Install(ctx, e.ServerID, e.UserID, p.Install)
	}))
}
