// Package system changes the machine's own settings from the Panel
// (docs/WINGS.md#system-settings): its hostname, time zone and clock sync,
// its firewall, and rebooting it. Wings is sandboxed (ProtectSystem=strict,
// ProtectHostname), so the hostname and time zone go through systemd's
// hostnamed and timedated over D-Bus, which do the writing themselves.
package system

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// firewallKey holds the node's firewall in kv; no row is no firewall.
const firewallKey = "host.firewall"

// ErrInvalid matches errors about what was asked (errors.Is). Their
// messages are sentences for people, without it.
var ErrInvalid = errors.New("invalid system setting")

type invalid struct{ error }

func (e invalid) Is(target error) bool { return target == ErrInvalid }

func invalidf(format string, a ...any) error { return invalid{fmt.Errorf(format, a...)} }

// Manager reads and changes the machine's settings.
type Manager struct {
	DB  *store.DB
	Log *slog.Logger
	// Run runs a command and returns its output (exec by default).
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Hosts is /etc/hosts; SSHConfig is sshd's config file.
	Hosts, SSHConfig string
	// ApplyFirewall loads the firewall (the daemon's rule set).
	ApplyFirewall func(ctx context.Context, h *firewall.HostRules) error
	// Open is what stays open when the firewall blocks incoming
	// connections.
	Open func(ctx context.Context) []firewall.OpenPort
	// After runs f after d (time.AfterFunc), for the reboot.
	After func(d time.Duration, f func())
}

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return m.Log
}

func (m *Manager) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if m.Run != nil {
		return m.Run(ctx, name, args...)
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed commands; arguments are validated
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", name, err, bytes.TrimSpace(out))
	}
	return out, nil
}

// State is the machine's settings now.
type State struct {
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
	// NTP is whether the clock syncs itself; Synced whether it is.
	NTP    bool `json:"ntp"`
	Synced bool `json:"synced"`
	// Firewall is the node's firewall (empty if never set); Open is what
	// stays open when it blocks incoming connections.
	Firewall firewall.HostRules  `json:"firewall"`
	Open     []firewall.OpenPort `json:"open"`
	// SSHPorts are the ports sshd listens on; UFW is whether ufw is on
	// too (it filters before these rules can allow anything).
	SSHPorts []int `json:"ssh_ports"`
	UFW      bool  `json:"ufw"`
}

// State reads the settings.
func (m *Manager) State(ctx context.Context) (State, error) {
	var st State
	if out, err := m.run(ctx, "hostnamectl", "hostname"); err == nil {
		st.Hostname = strings.TrimSpace(string(out))
	} else {
		m.log().Warn("reading the hostname failed", "err", err)
	}
	if out, err := m.run(ctx, "timedatectl", "show", "-p", "Timezone", "-p", "NTP", "-p", "NTPSynchronized"); err == nil {
		for line := range strings.Lines(string(out)) {
			k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
			switch k {
			case "Timezone":
				st.Timezone = v
			case "NTP":
				st.NTP = v == "yes"
			case "NTPSynchronized":
				st.Synced = v == "yes"
			}
		}
	} else {
		m.log().Warn("reading the time settings failed", "err", err)
	}
	h, err := m.Firewall(ctx)
	if err != nil {
		return st, err
	}
	if h != nil {
		st.Firewall = *h
	}
	if st.Firewall.Rules == nil {
		st.Firewall.Rules = []firewall.HostRule{}
	}
	if m.Open != nil {
		st.Open = m.Open(ctx)
	}
	st.SSHPorts = SSHPorts(m.SSHConfig)
	st.UFW = ufwActive()
	return st, nil
}

// hostname is RFC 1123: labels of letters, digits, and hyphens, not
// starting or ending with a hyphen.
var hostnameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// SetHostname changes the hostname, and the 127.0.1.1 line in /etc/hosts
// that Debian keeps for it, so the machine still resolves its own name. It
// returns a note for the owner if that line couldn't be changed (on nodes
// installed before Wings could write the file).
func (m *Manager) SetHostname(ctx context.Context, name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) > 253 || !hostnameRe.MatchString(name) {
		return "", invalidf("a hostname is letters, digits, and hyphens (dots between parts), like game-box-1")
	}
	old := ""
	if out, err := m.run(ctx, "hostnamectl", "hostname"); err == nil {
		old = strings.TrimSpace(string(out))
	}
	if _, err := m.run(ctx, "hostnamectl", "set-hostname", name); err != nil {
		m.log().Error("setting the hostname failed", "err", err)
		return "", errors.New("the hostname couldn't be changed: systemd-hostnamed didn't accept it")
	}
	m.log().Info("hostname changed", "from", old, "to", name)
	if old != "" && old != name && m.Hosts != "" {
		if err := replaceHost(m.Hosts, old, name); err != nil {
			// The hostname changed; only the local name lookup is stale.
			m.log().Warn("updating /etc/hosts failed", "err", err)
			return fmt.Sprintf("Change %s to %s on the 127.0.1.1 line of /etc/hosts too, or sudo warns it can't resolve the host.", old, name), nil
		}
	}
	return "", nil
}

// replaceHost renames old to name on 127.0.1.1 lines, writing the file in
// place (it's the one file in /etc Wings may write).
func replaceHost(path, old, name string) error {
	b, err := os.ReadFile(path) //nolint:gosec // /etc/hosts
	if err != nil {
		return err
	}
	var out strings.Builder
	changed := false
	for line := range strings.Lines(string(b)) {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[0] == "127.0.1.1" {
			for i, f := range fields[1:] {
				if f == old {
					fields[i+1] = name
					changed = true
				} else if short, _, ok := strings.Cut(old, "."); ok && f == short {
					short2, _, _ := strings.Cut(name, ".")
					fields[i+1] = short2
					changed = true
				}
			}
			out.WriteString(strings.Join(fields, "\t") + "\n")
			continue
		}
		out.WriteString(line)
	}
	if !changed {
		return nil
	}
	return os.WriteFile(path, []byte(out.String()), 0o644) //nolint:gosec // /etc/hosts is world-readable
}

// SetTime changes the time zone (an IANA name), and whether the clock syncs
// itself.
func (m *Manager) SetTime(ctx context.Context, tz string, ntp *bool) error {
	if tz != "" {
		if _, err := time.LoadLocation(tz); err != nil || strings.Contains(tz, "..") || filepath.IsAbs(tz) || tz == "Local" {
			return invalidf("%q isn't a time zone", tz)
		}
		if _, err := m.run(ctx, "timedatectl", "set-timezone", tz); err != nil {
			m.log().Error("setting the time zone failed", "err", err)
			return fmt.Errorf("the time zone couldn't be changed to %s: the machine doesn't know it", tz)
		}
	}
	if ntp != nil {
		if _, err := m.run(ctx, "timedatectl", "set-ntp", strconv.FormatBool(*ntp)); err != nil {
			m.log().Error("setting clock sync failed", "err", err)
			return errors.New("clock sync couldn't be changed: the machine has no time sync service (systemd-timesyncd)")
		}
	}
	return nil
}

// Firewall returns the node's firewall, or nil if it was never set.
func (m *Manager) Firewall(ctx context.Context) (*firewall.HostRules, error) {
	b, err := m.DB.Read.GetKV(ctx, firewallKey)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h firewall.HostRules
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// SetFirewall checks, saves, and loads the node's firewall.
func (m *Manager) SetFirewall(ctx context.Context, h firewall.HostRules) error {
	if err := h.Validate(); err != nil {
		return invalid{err}
	}
	// Nobody locks themselves out by accident: a rule blocking SSH from
	// everywhere needs another letting some address in.
	for _, port := range SSHPorts(m.SSHConfig) {
		closes := slices.ContainsFunc(h.Rules, func(r firewall.HostRule) bool {
			return r.Action == firewall.Block && r.Source == "" && r.Covers(firewall.TCP, port)
		})
		allowed := slices.ContainsFunc(h.Rules, func(r firewall.HostRule) bool {
			return r.Action == firewall.Allow && r.Covers(firewall.TCP, port)
		})
		if closes && !allowed {
			return invalidf("that would block SSH (port %d) from everywhere, and you'd only reach the machine through your provider's console. Allow your own address first", port)
		}
	}
	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if m.ApplyFirewall != nil {
		if err := m.ApplyFirewall(ctx, &h); err != nil {
			m.log().Error("loading the firewall failed", "err", err)
			return errors.New("the firewall rules couldn't be loaded, so nothing changed")
		}
	}
	return m.DB.Write.SetKV(ctx, store.SetKVParams{Key: firewallKey, Value: b})
}

// Reboot restarts the machine in a few seconds, so the command's answer
// gets back first. raptor-shutdown.service stops the servers properly.
func (m *Manager) Reboot(ctx context.Context) error {
	after := m.After
	if after == nil {
		after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}
	m.log().Warn("rebooting the machine, as asked from the Panel")
	after(3*time.Second, func() {
		if _, err := m.run(context.WithoutCancel(ctx), "systemctl", "reboot"); err != nil {
			m.log().Error("rebooting failed", "err", err)
		}
	})
	return nil
}

// SSHPorts are the ports sshd listens on, from its config and the files
// it includes from sshd_config.d (22 if none is set).
func SSHPorts(path string) []int {
	if path == "" {
		path = "/etc/ssh/sshd_config"
	}
	files := []string{path}
	if more, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "sshd_config.d", "*.conf")); more != nil {
		files = append(more, files...) // sshd takes the first value it reads, and includes come first
	}
	var ports []int
	for _, f := range files {
		ports = append(ports, portsIn(f)...)
	}
	slices.Sort(ports)
	ports = slices.Compact(ports)
	if len(ports) == 0 {
		return []int{22}
	}
	return ports
}

func portsIn(path string) []int {
	f, err := os.Open(path) //nolint:gosec // sshd's config files
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && strings.EqualFold(fields[0], "Port") {
			if p, err := strconv.Atoi(fields[1]); err == nil && p > 0 && p < 65536 {
				out = append(out, p)
			}
		}
		// A Match block's settings aren't global; the rest is.
		if len(fields) > 0 && strings.EqualFold(fields[0], "Match") {
			break
		}
	}
	return out
}

func ufwActive() bool {
	b, err := os.ReadFile("/etc/ufw/ufw.conf")
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(b)) {
		if strings.TrimSpace(line) == "ENABLED=yes" {
			return true
		}
	}
	return false
}
