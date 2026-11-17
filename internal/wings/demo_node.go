package wings

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"

	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/actions"
	"github.com/xena-studios/raptor/internal/wings/demo"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// The public demo's node (#449, docs/WINGS.md#demo-node).

// demoNetworkName is the network the demo's sandboxes go on.
const demoNetworkName = "Demo sandboxes"

// extraCapabilities are this node's capabilities beyond its build's.
func (r *runtimeSetup) extraCapabilities() []string {
	if r.demoCfg != nil {
		return []string{nodelink.CapDemo}
	}
	return nil
}

// demoNetwork makes sure the demo network exists on the demo node, and
// returns its ID ("" on every other node, or if it couldn't be made:
// sandboxes are refused then, rather than run without the egress policy).
func (r *runtimeSetup) demoNetwork(ctx context.Context, nsvc *networks.Service) string {
	if r.demoCfg == nil {
		return ""
	}
	id := demo.Network(ctx, r.db)
	if list, err := nsvc.List(ctx); err == nil {
		for _, n := range list {
			if n.ID == id {
				return id
			}
		}
	}
	n, err := nsvc.Save(ctx, networks.Network{Name: demoNetworkName, Internet: true})
	if err != nil {
		r.log.Error("demo: making the sandboxes' network failed; sandboxes are refused until it exists", "err", err)
		return ""
	}
	if err := demo.SetNetwork(ctx, r.db, n.ID); err != nil {
		r.log.Error("demo: recording the sandboxes' network failed", "err", err)
	}
	return n.ID
}

// startDemo starts the demo node's sandboxes, their deadlines, the warm
// templates, and the egress policy's allowed hosts. On every other node it
// deletes sandboxes left from when it was one (`raptor demo off`) and
// returns an empty DemoNode, which refuses the demo key.
func (r *runtimeSetup) startDemo(ctx context.Context, mgr *server.Manager, network string, uid, gid int) actions.DemoNode {
	if r.demoCfg == nil {
		r.cleanUpDemo(ctx, mgr)
		return actions.DemoNode{}
	}
	if network == "" {
		// No network, no egress policy: no sandboxes.
		return actions.DemoNode{}
	}
	// Setup is retried until it succeeds; the sandboxes start once.
	if r.demoNode != nil {
		return *r.demoNode
	}
	tpl := &demo.Templates{Dir: filepath.Join(r.cfg.Paths.Volumes, "demo-templates"), UID: uid, GID: gid, Log: r.log}
	dm, err := demo.New(ctx, demo.Options{Config: *r.demoCfg, DB: r.db, Servers: mgr, Templates: tpl, Log: r.log})
	if err != nil {
		r.log.Error("demo: starting the sandboxes failed; they're refused", "err", err)
		return actions.DemoNode{}
	}
	go dm.Run(ctx)
	go demo.RunEgress(ctx, nil, r.log, r.applyDemoEgress)
	r.svc.SetDemoManager(dm)
	r.demoNode = &actions.DemoNode{Manager: dm, Templates: tpl, Network: network}
	return *r.demoNode
}

// cleanUpDemo deletes the sandboxes a former demo node still has.
func (r *runtimeSetup) cleanUpDemo(ctx context.Context, mgr *server.Manager) {
	dm, err := demo.New(ctx, demo.Options{DB: r.db, Servers: mgr, Log: r.log})
	if err != nil {
		return
	}
	for _, s := range dm.List() {
		if err := mgr.Delete(ctx, s.ServerID); err != nil {
			r.log.Error("deleting a sandbox left from the demo failed", "server", s.ServerID, "err", err)
			continue
		}
		dm.Removed(ctx, s.ServerID)
		r.log.Info("deleted a sandbox left from the demo", "server", s.ServerID)
	}
}

// applyDemoEgress loads the allowed hosts' addresses into the demo's
// egress policy.
func (r *runtimeSetup) applyDemoEgress(ctx context.Context, allow []netip.Addr) error {
	r.fwMu.Lock()
	defer r.fwMu.Unlock()
	if r.rules == nil || r.rules.Demo == nil {
		return errors.New("the firewall isn't loaded yet")
	}
	next := *r.rules
	d := *next.Demo
	d.Allow = allow
	next.Demo = &d
	if err := firewall.Apply(ctx, next); err != nil {
		return err
	}
	r.rules = &next
	return nil
}
