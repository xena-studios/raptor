// Package config loads /etc/raptor/config.yml: static, box-level Wings settings
// (docs/WINGS.md#config-file). Server configuration lives in SQLite, not here.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// DefaultPath is where the installer writes the config.
const DefaultPath = "/etc/raptor/config.yml"

// Config is the parsed config file.
type Config struct {
	NodeID   string   `yaml:"node_id"`
	Panel    Panel    `yaml:"panel"`
	Identity Identity `yaml:"identity"`
	Paths    Paths    `yaml:"paths"`
	Docker   Docker   `yaml:"docker"`
	Ports    Ports    `yaml:"ports"`
	Limits   Limits   `yaml:"limits"`
	Storage  Storage  `yaml:"storage"`
	Updates  Updates  `yaml:"updates"`
	Log      Log      `yaml:"log"`
	// Notifications are sent straight from the node (docs/WINGS.md#notifications).
	// They're set here, on the box, and never by the Panel, so a compromised
	// Panel can't silence alerts about signed actions.
	Notifications []Notification `yaml:"notifications"`
}

// Notification is where to send notifications, and which.
type Notification struct {
	Name string `yaml:"name"` // shown in logs and raptor notifications test
	Type string `yaml:"type"` // discord or webhook
	URL  string `yaml:"url"`
	// Secret signs webhook bodies (HMAC-SHA256 in X-Raptor-Signature).
	Secret string `yaml:"secret"`
	// Events are the categories to send (see NotificationCategories);
	// empty = all of them.
	Events []string `yaml:"events"`
}

// NotificationCategories are what notifications can be about.
var NotificationCategories = []string{"security", "crash", "backup", "disk", "install", "update"}

// Panel is where Wings connects (a WebSocket to the Panel URL). Never
// hardcoded in Wings.
type Panel struct {
	URL string `yaml:"url"`
	// AppURL is the web app's origin. Passkey signatures are only accepted
	// when made there, with its hostname as the RP ID (docs/DECISIONS.md #82):
	// never the API's hostname or any other subdomain.
	AppURL string `yaml:"app_url"`
}

// Identity holds the node's key and the Panel's pinned signing key, both
// written at enrollment (docs/ARCHITECTURE.md#node-connection), and the SFTP
// host key, generated on first start.
type Identity struct {
	Key         string `yaml:"key"`           // the node's private key; never leaves the box
	PanelKey    string `yaml:"panel_key"`     // the Panel's public signing key
	SFTPHostKey string `yaml:"sftp_host_key"` // never leaves the box
}

// Paths holds on-box locations.
type Paths struct {
	State   string `yaml:"state"`
	Volumes string `yaml:"volumes"`
	Backups string `yaml:"backups"` // the local backup destination
	Tmp     string `yaml:"tmp"`
	Logs    string `yaml:"logs"`
	Socket  string `yaml:"socket"`
}

// Docker holds Docker network settings. Empty subnets are picked
// automatically from free ranges when the network is first created.
type Docker struct {
	Network        string   `yaml:"network"`
	Subnet         string   `yaml:"subnet"`
	InstallNetwork string   `yaml:"install_network"`
	InstallSubnet  string   `yaml:"install_subnet"`
	InstallAllow   []string `yaml:"install_allow"` // private CIDRs install containers may reach
}

// Ports holds Wings' own listening ports. Wings runs no HTTP server; SFTP
// only listens when it's enabled for the node.
type Ports struct {
	SFTP int `yaml:"sftp"`
}

// Limits holds node-wide concurrency and safety limits.
type Limits struct {
	ConcurrentInstalls int      `yaml:"concurrent_installs"`
	ConcurrentBackups  int      `yaml:"concurrent_backups"`
	HostDiskMinFree    ByteSize `yaml:"host_disk_min_free"`
	// ReservedMemory is kept free of game servers for the OS, Docker, and
	// Wings. 0 = automatic (10% of RAM, 1–4 GiB).
	ReservedMemory ByteSize `yaml:"reserved_memory"`
	// BackupMemory caps each backup worker process.
	BackupMemory ByteSize `yaml:"backup_memory"`
}

// Storage configures the server data volume (docs/WINGS.md#disk-quotas).
type Storage struct {
	// Quotas: disk limits enforced by XFS project quotas on the volume
	// (tiers 1 and 2). false = tier 3, the owner opted out (--no-quota):
	// limits are checked by periodic scans instead.
	Quotas bool `yaml:"quotas"`
}

// Updates controls self-update (docs/WINGS.md#updates).
type Updates struct {
	Channel string `yaml:"channel"` // stable or beta
	Pin     string `yaml:"pin"`     // a version to install and stay on, e.g. "1.4.2"
	// Automatic lets the Panel's staged rollouts update the node (default
	// on). Off, the node only updates when its owner runs raptor update.
	Automatic bool `yaml:"automatic"`
}

// Log controls logging.
type Log struct {
	Level string `yaml:"level"`
}

// Default returns the config used for any key the file doesn't set.
func Default() Config {
	return Config{
		Panel: Panel{URL: "https://api.raptorpanel.net", AppURL: "https://app.raptorpanel.net"},
		Identity: Identity{
			Key:         "/etc/raptor/node.key",
			PanelKey:    "/etc/raptor/panel.pub",
			SFTPHostKey: "/etc/raptor/sftp_host_key",
		},
		Paths: Paths{
			State:   "/var/lib/raptor/state.db",
			Volumes: "/var/lib/raptor/volumes",
			Backups: "/var/lib/raptor/backups",
			Tmp:     "/var/lib/raptor/tmp",
			Logs:    "/var/log/raptor",
			Socket:  "/run/raptor/wings.sock",
		},
		Docker: Docker{
			Network:        "raptor_nw",
			InstallNetwork: "raptor_install",
		},
		Ports:   Ports{SFTP: 2022},
		Limits:  Limits{ConcurrentInstalls: 2, ConcurrentBackups: 2, HostDiskMinFree: 10 << 30, BackupMemory: 1 << 30},
		Storage: Storage{Quotas: true},
		Updates: Updates{Channel: "stable", Automatic: true},
		Log:     Log{Level: "info"},
	}
}

// Load reads and validates the config file. Unknown keys are an error, so
// typos don't go unnoticed.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is operator-provided
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

// Parse parses and validates config file contents.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return cfg, nil
}

// Validate checks values the YAML types can't.
func (c Config) Validate() error {
	var errs []error
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("log.level %q: want debug, info, warn, or error", c.Log.Level))
	}
	switch c.Updates.Channel {
	case "stable", "beta":
	default:
		errs = append(errs, fmt.Errorf("updates.channel %q: want stable or beta", c.Updates.Channel))
	}
	if pin := c.Updates.Pin; pin != "" {
		tag := "v" + strings.TrimPrefix(pin, "v")
		if semver.Canonical(tag) != tag {
			errs = append(errs, fmt.Errorf("updates.pin %q: want a version like 1.4.2", pin))
		}
	}
	if p := c.Ports.SFTP; p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("ports.sftp %d: out of range", p))
	}
	if c.Limits.ConcurrentInstalls < 1 || c.Limits.ConcurrentBackups < 1 {
		errs = append(errs, errors.New("limits: concurrency must be at least 1"))
	}
	if c.Limits.BackupMemory < 256<<20 {
		errs = append(errs, errors.New("limits.backup_memory: at least 256MiB"))
	}
	for name, p := range map[string]string{"paths.state": c.Paths.State, "paths.volumes": c.Paths.Volumes, "paths.backups": c.Paths.Backups, "paths.tmp": c.Paths.Tmp, "paths.logs": c.Paths.Logs, "paths.socket": c.Paths.Socket, "identity.sftp_host_key": c.Identity.SFTPHostKey} {
		if !strings.HasPrefix(p, "/") {
			errs = append(errs, fmt.Errorf("%s %q: must be an absolute path", name, p))
		}
	}
	for name, v := range map[string]string{"docker.subnet": c.Docker.Subnet, "docker.install_subnet": c.Docker.InstallSubnet} {
		if v == "" {
			continue
		}
		if p, err := netip.ParsePrefix(v); err != nil || !p.Addr().Is4() || p != p.Masked() {
			errs = append(errs, fmt.Errorf("%s %q: want an IPv4 network like 172.29.0.0/16", name, v))
		}
	}
	for _, v := range c.Docker.InstallAllow {
		if _, err := netip.ParsePrefix(v); err != nil {
			errs = append(errs, fmt.Errorf("docker.install_allow %q: want a CIDR like 192.168.1.10/32", v))
		}
	}
	for i, n := range c.Notifications {
		errs = append(errs, n.validate(i))
	}
	if c.Docker.Network == "" || c.Docker.InstallNetwork == "" || c.Docker.Network == c.Docker.InstallNetwork {
		errs = append(errs, errors.New("docker: network and install_network must be set and different"))
	}
	return errors.Join(errs...)
}

func (n Notification) validate(i int) error {
	name := fmt.Sprintf("notifications[%d]", i)
	if n.Name != "" {
		name += " (" + n.Name + ")"
	}
	var errs []error
	u, err := url.Parse(n.URL)
	switch {
	case err != nil || u.Host == "":
		errs = append(errs, fmt.Errorf("%s: url %q isn't a URL", name, n.URL))
	case n.Type == "discord" && (u.Scheme != "https" || (u.Hostname() != "discord.com" && u.Hostname() != "discordapp.com")):
		errs = append(errs, fmt.Errorf("%s: a Discord webhook URL starts with https://discord.com/api/webhooks/", name))
	case n.Type == "webhook" && u.Scheme != "https" && u.Scheme != "http":
		errs = append(errs, fmt.Errorf("%s: url must be http or https", name))
	}
	switch n.Type {
	case "discord", "webhook":
	default:
		errs = append(errs, fmt.Errorf("%s: type %q: want discord or webhook", name, n.Type))
	}
	if n.Secret != "" && n.Type != "webhook" {
		errs = append(errs, fmt.Errorf("%s: secret is only for webhooks", name))
	}
	for _, e := range n.Events {
		if !slices.Contains(NotificationCategories, e) {
			errs = append(errs, fmt.Errorf("%s: event %q: want one of %s", name, e, strings.Join(NotificationCategories, ", ")))
		}
	}
	return errors.Join(errs...)
}

// Subnets returns the configured subnets; a zero prefix means automatic.
// Call after Validate.
func (d Docker) Subnets() (server, install netip.Prefix) {
	server, _ = netip.ParsePrefix(d.Subnet)
	install, _ = netip.ParsePrefix(d.InstallSubnet)
	return server, install
}

// AllowedPrefixes returns install_allow parsed. Call after Validate.
func (d Docker) AllowedPrefixes() []netip.Prefix {
	out := make([]netip.Prefix, 0, len(d.InstallAllow))
	for _, v := range d.InstallAllow {
		if p, err := netip.ParsePrefix(v); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// ByteSize is a size written like "10GiB", "512MiB", or a plain byte count.
type ByteSize int64

var units = []struct {
	suffix string
	mult   int64
}{
	{"TiB", 1 << 40},
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
	{"TB", 1e12},
	{"GB", 1e9},
	{"MB", 1e6},
	{"KB", 1e3},
	{"B", 1},
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (b *ByteSize) UnmarshalYAML(n *yaml.Node) error {
	v, err := ParseByteSize(n.Value)
	if err != nil {
		return err
	}
	*b = v
	return nil
}

// ParseByteSize parses a size string.
func ParseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(num), 10, 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			return ByteSize(n * u.mult), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return ByteSize(n), nil
}
