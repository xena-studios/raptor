// Package docker runs egg install and server containers the way Pterodactyl
// Wings does, with Raptor's isolation rules on top (docs/EGGS.md, docs/SERVERS.md).
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
)

// Labels applied to every container Wings creates. Wings only ever lists or
// touches containers carrying LabelManaged.
const (
	LabelManaged = "raptor.wings.managed"
	LabelServer  = "raptor.wings.server"
	LabelNode    = "raptor.wings.node"
	LabelRole    = "raptor.wings.role"

	RoleServer  = "server"
	RoleInstall = "install"
)

const (
	tmpfsSizeMiB   = 100
	pidLimit       = 512
	installMinMiB  = 1024
	defaultCPU     = 1024
	installCPU     = 256 // installs must not starve running servers
	installIO      = 10  // lowest I/O weight, where the I/O scheduler supports it
	slice          = "raptor.slice"
	loopbackBindIP = "127.0.0.1"
)

// Config selects the networks Wings uses. Empty subnets are picked
// automatically from free ranges when the network is first created.
type Config struct {
	NodeID         string
	Network        string
	Subnet         netip.Prefix
	InstallNetwork string
	InstallSubnet  netip.Prefix
}

// Client is the Docker implementation of containers.Runtime.
type Client struct {
	api  *client.Client
	cfg  Config
	nets containers.Networks
}

var _ containers.Runtime = (*Client)(nil)

// New connects to the local Docker daemon. Call Setup before running anything.
func New(cfg Config) (*Client, error) {
	api, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Client{api: api, cfg: cfg}, nil
}

// Setup ensures Wings' networks exist and detects cgroup placement. It's
// idempotent: existing networks are reused as they are.
func (c *Client) Setup(ctx context.Context) (containers.Networks, error) {
	info, err := c.api.Info(ctx, client.InfoOptions{})
	if err != nil {
		return containers.Networks{}, fmt.Errorf("docker info: %w", err)
	}
	var nets containers.Networks
	if info.Info.CgroupDriver == "systemd" {
		nets.CgroupParent = slice
	}
	if nets.Server, err = c.ensureNetwork(ctx, c.cfg.Network, ServerBridge, c.cfg.Subnet, true, nil); err != nil {
		return containers.Networks{}, err
	}
	if nets.Install, err = c.ensureNetwork(ctx, c.cfg.InstallNetwork, InstallBridge, c.cfg.InstallSubnet, false, []netip.Prefix{nets.Server.Subnet}); err != nil {
		return containers.Networks{}, err
	}
	c.nets = nets
	return nets, nil
}

func (c *Client) labels(serverID, role string) map[string]string {
	l := map[string]string{LabelManaged: "true", LabelServer: serverID, LabelRole: role}
	if c.cfg.NodeID != "" {
		l[LabelNode] = c.cfg.NodeID
	}
	return l
}

// Close closes the connection to Docker.
func (c *Client) Close() error { return c.api.Close() }

// EnsureImage pulls ref. If the pull fails but the image exists locally, the
// local copy is used (a registry outage shouldn't stop servers from starting).
func (c *Client) EnsureImage(ctx context.Context, ref string, progress io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	resp, err := c.api.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err == nil {
		for msg, merr := range resp.JSONMessages(ctx) {
			if merr != nil {
				err = merr
				break
			}
			if msg.Error != nil {
				err = errors.New(msg.Error.Message)
				break
			}
			if progress != nil && msg.Status != "" && msg.Progress == nil {
				_, _ = fmt.Fprintf(progress, "%s %s\n", msg.ID, msg.Status)
			}
		}
		_ = resp.Close()
	}
	if err == nil {
		return nil
	}
	if _, ierr := c.api.ImageInspect(ctx, ref); ierr == nil {
		return nil
	}
	return fmt.Errorf("pull %s: %w", ref, err)
}

// Install runs the egg's install script in a separate, short-lived container.
// Like Pterodactyl, a non-zero exit code is reported but is not treated as a
// failure; only Docker errors and timeouts are.
func (c *Client) Install(ctx context.Context, s containers.InstallSpec) (containers.InstallResult, error) {
	if c.nets.Install.Name == "" {
		return containers.InstallResult{}, errors.New("runtime not set up")
	}
	scriptDir, err := os.MkdirTemp(s.TmpDir, "install-"+s.ServerID+"-")
	if err != nil {
		return containers.InstallResult{}, err
	}
	defer func() { _ = os.RemoveAll(scriptDir) }()

	script := strings.ReplaceAll(s.Install.Script, "\r\n", "\n")
	if err := os.WriteFile(filepath.Join(scriptDir, "install.sh"), []byte(script), 0o644); err != nil { //nolint:gosec // read-only mount, readable by the container
		return containers.InstallResult{}, err
	}
	if err := os.Chmod(scriptDir, 0o755); err != nil { //nolint:gosec // mounted read-only into the install container
		return containers.InstallResult{}, err
	}

	if err := c.EnsureImage(ctx, s.Install.Container, nil); err != nil {
		return containers.InstallResult{}, err
	}

	name := "raptor-" + s.ServerID + "-install"
	if err := c.removeManaged(ctx, name); err != nil {
		return containers.InstallResult{}, err
	}

	mem := max(s.MemoryMiB, installMinMiB) * 1024 * 1024
	pids := int64(pidLimit)
	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Hostname:     "installer",
			Image:        s.Install.Container,
			Cmd:          []string{s.Install.Entrypoint, "/mnt/install/install.sh"},
			Env:          s.Env,
			Tty:          true,
			AttachStdout: true,
			AttachStderr: true,
			Labels:       c.labels(s.ServerID, RoleInstall),
		},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: s.Dir, Target: "/mnt/server"},
				{Type: mount.TypeBind, Source: scriptDir, Target: "/mnt/install", ReadOnly: true},
			},
			Tmpfs:       map[string]string{"/tmp": "rw,exec,nosuid,size=" + strconv.Itoa(tmpfsSizeMiB) + "M"},
			NetworkMode: container.NetworkMode(c.nets.Install.Name),
			SecurityOpt: []string{"no-new-privileges"},
			CapDrop:     []string{"NET_RAW", "MKNOD", "AUDIT_WRITE", "SETFCAP"},
			Resources: container.Resources{
				CgroupParent: c.nets.CgroupParent,
				Memory:       mem,
				MemorySwap:   mem,
				PidsLimit:    &pids,
				CPUShares:    installCPU,
				BlkioWeight:  installIO,
			},
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "1", "compress": "false"}},
		},
	})
	if err != nil {
		return containers.InstallResult{}, fmt.Errorf("create install container: %w", err)
	}
	defer func() { _ = c.remove(context.WithoutCancel(ctx), created.ID) }()

	if _, err := c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return containers.InstallResult{}, fmt.Errorf("start install container: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	if s.Output != nil {
		go c.follow(runCtx, created.ID, s.Output)
	}

	wait := c.api.ContainerWait(runCtx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-wait.Result:
		return containers.InstallResult{ExitCode: res.StatusCode}, nil
	case err := <-wait.Error:
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			_, _ = c.api.ContainerKill(context.WithoutCancel(ctx), created.ID, client.ContainerKillOptions{Signal: "SIGKILL"})
			return containers.InstallResult{TimedOut: true}, fmt.Errorf("install timed out after %s", s.Timeout)
		}
		return containers.InstallResult{}, err
	}
}

// Create creates (but doesn't start) the server's container.
func (c *Client) Create(ctx context.Context, s containers.ServerSpec) (string, error) {
	if c.nets.Server.Name == "" {
		return "", errors.New("runtime not set up")
	}
	if err := c.EnsureImage(ctx, s.Image, nil); err != nil {
		return "", err
	}

	ports := c.hostPorts(s.Ports)
	if err := containers.CheckPorts(ports); err != nil {
		return "", err
	}

	netMode := container.NetworkMode(c.nets.Server.Name)
	exposed := network.PortSet{}
	bindings := network.PortMap{}
	if s.HostNetwork {
		netMode = network.NetworkHost
	} else {
		for _, p := range ports {
			ip, err := netip.ParseAddr(p.IP)
			if err != nil {
				return "", fmt.Errorf("port %d: %w", p.Port, err)
			}
			for _, proto := range []string{"tcp", "udp"} {
				port, err := network.ParsePort(strconv.Itoa(p.Port) + "/" + proto)
				if err != nil {
					return "", err
				}
				exposed[port] = struct{}{}
				bindings[port] = append(bindings[port], network.PortBinding{HostIP: ip, HostPort: strconv.Itoa(p.Port)})
			}
		}
	}

	lim := s.Limits
	mem := boundedMemory(lim.MemoryMiB)
	swap := mem + lim.SwapMiB*1024*1024
	pids := lim.PIDs
	if pids <= 0 {
		pids = pidLimit
	}
	shares := lim.CPUWeight
	if shares <= 0 {
		shares = defaultCPU
	}

	name := "raptor-" + s.ServerID
	if err := c.removeManaged(ctx, name); err != nil {
		return "", err
	}

	created, err := c.api.ContainerCreate(ctx, client.ContainerCreateOptions{
		Name: name,
		Config: &container.Config{
			Hostname:     s.ServerID,
			Image:        s.Image,
			Env:          s.Env,
			User:         strconv.Itoa(s.UID) + ":" + strconv.Itoa(s.GID),
			Tty:          true,
			OpenStdin:    true,
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			ExposedPorts: exposed,
			Labels:       c.labels(s.ServerID, RoleServer),
		},
		HostConfig: &container.HostConfig{
			Mounts:         []mount.Mount{{Type: mount.TypeBind, Source: s.Dir, Target: "/home/container"}},
			Tmpfs:          map[string]string{"/tmp": "rw,exec,nosuid,size=" + strconv.Itoa(tmpfsSizeMiB) + "M"},
			PortBindings:   bindings,
			NetworkMode:    netMode,
			ReadonlyRootfs: true,
			SecurityOpt:    []string{"no-new-privileges"},
			CapDrop: []string{
				"SETPCAP", "MKNOD", "AUDIT_WRITE", "NET_RAW", "DAC_OVERRIDE",
				"FOWNER", "FSETID", "NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP",
			},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			Resources: container.Resources{
				CgroupParent:      c.nets.CgroupParent,
				Memory:            mem,
				MemoryReservation: lim.MemoryMiB * 1024 * 1024,
				MemorySwap:        swap,
				PidsLimit:         &pids,
				CPUShares:         shares,
				NanoCPUs:          lim.CPUPercent * 10_000_000,
				CpusetCpus:        lim.Cpuset,
			},
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "20m", "max-file": "3"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create server container: %w", err)
	}
	return created.ID, nil
}

// hostPorts resolves the host address each port is bound to. Like Pterodactyl,
// an allocation on 127.0.0.1 is bound to the server network's gateway
// instead: loopback on the host isn't reachable from a container, but the
// gateway is reachable from the host and other servers (e.g. a proxy) while
// still not being exposed to the internet.
func (c *Client) hostPorts(ports []containers.Port) []containers.Port {
	out := make([]containers.Port, 0, len(ports))
	for _, p := range ports {
		switch p.IP {
		case "":
			p.IP = "0.0.0.0"
		case loopbackBindIP:
			p.IP = c.nets.Server.Gateway.String()
		}
		out = append(out, p)
	}
	return out
}

// boundedMemory adds Pterodactyl's memory overhead so JVM servers aren't
// OOM-killed right at their heap limit: +15% up to 2 GiB, +10% up to 4 GiB,
// +5% above.
func boundedMemory(mib int64) int64 {
	mult := 1.05
	switch {
	case mib <= 2048:
		mult = 1.15
	case mib <= 4096:
		mult = 1.10
	}
	return int64(math.Round(float64(mib) * mult * 1024 * 1024))
}

// console is a live connection to a container's stdin and output.
type console struct {
	io.Reader
	conn io.WriteCloser
}

func (a *console) Send(cmd string) error {
	_, err := io.WriteString(a.conn, cmd+"\n")
	return err
}

func (a *console) Close() error { return a.conn.Close() }

// Attach connects to the container's stdin/stdout. Call before Start so no
// output is missed.
func (c *Client) Attach(ctx context.Context, id string) (containers.Console, error) {
	res, err := c.api.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		return nil, err
	}
	return &console{Reader: res.Reader, conn: res.Conn}, nil
}

// Start starts a created container.
func (c *Client) Start(ctx context.Context, id string) error {
	_, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// Stop stops a server the way its egg asks: a console command, a signal, or
// Docker's default stop. If it hasn't exited after timeout, it's killed.
func (c *Client) Stop(ctx context.Context, id string, a containers.Console, stop eggs.Stop, timeout time.Duration) error {
	switch {
	case stop.Command != "" && a != nil:
		if err := a.Send(stop.Command); err != nil {
			return err
		}
	case stop.Signal != 0:
		if _, err := c.api.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: signalName(stop.Signal)}); err != nil {
			return err
		}
	default:
		secs := int(timeout.Seconds())
		_, err := c.api.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &secs})
		return err
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	wait := c.api.ContainerWait(waitCtx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case <-wait.Result:
		return nil
	case <-wait.Error:
		_, err := c.api.ContainerKill(ctx, id, client.ContainerKillOptions{Signal: "SIGKILL"})
		return err
	}
}

// Wait blocks until the container stops and returns its exit code.
func (c *Client) Wait(ctx context.Context, id string) (int64, error) {
	wait := c.api.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-wait.Result:
		return res.StatusCode, nil
	case err := <-wait.Error:
		return 0, err
	}
}

// Remove force-removes a container Wings manages.
func (c *Client) Remove(ctx context.Context, id string) error { return c.removeManaged(ctx, id) }

// removeManaged force-removes the container with this name or ID if it
// exists. It refuses to touch a container Wings doesn't manage.
func (c *Client) removeManaged(ctx context.Context, nameOrID string) error {
	res, err := c.api.ContainerInspect(ctx, nameOrID, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if res.Container.Config == nil || res.Container.Config.Labels[LabelManaged] != "true" {
		return fmt.Errorf("container %s isn't managed by Wings; refusing to touch it", strings.TrimPrefix(res.Container.Name, "/"))
	}
	return c.remove(ctx, res.Container.ID)
}

// List returns every container Wings manages. Containers without the managed
// label are never listed.
func (c *Client) List(ctx context.Context) ([]containers.Container, error) {
	res, err := c.api.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", LabelManaged+"=true"),
	})
	if err != nil {
		return nil, err
	}
	out := make([]containers.Container, 0, len(res.Items))
	for _, s := range res.Items {
		var name string
		if len(s.Names) > 0 {
			name = strings.TrimPrefix(s.Names[0], "/")
		}
		out = append(out, containers.Container{
			ID:       s.ID,
			Name:     name,
			ServerID: s.Labels[LabelServer],
			Role:     s.Labels[LabelRole],
			State:    string(s.State),
		})
	}
	return out, nil
}

func (c *Client) remove(ctx context.Context, id string) error {
	_, err := c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	return err
}

func (c *Client) follow(ctx context.Context, id string, w io.Writer) {
	logs, err := c.api.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return
	}
	defer func() { _ = logs.Close() }()
	_, _ = io.Copy(w, logs)
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGABRT:
		return "SIGABRT"
	default:
		return "SIGKILL"
	}
}

// Version returns the Docker daemon's version.
func (c *Client) Version(ctx context.Context) (string, error) {
	v, err := c.api.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		return "", err
	}
	return v.Version, nil
}
