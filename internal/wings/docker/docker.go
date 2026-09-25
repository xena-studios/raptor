// Package docker runs egg install and server containers the way Pterodactyl
// Wings does, with Raptor's isolation rules on top (docs/EGGS.md, docs/SERVERS.md).
package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/eggs"
)

// Labels applied to every container Wings creates. Wings only ever lists or
// touches containers carrying LabelManaged.
const (
	LabelManaged = "raptor.wings.managed"
	LabelServer  = "raptor.wings.server"
	LabelRole    = "raptor.wings.role"

	RoleServer  = "server"
	RoleInstall = "install"
)

const (
	tmpfsSizeMiB  = 100
	pidLimit      = 512
	installMinMiB = 1024
)

// Client wraps the Docker API client.
type Client struct {
	api *client.Client
}

// New connects to the local Docker daemon.
func New() (*Client, error) {
	api, err := client.New(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{api: api}, nil
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

// InstallSpec describes one egg install run.
type InstallSpec struct {
	ServerID  string
	Dir       string // the server's data directory on the host
	TmpDir    string // parent for the per-install script directory
	Install   eggs.Install
	Env       []string
	MemoryMiB int64
	UID, GID  int
	Network   string
	Timeout   time.Duration
	Output    io.Writer // receives the script's output
}

// InstallResult reports how the install script ended.
type InstallResult struct {
	ExitCode int64
	TimedOut bool
}

// Install runs the egg's install script in a separate, short-lived container.
// Like Pterodactyl, a non-zero exit code is reported but is not treated as a
// failure; only Docker errors and timeouts are.
func (c *Client) Install(ctx context.Context, s InstallSpec) (InstallResult, error) {
	scriptDir, err := os.MkdirTemp(s.TmpDir, "install-"+s.ServerID+"-")
	if err != nil {
		return InstallResult{}, err
	}
	defer os.RemoveAll(scriptDir)

	script := strings.ReplaceAll(s.Install.Script, "\r\n", "\n")
	if err := os.WriteFile(filepath.Join(scriptDir, "install.sh"), []byte(script), 0o644); err != nil { //nolint:gosec // read-only mount, readable by the container
		return InstallResult{}, err
	}
	if err := os.Chmod(scriptDir, 0o755); err != nil { //nolint:gosec // mounted read-only into the install container
		return InstallResult{}, err
	}

	if err := c.EnsureImage(ctx, s.Install.Container, nil); err != nil {
		return InstallResult{}, err
	}

	name := "raptor-" + s.ServerID + "-install"
	_ = c.remove(ctx, name)

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
			Labels: map[string]string{
				LabelManaged: "true",
				LabelServer:  s.ServerID,
				LabelRole:    RoleInstall,
			},
		},
		HostConfig: &container.HostConfig{
			Mounts: []mount.Mount{
				{Type: mount.TypeBind, Source: s.Dir, Target: "/mnt/server"},
				{Type: mount.TypeBind, Source: scriptDir, Target: "/mnt/install", ReadOnly: true},
			},
			Tmpfs:       map[string]string{"/tmp": "rw,exec,nosuid,size=" + strconv.Itoa(tmpfsSizeMiB) + "M"},
			NetworkMode: container.NetworkMode(s.Network),
			SecurityOpt: []string{"no-new-privileges"},
			CapDrop:     []string{"NET_RAW", "MKNOD", "AUDIT_WRITE", "SETFCAP"},
			Resources: container.Resources{
				Memory:     mem,
				MemorySwap: mem,
				PidsLimit:  &pids,
				CPUShares:  256, // installs must not starve running servers
			},
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "10m", "max-file": "1", "compress": "false"}},
		},
	})
	if err != nil {
		return InstallResult{}, fmt.Errorf("create install container: %w", err)
	}
	defer func() { _ = c.remove(context.WithoutCancel(ctx), created.ID) }()

	if _, err := c.api.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return InstallResult{}, fmt.Errorf("start install container: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	if s.Output != nil {
		go c.follow(runCtx, created.ID, s.Output)
	}

	wait := c.api.ContainerWait(runCtx, created.ID, client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case res := <-wait.Result:
		return InstallResult{ExitCode: res.StatusCode}, nil
	case err := <-wait.Error:
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			_, _ = c.api.ContainerKill(context.WithoutCancel(ctx), created.ID, client.ContainerKillOptions{Signal: "SIGKILL"})
			return InstallResult{TimedOut: true}, fmt.Errorf("install timed out after %s", s.Timeout)
		}
		return InstallResult{}, err
	}
}

// FixOwnership hands every file in dir to uid:gid after an install. It uses
// lchown through os.Root, so it never follows symlinks and never leaves dir.
func FixOwnership(dir string, uid, gid int) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return root.Lchown(path, uid, gid)
	})
}

// Port is a published port (TCP and UDP).
type Port struct {
	IP   string
	Port int
}

// ServerSpec describes a server container.
type ServerSpec struct {
	ServerID  string
	Dir       string
	Image     string
	Env       []string
	MemoryMiB int64
	UID, GID  int
	Network   string
	Ports     []Port
}

// CreateServer creates (but doesn't start) the server's container.
func (c *Client) CreateServer(ctx context.Context, s ServerSpec) (string, error) {
	if err := c.EnsureImage(ctx, s.Image, nil); err != nil {
		return "", err
	}

	exposed := network.PortSet{}
	bindings := network.PortMap{}
	for _, p := range s.Ports {
		ip, err := netip.ParseAddr(s.hostIP(p))
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

	mem := boundedMemory(s.MemoryMiB)
	pids := int64(pidLimit)
	name := "raptor-" + s.ServerID
	_ = c.remove(ctx, name)

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
			Labels: map[string]string{
				LabelManaged: "true",
				LabelServer:  s.ServerID,
				LabelRole:    RoleServer,
			},
		},
		HostConfig: &container.HostConfig{
			Mounts:         []mount.Mount{{Type: mount.TypeBind, Source: s.Dir, Target: "/home/container"}},
			Tmpfs:          map[string]string{"/tmp": "rw,exec,nosuid,size=" + strconv.Itoa(tmpfsSizeMiB) + "M"},
			PortBindings:   bindings,
			NetworkMode:    container.NetworkMode(s.Network),
			ReadonlyRootfs: true,
			SecurityOpt:    []string{"no-new-privileges"},
			CapDrop: []string{
				"SETPCAP", "MKNOD", "AUDIT_WRITE", "NET_RAW", "DAC_OVERRIDE",
				"FOWNER", "FSETID", "NET_BIND_SERVICE", "SYS_CHROOT", "SETFCAP",
			},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyDisabled},
			Resources: container.Resources{
				Memory:            mem,
				MemoryReservation: s.MemoryMiB * 1024 * 1024,
				MemorySwap:        mem,
				PidsLimit:         &pids,
			},
			LogConfig: container.LogConfig{Type: "local", Config: map[string]string{"max-size": "20m", "max-file": "3"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("create server container: %w", err)
	}
	return created.ID, nil
}

func (s ServerSpec) hostIP(p Port) string {
	if p.IP == "" {
		return "0.0.0.0"
	}
	return p.IP
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

// Attached is a live connection to a server container's console.
type Attached struct {
	Output io.Reader
	conn   io.WriteCloser
}

// Send writes a console command to the server's stdin.
func (a *Attached) Send(cmd string) error {
	_, err := io.WriteString(a.conn, cmd+"\n")
	return err
}

// Close detaches.
func (a *Attached) Close() error { return a.conn.Close() }

// Attach connects to the container's stdin/stdout. Call before Start so no
// output is missed.
func (c *Client) Attach(ctx context.Context, id string) (*Attached, error) {
	res, err := c.api.ContainerAttach(ctx, id, client.ContainerAttachOptions{Stream: true, Stdin: true, Stdout: true, Stderr: true})
	if err != nil {
		return nil, err
	}
	return &Attached{Output: res.Reader, conn: res.Conn}, nil
}

// Start starts a created container.
func (c *Client) Start(ctx context.Context, id string) error {
	_, err := c.api.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

// Stop stops a server the way its egg asks: a console command, a signal, or
// Docker's default stop. If it hasn't exited after timeout, it's killed.
func (c *Client) Stop(ctx context.Context, id string, a *Attached, stop eggs.Stop, timeout time.Duration) error {
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

// Remove force-removes a container.
func (c *Client) Remove(ctx context.Context, id string) error { return c.remove(ctx, id) }

func (c *Client) remove(ctx context.Context, id string) error {
	_, err := c.api.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	return err
}

func (c *Client) follow(ctx context.Context, id string, w io.Writer) {
	logs, err := c.api.ContainerLogs(ctx, id, client.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		return
	}
	defer logs.Close()
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
