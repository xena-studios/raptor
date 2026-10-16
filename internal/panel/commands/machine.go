package commands

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// afterMachine keeps the Panel's copy of a machine's hostname and time zone
// (docs/PANEL.md#hostname-and-time-zone) in step with what a user's own
// command read or changed. A hostname set straight through node.hostname
// is the admin's own, so it stops following the node's name.
func (s *Service) afterMachine(ctx context.Context, node pgtype.UUID, env nodecmd.Envelope, result []byte) {
	q := store.New(s.Auth.DB)
	ctx = context.WithoutCancel(ctx)
	switch env.Action {
	case "node.system":
		var st struct {
			Hostname string `json:"hostname"`
			Timezone string `json:"timezone"`
		}
		if json.Unmarshal(result, &st) == nil && len(st.Hostname) <= 253 && len(st.Timezone) <= 64 {
			_ = q.SetNodeSystem(ctx, store.SetNodeSystemParams{ID: node, Hostname: st.Hostname, Timezone: st.Timezone})
		}
	case "node.time":
		var p struct {
			Timezone string `json:"timezone"`
		}
		if json.Unmarshal(env.Params, &p) == nil && p.Timezone != "" && len(p.Timezone) <= 64 {
			_ = q.SetNodeTimezone(ctx, store.SetNodeTimezoneParams{ID: node, Timezone: p.Timezone})
		}
	case "node.hostname":
		var p struct {
			Hostname string `json:"hostname"`
		}
		if json.Unmarshal(env.Params, &p) == nil && p.Hostname != "" && len(p.Hostname) <= 253 {
			p.Hostname = strings.ToLower(strings.TrimSpace(p.Hostname))
			if _, err := q.SetNodeHostnameCustom(ctx, store.SetNodeHostnameCustomParams{ID: node, HostnameCustom: p.Hostname}); err == nil {
				_ = q.SetNodeHostnameTried(ctx, store.SetNodeHostnameTriedParams{ID: node, Tried: p.Hostname})
			}
		}
	}
}
