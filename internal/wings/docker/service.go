package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// Services are long-running containers Wings runs for itself beside game
// servers: database instances (docs/DATABASES.md). Unlike servers, Docker
// restarts them (unless-stopped), so they come back with the machine even
// before Wings does; Wings makes them match their spec at every start.

// LabelSpec holds a hash of a service's spec: a container whose hash
// differs is created again.
const LabelSpec = "raptor.wings.spec"

// specHash is a stable hash of everything in a spec.
func specHash(s containers.ServiceSpec) string {
	b, _ := json.Marshal(s) //nolint:errchkjson // plain struct
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}

// EnsureService implements containers.Runtime.
func (c *Client) EnsureService(ctx context.Context, s containers.ServiceSpec) error {
	if c.nets.Server.Name == "" {
		return fmt.Errorf("runtime not set up")
	}
	hash := specHash(s)
	res, err := c.api.ContainerInspect(ctx, s.Name, client.ContainerInspectOptions{})
	switch {
	case err == nil:
		ctr := res.Container
		if ctr.Config == nil || ctr.Config.Labels[LabelManaged] != "true" {
			return fmt.Errorf("container %s exists but wasn't created by Wings; remove or rename it", s.Name)
		}
		if ctr.Config.Labels[LabelSpec] == hash {
			if ctr.State != nil && ctr.State.Running {
				return nil
			}
			return c.Start(ctx, ctr.ID)
		}
		if err := c.remove(ctx, ctr.ID); err != nil {
			return err
		}
	case !cerrdefs.IsNotFound(err):
		return fmt.Errorf("inspect %s: %w", s.Name, err)
	}

	if err := c.EnsureImage(ctx, s.Image, nil); err != nil {
		return err
	}
	nets := s.Networks
	if len(nets) == 0 {
		nets = []containers.Network{c.nets.Server}
	}
	mounts := make([]mount.Mount, 0, len(s.Mounts))
	for _, m := range s.Mounts {
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	tmpfs := map[string]string{}
	for _, p := range s.Tmpfs {
		tmpfs[p] = "rw,nosuid,nodev,size=64M"
	}
	pids := s.PIDs
	if pids <= 0 {
		pids = 1024
	}
	labels := map[string]string{LabelManaged: "true", LabelRole: s.Role, LabelServer: s.ID, LabelSpec: hash}
	if c.cfg.NodeID != "" {
		labels[LabelNode] = c.cfg.NodeID
	}
	stop := int(s.StopTimeout.Seconds())
	if stop <= 0 {
		stop = 60
	}
	mem := s.MemoryMiB * 1024 * 1024
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: s.Name,
		Config: &container.Config{
			Image:       s.Image,
			Env:         s.Env,
			Cmd:         s.Cmd,
			Labels:      labels,
			StopTimeout: &stop,
		},
		HostConfig: &container.HostConfig{
			Mounts:         mounts,
			Tmpfs:          tmpfs,
			NetworkMode:    container.NetworkMode(nets[0].Name),
			ReadonlyRootfs: true,
			// The engines' entrypoints start as root to set up their data
			// folder, then drop to their own user; Docker's default seccomp
			// profile, not the game servers' stricter one.
			SecurityOpt:   []string{"no-new-privileges"},
			CapDrop:       []string{"NET_RAW", "MKNOD", "AUDIT_WRITE", "NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP", "SETPCAP"},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Resources: container.Resources{
				CgroupParent: c.nets.CgroupParent,
				Memory:       mem,
				MemorySwap:   mem,
				PidsLimit:    &pids,
				NanoCPUs:     s.CPUPercent * 10_000_000,
			},
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			nets[0].Name: {Aliases: s.Aliases, GwPriority: 1, IPAMConfig: pinned(s.Addresses, nets[0].Name)},
		}},
	})
	if err != nil {
		return fmt.Errorf("create %s: %w", s.Name, err)
	}
	for _, n := range nets[1:] {
		if _, err := c.api.NetworkConnect(ctx, n.Name, client.NetworkConnectOptions{
			Container: created.ID, EndpointConfig: &network.EndpointSettings{Aliases: s.Aliases, IPAMConfig: pinned(s.Addresses, n.Name)},
		}); err != nil {
			_ = c.remove(context.WithoutCancel(ctx), created.ID)
			return fmt.Errorf("connect %s to network %s: %w", s.Name, n.Name, err)
		}
	}
	return c.Start(ctx, created.ID)
}

// StopService implements containers.Runtime.
func (c *Client) StopService(ctx context.Context, name string, timeout time.Duration) error {
	secs := int(timeout.Seconds())
	_, err := c.api.ContainerStop(ctx, name, client.ContainerStopOptions{Timeout: &secs})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// RemoveService implements containers.Runtime.
func (c *Client) RemoveService(ctx context.Context, name string) error {
	return c.removeManaged(ctx, name)
}

// ServiceState implements containers.Runtime.
func (c *Client) ServiceState(ctx context.Context, name string) (containers.State, bool, error) {
	res, err := c.api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return containers.State{}, false, nil
	}
	if err != nil {
		return containers.State{}, false, err
	}
	st := containers.State{}
	if s := res.Container.State; s != nil {
		st.Running, st.ExitCode, st.OOMKilled, st.Pid = s.Running, int64(s.ExitCode), s.OOMKilled, s.Pid
		if t, err := time.Parse(time.RFC3339Nano, s.StartedAt); err == nil {
			st.StartedAt = t
		}
	}
	if ns := res.Container.NetworkSettings; ns != nil {
		for name, ep := range ns.Networks {
			if ep != nil && ep.GlobalIPv6Address.IsValid() {
				if st.IPv6 == nil {
					st.IPv6 = map[string]netip.Addr{}
				}
				st.IPv6[name] = ep.GlobalIPv6Address
			}
		}
	}
	return st, true, nil
}
