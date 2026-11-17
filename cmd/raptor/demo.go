package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

// demoCmd is `raptor demo [status]|on|off`: whether this machine is
// Raptor's public demo node (#449, docs/WINGS.md#demo-node), which trusts
// the Panel's demo key for guests' sandboxes. Only root on the box can turn
// it on: the Panel can't (docs/SECURITY-MODEL.md#the-demo-key).
func demoCmd(ctx context.Context, args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	var key, ports string
	var sandboxes, templates int
	c, _, err := dial("demo "+sub, args, func(fs *flag.FlagSet) {
		if sub == "on" {
			fs.StringVar(&key, "key", "", "the Panel's demo key for this node (`panel staff demo node` shows it)")
			fs.IntVar(&sandboxes, "sandboxes", 0, "sandboxes at once (default 4)")
			fs.StringVar(&ports, "ports", "", "the sandboxes' ports, from-to (default 30000-30099)")
			fs.IntVar(&templates, "templates", 0, "warm templates to keep (default 6; -1 for none)")
		}
	})
	if err != nil {
		return err
	}
	switch sub {
	case "status":
		res, err := c.GetDemo(ctx, &localv1.GetDemoRequest{})
		if err != nil {
			return rpcErr(err)
		}
		if !res.GetEnabled() {
			fmt.Println("This isn't a demo node: it doesn't trust the Panel's demo key.")
			return nil
		}
		fmt.Printf("Demo node: up to %d sandboxes on ports %d-%d, %d warm templates.\n",
			res.GetMaxSandboxes(), res.GetPortMin(), res.GetPortMax(), res.GetTemplates())
		fmt.Printf("Trusted demo key: %s\n", res.GetKey())
		if len(res.GetSandboxes()) == 0 {
			fmt.Println("No sandboxes right now.")
		}
		for _, s := range res.GetSandboxes() {
			state := "installing"
			switch {
			case s.GetWarm():
				state = "building a template"
			case s.GetReadyAt() != nil:
				state = "ready"
			}
			left := time.Until(s.GetDeadline().AsTime()).Round(time.Second)
			fmt.Printf("  %s  %-24s %-20s deleted in %s\n", s.GetServerId(), s.GetEggId(), state, left)
		}
		return nil
	case "on":
		if key == "" {
			return errors.New("usage: raptor demo on -key <base64> (run `panel staff demo node <node-id>` on the Panel for it)")
		}
		req := &localv1.SetDemoRequest{Enabled: true, Key: key, MaxSandboxes: int32(sandboxes), Templates: int32(templates)} //nolint:gosec // checked by Wings
		if ports != "" {
			from, to, ok := strings.Cut(ports, "-")
			a, errA := strconv.Atoi(from)
			b, errB := strconv.Atoi(to)
			if !ok || errA != nil || errB != nil {
				return errors.New("-ports is a range like 30000-30099")
			}
			req.PortMin, req.PortMax = int32(a), int32(b) //nolint:gosec // checked by Wings
		}
		if _, err := c.SetDemo(ctx, req); err != nil {
			return rpcErr(err)
		}
		fmt.Println("This is now Raptor's public demo node. It trusts the Panel's demo key for guests' sandboxes and the showcase servers' restores, and nothing else. Wings is restarting; servers keep running.")
		return nil
	case "off":
		if _, err := c.SetDemo(ctx, &localv1.SetDemoRequest{}); err != nil {
			return rpcErr(err)
		}
		fmt.Println("This is no longer a demo node: the demo key isn't trusted. Wings is restarting, and deletes the sandboxes left over.")
		return nil
	}
	return errors.New("usage: raptor demo [status]|on -key <base64>|off")
}
