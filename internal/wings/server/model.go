// Package server manages servers on a node: their configuration in SQLite,
// installs, power actions, console, crash handling, and reconciliation with
// Docker after restarts (docs/SERVERS.md).
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// State is a server's runtime state (docs/SERVERS.md#states).
type State string

const (
	Installing    State = "installing"
	InstallFailed State = "install_failed"
	Offline       State = "offline"
	Starting      State = "starting"
	Running       State = "running"
	Stopping      State = "stopping"
	Crashed       State = "crashed"
)

// Install states, stored in SQLite.
const (
	installPending    = "pending"
	installInstalling = "installing"
	installInstalled  = "installed"
	installFailed     = "failed"
)

// Settings are per-server behavior switches.
type Settings struct {
	// CrashAutoRestart restarts a crashed server (with backoff). Default on.
	CrashAutoRestart bool `json:"crash_auto_restart"`
	// CleanExitIsStop treats exit code 0 as a stop, not a crash. Default off,
	// matching Pterodactyl.
	CleanExitIsStop bool `json:"clean_exit_is_stop"`
	// StopTimeout is how long a stop waits before killing. Default 60s, at
	// most 10 minutes.
	StopTimeout Duration `json:"stop_timeout"`
	// SkipInstall runs no install script (Pterodactyl's "skip egg scripts").
	SkipInstall bool `json:"skip_install"`
}

// DefaultSettings are the settings of a new server.
func DefaultSettings() Settings {
	return Settings{CrashAutoRestart: true, StopTimeout: Duration(defaultStopTimeout)}
}

const (
	defaultStopTimeout = 60 * time.Second
	maxStopTimeout     = 10 * time.Minute
)

// Duration is a time.Duration stored as a string ("60s").
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	*d = Duration(v)
	return err
}

// Allocation is an address a server is reachable on (TCP and UDP).
type Allocation struct {
	IP      string `json:"ip"` // "0.0.0.0" = all addresses
	Port    int    `json:"port"`
	Primary bool   `json:"primary"`
}

// Config is everything about a server that the Panel (or a test) sets.
type Config struct {
	Name        string
	Egg         []byte // the egg file, stored as a snapshot
	EggSource   string // where the egg came from (URL or catalog ID), informational
	Image       string // one of the egg's images; "" = the egg's default
	Startup     string // "" = the egg's default startup command
	Variables   map[string]string
	Limits      containers.Limits
	Settings    Settings
	HostNetwork bool
	Allocations []Allocation
}

// Server is a server's stored configuration and state.
type Server struct {
	ID string
	Config
	egg          *eggs.Egg
	EggHash      string
	DesiredState string // "running" or "stopped"
	InstallState string
	InstallError string
	Version      int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Egg returns the parsed egg snapshot.
func (s *Server) Egg() *eggs.Egg { return s.egg }

// Primary returns the primary allocation.
func (s *Server) Primary() Allocation {
	for _, a := range s.Allocations {
		if a.Primary {
			return a
		}
	}
	return Allocation{}
}

func eggHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ErrInvalid wraps configuration validation errors.
var ErrInvalid = errors.New("invalid server configuration")

// validate checks a config and fills in defaults. It returns the parsed egg.
func (c *Config) validate(reserved []int) (*eggs.Egg, error) {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
	}
	if c.Name == "" {
		return nil, bad("name is required")
	}
	egg, err := eggs.Parse(c.Egg)
	if err != nil {
		return nil, bad("%v", err)
	}
	if c.Image == "" {
		c.Image = egg.DefaultImage()
	} else if !slices.ContainsFunc(egg.Images, func(i eggs.Image) bool { return i.Ref == c.Image }) {
		return nil, bad("image %q isn't one of the egg's images", c.Image)
	}
	if c.Startup == "" {
		c.Startup = egg.DefaultStartup()
	}
	vars, err := egg.Validate(c.Variables)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	c.Variables = vars

	l := &c.Limits
	if l.MemoryMiB < 64 {
		return nil, bad("memory must be at least 64 MiB")
	}
	if l.SwapMiB < 0 || l.CPUPercent < 0 || l.CPUWeight < 0 || l.PIDs < 0 {
		return nil, bad("limits can't be negative")
	}
	st := &c.Settings
	if st.StopTimeout == 0 {
		st.StopTimeout = Duration(defaultStopTimeout)
	}
	if time.Duration(st.StopTimeout) < time.Second || time.Duration(st.StopTimeout) > maxStopTimeout {
		return nil, bad("stop timeout must be between 1s and %s", maxStopTimeout)
	}
	if err := validateAllocations(c.Allocations, reserved); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return egg, nil
}

// validateAllocations checks one server's allocations on their own. Conflicts
// with other servers are checked against the database.
func validateAllocations(allocs []Allocation, reserved []int) error {
	if len(allocs) == 0 {
		return errors.New("a server needs a primary allocation")
	}
	primaries := 0
	for i, a := range allocs {
		ip, err := netip.ParseAddr(a.IP)
		if err != nil || !ip.Is4() {
			return fmt.Errorf("allocation %s:%d: IP must be an IPv4 address or 0.0.0.0", a.IP, a.Port)
		}
		if a.Port < 1024 || a.Port > 65535 {
			return fmt.Errorf("allocation port %d: must be between 1024 and 65535", a.Port)
		}
		if slices.Contains(reserved, a.Port) {
			return fmt.Errorf("allocation port %d is used by Wings itself", a.Port)
		}
		if a.Primary {
			primaries++
		}
		for _, b := range allocs[:i] {
			if a.Port == b.Port && (a.IP == b.IP || a.IP == "0.0.0.0" || b.IP == "0.0.0.0") {
				return fmt.Errorf("allocation %s:%d is listed twice", a.IP, a.Port)
			}
		}
	}
	if primaries != 1 {
		return errors.New("exactly one allocation must be primary")
	}
	return nil
}

// conflicts reports whether two allocations would bind the same address.
func conflicts(a, b Allocation) bool {
	return a.Port == b.Port && (a.IP == b.IP || a.IP == "0.0.0.0" || b.IP == "0.0.0.0")
}

func fromRow(r store.Server, allocs []store.Allocation) (*Server, error) {
	s := &Server{
		ID:           r.ID,
		EggHash:      r.EggHash,
		DesiredState: r.DesiredState,
		InstallState: r.InstallState,
		InstallError: r.InstallError,
		Version:      r.Version,
		CreatedAt:    time.Unix(r.CreatedAt, 0),
		UpdatedAt:    time.Unix(r.UpdatedAt, 0),
		Config: Config{
			Name:        r.Name,
			Egg:         r.Egg,
			EggSource:   r.EggSource,
			Image:       r.Image,
			Startup:     r.Startup,
			HostNetwork: r.HostNetwork == 1,
			Settings:    DefaultSettings(),
		},
	}
	if err := json.Unmarshal([]byte(r.Variables), &s.Variables); err != nil {
		return nil, fmt.Errorf("server %s variables: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(r.Limits), &s.Limits); err != nil {
		return nil, fmt.Errorf("server %s limits: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(r.Settings), &s.Settings); err != nil {
		return nil, fmt.Errorf("server %s settings: %w", r.ID, err)
	}
	for _, a := range allocs {
		s.Allocations = append(s.Allocations, Allocation{IP: a.Ip, Port: int(a.Port), Primary: a.IsPrimary == 1})
	}
	egg, err := eggs.Parse(r.Egg)
	if err != nil {
		return nil, fmt.Errorf("server %s egg: %w", r.ID, err)
	}
	s.egg = egg
	return s, nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // only plain structs and maps are marshaled
	}
	return string(b)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
