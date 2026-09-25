// Package config loads /etc/raptor/config.yml: static, box-level Wings settings
// (docs/WINGS.md#config-file). Server configuration lives in SQLite, not here.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultPath is where the installer writes the config.
const DefaultPath = "/etc/raptor/config.yml"

// Config is the parsed config file.
type Config struct {
	NodeID  string  `yaml:"node_id"`
	Panel   Panel   `yaml:"panel"`
	TLS     TLS     `yaml:"tls"`
	Paths   Paths   `yaml:"paths"`
	Docker  Docker  `yaml:"docker"`
	Ports   Ports   `yaml:"ports"`
	Limits  Limits  `yaml:"limits"`
	Updates Updates `yaml:"updates"`
	Log     Log     `yaml:"log"`
}

// Panel is where Wings connects. Never hardcoded in Wings.
type Panel struct {
	URL    string `yaml:"url"`
	Tunnel string `yaml:"tunnel"`
}

// TLS holds certificate paths.
type TLS struct {
	CA   string `yaml:"ca"`
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// Paths holds on-box locations.
type Paths struct {
	State   string `yaml:"state"`
	Volumes string `yaml:"volumes"`
	Tmp     string `yaml:"tmp"`
	Socket  string `yaml:"socket"`
}

// Docker holds Docker network settings.
type Docker struct {
	Network        string `yaml:"network"`
	Subnet         string `yaml:"subnet"`
	InstallNetwork string `yaml:"install_network"`
	InstallSubnet  string `yaml:"install_subnet"`
}

// Ports holds Wings' own listening ports.
type Ports struct {
	SFTP  int `yaml:"sftp"`
	HTTPS int `yaml:"https"`
}

// Limits holds node-wide concurrency and safety limits.
type Limits struct {
	ConcurrentInstalls int      `yaml:"concurrent_installs"`
	ConcurrentBackups  int      `yaml:"concurrent_backups"`
	HostDiskMinFree    ByteSize `yaml:"host_disk_min_free"`
}

// Updates controls self-update.
type Updates struct {
	Channel string `yaml:"channel"`
	Pin     string `yaml:"pin"`
}

// Log controls logging.
type Log struct {
	Level string `yaml:"level"`
}

// Default returns the config used for any key the file doesn't set.
func Default() Config {
	return Config{
		Panel: Panel{URL: "https://raptorpanel.net", Tunnel: "tunnel.raptorpanel.net:443"},
		TLS: TLS{
			CA:   "/etc/raptor/tls/panel-ca.pem",
			Cert: "/etc/raptor/tls/node.pem",
			Key:  "/etc/raptor/tls/node.key",
		},
		Paths: Paths{
			State:   "/var/lib/raptor/state.db",
			Volumes: "/var/lib/raptor/volumes",
			Tmp:     "/var/lib/raptor/tmp",
			Socket:  "/run/raptor/wings.sock",
		},
		Docker: Docker{
			Network:        "raptor_nw",
			Subnet:         "172.29.0.0/16",
			InstallNetwork: "raptor_install",
			InstallSubnet:  "172.30.0.0/16",
		},
		Ports:   Ports{SFTP: 2022, HTTPS: 8443},
		Limits:  Limits{ConcurrentInstalls: 2, ConcurrentBackups: 2, HostDiskMinFree: 10 << 30},
		Updates: Updates{Channel: "stable"},
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
	for name, p := range map[string]int{"ports.sftp": c.Ports.SFTP, "ports.https": c.Ports.HTTPS} {
		if p < 1 || p > 65535 {
			errs = append(errs, fmt.Errorf("%s %d: out of range", name, p))
		}
	}
	if c.Limits.ConcurrentInstalls < 1 || c.Limits.ConcurrentBackups < 1 {
		errs = append(errs, errors.New("limits: concurrency must be at least 1"))
	}
	for name, p := range map[string]string{"paths.state": c.Paths.State, "paths.volumes": c.Paths.Volumes, "paths.tmp": c.Paths.Tmp, "paths.socket": c.Paths.Socket} {
		if !strings.HasPrefix(p, "/") {
			errs = append(errs, fmt.Errorf("%s %q: must be an absolute path", name, p))
		}
	}
	return errors.Join(errs...)
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
