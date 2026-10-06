package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/update"
)

// NodeUpdate installs a newer Wings: the Panel's staged rollouts send it
// (docs/WINGS.md#updates). Unsigned, because rollouts have no user to sign
// them; what it can do is narrow: only a release signed with the release
// key, only newer than what's running, and not at all on a node whose owner
// turned automatic updates off or pinned a version.
const NodeUpdate = "node.update"

// UpdateParams are node.update's parameters.
type UpdateParams struct {
	Version string `json:"version"` // e.g. "1.5.0"
}

// Updater is what node.update uses (*update.Updater).
type Updater interface {
	Apply(ctx context.Context, version, actor string) (update.Plan, error)
}

// UpdatePolicy is the owner's say, from config.yml.
type UpdatePolicy struct {
	Automatic bool   // updates.automatic
	Pin       string // updates.pin
	Current   string // the running version
}

// RegisterUpdates adds node.update.
func RegisterUpdates(x *command.Executor, u Updater, p UpdatePolicy) {
	x.Register(NodeUpdate, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		var params UpdateParams
		if err := decode(e, &params); err != nil {
			return nil, err
		}
		switch {
		case !p.Automatic:
			return nil, errors.New("automatic updates are off on this node (updates.automatic: false); the owner updates it with raptor update")
		case p.Pin != "":
			return nil, fmt.Errorf("this node is pinned to %s (updates.pin)", p.Pin)
		}
		want, cur := "v"+strings.TrimPrefix(params.Version, "v"), "v"+strings.TrimPrefix(p.Current, "v")
		if !semver.IsValid(want) {
			return nil, fmt.Errorf("%q isn't a version", params.Version)
		}
		if !semver.IsValid(cur) {
			return nil, fmt.Errorf("this is a development build (%s); it's updated by hand", p.Current)
		}
		if semver.Compare(want, cur) <= 0 {
			return nil, fmt.Errorf("%s isn't newer than %s; only the owner can move a node back", params.Version, p.Current)
		}
		plan, err := u.Apply(context.WithoutCancel(ctx), strings.TrimPrefix(want, "v"), "panel")
		if err != nil {
			return nil, err
		}
		return plan, nil
	}})
}
