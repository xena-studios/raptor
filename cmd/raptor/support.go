package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

var supportLevels = map[int32]string{1: "diagnostics", 2: "operate", 3: "manage"}

// supportCmd is `raptor support [status]|revoke <id>|--all|off|on`: Raptor
// staff's access to this node, seen and ended from the box itself, Panel
// or not (docs/PANEL.md#support-access).
func supportCmd(ctx context.Context, args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	c, pos, err := dial("support "+sub, args, nil)
	if err != nil {
		return err
	}
	switch sub {
	case "status":
		res, err := c.ListSupport(ctx, &localv1.ListSupportRequest{})
		if err != nil {
			return rpcErr(err)
		}
		if res.GetDisabled() {
			fmt.Println("Support access is off on this node: Raptor's staff can't be granted any. Turn it on with raptor support on.")
		}
		active := 0
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "ID\tSTAFF\tLEVEL\tUNTIL\tTICKET\tREASON")
		for _, g := range res.GetGrants() {
			if !g.GetActive() {
				continue
			}
			active++
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", g.GetId(), g.GetStaffEmail(), supportLevels[g.GetLevel()],
				g.GetExpiresAt().AsTime().Local().Format(time.DateTime), g.GetTicket(), g.GetReason())
		}
		if active == 0 {
			fmt.Println("No support access right now.")
			return nil
		}
		return w.Flush()
	case "revoke":
		req := &localv1.RevokeSupportRequest{}
		switch {
		case len(pos) == 1 && pos[0] == "--all", len(pos) == 1 && pos[0] == "all":
			req.All = true
		case len(pos) == 1:
			req.Id = pos[0]
		default:
			return errors.New("usage: raptor support revoke <id>|all")
		}
		res, err := c.RevokeSupport(ctx, req)
		if err != nil {
			return rpcErr(err)
		}
		fmt.Printf("Ended %d support access grant(s).\n", res.GetRevoked())
		return nil
	case "off", "on":
		if _, err := c.SetSupportEnabled(ctx, &localv1.SetSupportEnabledRequest{Enabled: sub == "on"}); err != nil {
			return rpcErr(err)
		}
		if sub == "off" {
			fmt.Println("Support access is off: every grant ended, and no new ones can be made.")
		} else {
			fmt.Println("Support access is on: Raptor's staff can ask, and you approve each time with your passkey.")
		}
		return nil
	}
	return errors.New("usage: raptor support [status]|revoke <id>|all|off|on")
}
