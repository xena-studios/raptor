package actions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/xena-studios/raptor/internal/eggs"
	limits "github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/demo"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// The public demo's actions (#449, docs/SECURITY-MODEL.md#the-demo-key).
const (
	// NodeDemo reads the demo node's state: its settings, sandboxes,
	// templates, and the library eggs that fit. Not signed; it changes
	// nothing.
	NodeDemo = "node.demo"
	// NodeDemoExtend gives a sandbox its 15 more minutes, once. Not
	// signed: Wings allows it once, so the Panel can't keep one forever.
	NodeDemoExtend = "node.demo.extend"
)

// DemoParams are server.create's demo params.
type DemoParams struct {
	// Warm builds a warm template instead of a guest's server: it
	// installs, is copied into the templates, and is deleted.
	Warm bool `json:"warm,omitempty"`
}

// DemoState is what node.demo reports.
type DemoState struct {
	Enabled      bool              `json:"enabled"`
	MaxSandboxes int               `json:"max_sandboxes,omitempty"`
	PortMin      int               `json:"port_min,omitempty"`
	PortMax      int               `json:"port_max,omitempty"`
	Sandboxes    []demo.Sandbox    `json:"sandboxes"`
	Fingerprints []string          `json:"fingerprints"`
	Templates    map[string]string `json:"templates"` // key: egg
}

// DemoNode is what RegisterDemo needs on a demo node; nil Manager means
// the node isn't one, and every demo key check refuses (as the executor
// already does without a demo key).
type DemoNode struct {
	Manager   *demo.Manager
	Templates *demo.Templates
	// Network is the demo network sandboxes go on, whose outbound
	// traffic the demo's egress policy limits.
	Network string
}

var errNotDemo = errors.New("this node isn't a demo node")

// RegisterDemo lets the demo key sign the three things the demo needs,
// and nothing else: creating a sandbox from a library egg within the
// sandbox limits, deleting a sandbox, and restoring a showcase server's
// locked backup. It adds the checks to the handlers Register and
// RegisterBackups made (call it after them), and the node.demo actions.
func RegisterDemo(x *command.Executor, m *server.Manager, b *backup.Manager, d DemoNode) {
	handlers := x.Handlers()

	create := handlers[ServerCreate]
	create.Demo = func(ctx context.Context, e command.Envelope) error {
		if d.Manager == nil {
			return errNotDemo
		}
		var p CreateParams
		if err := decode(e, &p); err != nil {
			return err
		}
		_, err := checkSandbox(d.Manager, e, p)
		if err == nil {
			err = d.Manager.Room()
		}
		return err
	}
	normal := create.Run
	create.Run = func(ctx context.Context, e command.Envelope) (any, error) {
		var p CreateParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if len(e.Demo) == 0 {
			if p.Demo != nil {
				return nil, errors.New("demo params are only taken with the demo key")
			}
			return normal(ctx, e)
		}
		return createSandbox(ctx, m, d, e, p)
	}
	x.Register(ServerCreate, create)

	del := handlers[ServerDelete]
	del.Demo = func(_ context.Context, e command.Envelope) error {
		if d.Manager == nil {
			return errNotDemo
		}
		var p DeleteParams
		if err := decodeOptional(e, &p); err != nil {
			return err
		}
		if !d.Manager.IsSandbox(e.ServerID) {
			return demo.ErrNotBox
		}
		if p.FinalBackup {
			return errors.New("sandboxes aren't backed up")
		}
		return nil
	}
	plainDelete := del.Run
	del.Run = func(ctx context.Context, e command.Envelope) (any, error) {
		res, err := plainDelete(ctx, e)
		if err == nil && d.Manager != nil {
			d.Manager.Removed(ctx, e.ServerID)
		}
		return res, err
	}
	x.Register(ServerDelete, del)

	restore := handlers[BackupRestore]
	restore.Demo = func(ctx context.Context, e command.Envelope) error {
		if d.Manager == nil || b == nil {
			return errNotDemo
		}
		var p BackupParams
		if err := decode(e, &p); err != nil {
			return err
		}
		if e.ServerID == "" || p.BackupID == "" {
			return errors.New("needs a server and a backup")
		}
		if d.Manager.IsSandbox(e.ServerID) {
			return errors.New("sandboxes aren't restored")
		}
		bk, err := b.Get(ctx, e.ServerID, p.BackupID)
		if err != nil {
			return err
		}
		if !bk.Locked || bk.Status != backup.StatusOK {
			return errors.New("only a showcase server's locked, finished backup can be restored with the demo key")
		}
		return nil
	}
	x.Register(BackupRestore, restore)

	// A guest may change a sandbox's settings (memory, variables), never
	// past the sandbox limits or off its port.
	if update, ok := handlers[ServerUpdate]; ok {
		plain := update.Run
		update.Run = func(ctx context.Context, e command.Envelope) (any, error) {
			if d.Manager != nil && d.Manager.IsSandbox(e.ServerID) {
				var p ServerUpdateParams
				if err := decode(e, &p); err != nil {
					return nil, err
				}
				if err := checkSandboxUpdate(ctx, m, e.ServerID, p); err != nil {
					return nil, err
				}
			}
			return plain(ctx, e)
		}
		x.Register(ServerUpdate, update)
	}

	x.Register(NodeDemo, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(context.Context, command.Envelope) (any, error) {
		if d.Manager == nil {
			return DemoState{Sandboxes: []demo.Sandbox{}, Fingerprints: []string{}, Templates: map[string]string{}}, nil
		}
		c := d.Manager.Config()
		return DemoState{
			Enabled: true, MaxSandboxes: c.MaxSandboxes, PortMin: c.PortMin, PortMax: c.PortMax,
			Sandboxes: d.Manager.List(), Fingerprints: d.Manager.Fingerprints(), Templates: d.Templates.List(),
		}, nil
	}})
	x.Register(NodeDemoExtend, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if d.Manager == nil {
			return nil, errNotDemo
		}
		return d.Manager.Extend(ctx, e.ServerID)
	}})
}

// checkSandbox checks a demo-signed server.create is a sandbox the demo
// node may make: a library egg that fits, on this node's architecture,
// with its own image and startup command, the sandbox limits, one port in
// the sandbox range, and nothing else (no host network, no disk or network
// of its own, no backups, no settings).
func checkSandbox(d *demo.Manager, e command.Envelope, p CreateParams) (eggID string, err error) {
	if e.ServerID != "" {
		return "", errors.New("a new server has no ID yet")
	}
	egg, err := eggs.Parse(p.Egg)
	if err != nil {
		return "", err
	}
	lib, ok := d.Egg(eggs.CodeFingerprint(egg))
	if !ok {
		return "", errors.New("the egg isn't one of the library's eggs that fit a sandbox on this node (a newer egg needs a newer Wings here)")
	}
	if p.Image != "" && !slices.ContainsFunc(egg.Images, func(i eggs.Image) bool { return i.Ref == p.Image }) {
		return "", errors.New("the image isn't one of the egg's")
	}
	if p.Startup != "" && !slices.ContainsFunc(egg.Startup, func(s eggs.StartupCommand) bool { return s.Command == p.Startup }) {
		return "", errors.New("the startup command isn't one of the egg's")
	}
	if err := checkSandboxLimits(p.Limits); err != nil {
		return "", err
	}
	c := d.Config()
	if len(p.Allocations) != 1 {
		return "", errors.New("a sandbox has exactly one port")
	}
	if a := p.Allocations[0]; (a.IP != "" && a.IP != "0.0.0.0") || a.Port < c.PortMin || a.Port > c.PortMax || !a.Primary {
		return "", fmt.Errorf("a sandbox's port is one from %d to %d, on every address", c.PortMin, c.PortMax)
	}
	switch {
	case p.HostNetwork:
		return "", errors.New("sandboxes can't use the host's network")
	case p.Settings != nil:
		return "", errors.New("sandboxes take the default settings")
	case p.Disk != "" || len(p.Networks) > 0:
		return "", errors.New("sandboxes go on the main volume and the demo network")
	case p.BackupSchedule != nil && *p.BackupSchedule:
		return "", errors.New("sandboxes aren't backed up")
	}
	return lib.ID, nil
}

// checkSandboxLimits refuses limits above a sandbox's.
func checkSandboxLimits(l containers.Limits) error {
	switch {
	case l.MemoryMiB > limits.MemoryMiB || l.SwapMiB > 0:
		return fmt.Errorf("a sandbox has at most %d MiB of memory and no swap", limits.MemoryMiB)
	case l.CPUPercent > limits.CPUPercent:
		return fmt.Errorf("a sandbox has at most %d%% CPU", limits.CPUPercent)
	case l.DiskMiB > limits.DiskMiB:
		return fmt.Errorf("a sandbox has at most %d MiB of disk", limits.DiskMiB)
	case l.Cpuset != "" || l.PIDs > 512:
		return errors.New("sandboxes take the default CPU set and process limit")
	}
	return nil
}

// sandboxLimits are what every sandbox gets, whatever was asked (within
// the limits): it's capped even if the Panel asked for less of a cap.
func sandboxLimits(asked containers.Limits) containers.Limits {
	l := containers.Limits{MemoryMiB: limits.MemoryMiB, CPUPercent: limits.CPUPercent, DiskMiB: limits.DiskMiB}
	if asked.MemoryMiB > 0 {
		l.MemoryMiB = asked.MemoryMiB
	}
	if asked.CPUPercent > 0 {
		l.CPUPercent = asked.CPUPercent
	}
	if asked.DiskMiB > 0 {
		l.DiskMiB = asked.DiskMiB
	}
	return l
}

// createSandbox makes a guest's sandbox (or builds a template): from the
// warm template if there's one, by installing otherwise.
func createSandbox(ctx context.Context, m *server.Manager, d DemoNode, e command.Envelope, p CreateParams) (any, error) {
	eggID, err := checkSandbox(d.Manager, e, p)
	if err != nil {
		return nil, err
	}
	release, err := d.Manager.Reserve()
	if err != nil {
		return nil, err
	}
	defer release()
	cfg := p.toConfig()
	cfg.Limits = sandboxLimits(p.Limits)
	cfg.Settings = server.DefaultSettings()
	cfg.HostNetwork, cfg.Disk = false, ""
	if d.Network != "" {
		cfg.Networks = []string{d.Network}
	}
	egg, _ := eggs.Parse(p.Egg)
	image := p.Image
	if image == "" {
		image = egg.DefaultImage()
	}
	warm := p.Demo != nil && p.Demo.Warm
	key := demo.TemplateKey(eggs.CodeFingerprint(egg), image, p.Variables, p.AcceptEULA)
	opts := server.CreateOptions{StartAfterInstall: !warm, AcceptEULA: p.AcceptEULA}
	fromTemplate := !warm && d.Templates.Has(key)
	if fromTemplate {
		opts.Import = func(ctx context.Context, dir string) error {
			if err := d.Templates.CopyTo(ctx, key, dir); err != nil {
				return err
			}
			if p.AcceptEULA {
				return os.WriteFile(filepath.Join(dir, "eula.txt"), []byte("eula=true\n"), 0o644) //nolint:gosec // the server reads it
			}
			return nil
		}
	}
	id, err := m.Create(ctx, cfg, opts)
	if err != nil {
		return nil, err
	}
	if err := d.Manager.Add(ctx, demo.Sandbox{
		ServerID: id, EggID: eggID, Fingerprint: eggs.CodeFingerprint(egg), Template: key, Warm: warm,
	}); err != nil {
		return nil, errors.Join(err, m.Delete(context.WithoutCancel(ctx), id))
	}
	if fromTemplate {
		// An import leaves the server installed and stopped, before the
		// sandbox was recorded: its 15 minutes start now.
		d.Manager.Ready(ctx, id)
		go func() { _ = m.Start(context.WithoutCancel(ctx), id) }()
	}
	return map[string]any{"server_id": id, "from_template": fromTemplate}, nil
}

// checkSandboxUpdate refuses a sandbox update past the sandbox limits, onto
// the host's network, or off its port.
func checkSandboxUpdate(ctx context.Context, m *server.Manager, id string, p ServerUpdateParams) error {
	if p.Reinstall != nil {
		return errors.New("sandboxes aren't reinstalled; make a new one")
	}
	if err := checkSandboxLimits(p.Limits); err != nil {
		return err
	}
	if p.Limits.MemoryMiB == 0 || p.Limits.CPUPercent == 0 || p.Limits.DiskMiB == 0 {
		return errors.New("a sandbox keeps a memory, CPU, and disk limit")
	}
	if p.HostNetwork {
		return errors.New("sandboxes can't use the host's network")
	}
	cur, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	if !slices.Equal(cur.Allocations, p.Allocations) {
		return errors.New("a sandbox keeps its port")
	}
	return nil
}
