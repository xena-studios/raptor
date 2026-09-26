// Package containers defines the container runtime Wings runs installs and
// servers on. Docker is the only implementation (internal/wings/docker); the
// rest of Wings depends on this interface, not on Docker.
package containers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
)

// Runtime runs install and server containers.
type Runtime interface {
	// Setup prepares the runtime (networks, cgroup placement). It's idempotent
	// and must succeed before anything else is called.
	Setup(ctx context.Context) (Networks, error)
	// Install runs an egg's install script in a separate, short-lived container.
	Install(ctx context.Context, s InstallSpec) (InstallResult, error)
	// Create creates (but doesn't start) a server's container and returns its ID.
	Create(ctx context.Context, s ServerSpec) (string, error)
	// Attach connects to a container's console. Call before Start so no output is missed.
	Attach(ctx context.Context, id string) (Console, error)
	// Input connects to a container's stdin only. Unlike Attach, nothing has to
	// read the container's output, so a slow reader can never stall the game.
	Input(ctx context.Context, id string) (Input, error)
	// Logs streams the container's output, one line per Line, from Docker's
	// log store: history (the last tail lines, or all if tail < 0, after
	// since if set) and then, with follow, live output. Unlike an attach
	// stream it can be resumed exactly after a Wings or Docker restart.
	Logs(ctx context.Context, id string, o LogOptions) (<-chan Line, <-chan error)
	// Inspect reports a container's state.
	Inspect(ctx context.Context, id string) (State, error)
	Start(ctx context.Context, id string) error
	// Stop stops a server the way its egg asks, killing it after timeout.
	Stop(ctx context.Context, id string, in Sender, stop eggs.Stop, timeout time.Duration) error
	// Wait blocks until the container stops and returns its exit code.
	Wait(ctx context.Context, id string) (int64, error)
	Remove(ctx context.Context, id string) error
	// List returns the containers Wings manages, and nothing else.
	List(ctx context.Context) ([]Container, error)
	// CheckArch fails with ErrUnsupportedArch if the image has no variant for
	// this machine's CPU architecture. When the registry can't be asked, it
	// falls back to a local copy and otherwise lets the pull decide.
	CheckArch(ctx context.Context, image string) error
	Version(ctx context.Context) (string, error)
}

// Sender writes console commands to a container's stdin.
type Sender interface {
	// Send writes a console command followed by a newline.
	Send(cmd string) error
}

// Input is a connection to a container's stdin.
type Input interface {
	Sender
	Close() error
}

// Console is a live connection to a container's stdin and output.
type Console interface {
	io.Reader
	Input
}

// LogOptions select what Logs returns.
type LogOptions struct {
	Follow bool
	Tail   int       // lines of history; < 0 = all
	Since  time.Time // only output at or after this time
}

// Line is one line of container output.
type Line struct {
	Time time.Time
	Text string // without the line ending
}

// State is a container's state.
type State struct {
	Running   bool
	ExitCode  int64
	OOMKilled bool
	StartedAt time.Time
}

// Network is one of Wings' container networks.
type Network struct {
	Name    string
	Bridge  string // host interface name
	Subnet  netip.Prefix
	Gateway netip.Addr
}

// Networks are the networks Setup ensured.
type Networks struct {
	Server  Network // game servers
	Install Network // install containers: outbound internet only
	// CgroupParent is the systemd slice containers are placed in, or "" when
	// Docker doesn't use the systemd cgroup driver.
	CgroupParent string
}

// InstallSpec describes one egg install run.
type InstallSpec struct {
	ServerID  string
	Dir       string // the server's data directory on the host
	TmpDir    string // parent for the per-install script directory
	Install   eggs.Install
	Env       []string
	MemoryMiB int64
	Timeout   time.Duration
	Output    io.Writer // receives the script's output
}

// InstallResult reports how the install script ended.
type InstallResult struct {
	ExitCode int64
	TimedOut bool
}

// ServerSpec describes a server container.
type ServerSpec struct {
	ServerID string
	Dir      string
	Image    string
	Env      []string
	UID, GID int
	Limits   Limits
	// HostNetwork runs the server in the host's network namespace instead of
	// the server network. Ports are then bound by the game directly.
	HostNetwork bool
	Ports       []Port
}

// Limits are a server's resource limits.
type Limits struct {
	MemoryMiB int64 `json:"memory_mib"` // allocated memory; the container gets this plus overhead
	SwapMiB   int64 `json:"swap_mib"`
	// CPUWeight is the relative CPU share (Docker CPU shares, default 1024).
	// It's the default way to divide CPU because it never throttles.
	CPUWeight int64 `json:"cpu_weight"`
	// CPUPercent is an optional hard limit: 100 = one core. 0 = none.
	CPUPercent int64 `json:"cpu_percent"`
	// Cpuset pins the server to specific cores ("0-3", "1,3"). "" = any.
	Cpuset string `json:"cpuset"`
	// PIDs is the process limit. 0 = the default (512).
	PIDs int64 `json:"pids"`
}

// Port is a published port (TCP and UDP).
type Port struct {
	IP   string // "" or "0.0.0.0" = all addresses
	Port int
}

// Container is a container Wings manages.
type Container struct {
	ID       string
	Name     string
	ServerID string
	Role     string
	State    string
}

// ErrUnsupportedArch is returned when an image can't run on this machine.
var ErrUnsupportedArch = errors.New("image not available for this CPU architecture")

// ErrPortInUse is returned when an allocated port is already taken on the host.
var ErrPortInUse = errors.New("port in use")

// CheckPorts fails with ErrPortInUse if any port is already bound on the host,
// for TCP or UDP. Docker would otherwise fail later with a less useful error,
// or not at all for a host-networked server.
func CheckPorts(ports []Port) error {
	var errs []error
	for _, p := range ports {
		ip := p.IP
		if ip == "" {
			ip = "0.0.0.0"
		}
		addr := net.JoinHostPort(ip, strconv.Itoa(p.Port))
		l, err := net.Listen("tcp", addr) //nolint:noctx // bound and closed immediately
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %s/tcp: %w", ErrPortInUse, addr, err))
		} else {
			_ = l.Close()
		}
		pc, err := net.ListenPacket("udp", addr) //nolint:noctx // bound and closed immediately
		if err != nil {
			errs = append(errs, fmt.Errorf("%w: %s/udp: %w", ErrPortInUse, addr, err))
		} else {
			_ = pc.Close()
		}
	}
	return errors.Join(errs...)
}
