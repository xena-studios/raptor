package config

import (
	"strings"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte("node_id: abc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeID != "abc" || cfg.Ports.SFTP != 2022 || cfg.Paths.Socket != "/run/raptor/wings.sock" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if _, err := Parse(nil); err != nil {
		t.Fatalf("empty file: %v", err)
	}
}

func TestParseFull(t *testing.T) {
	cfg, err := Parse([]byte(`
panel:
  url: https://example.test
docker:
  subnet: 10.50.0.0/16
  install_allow: [192.168.1.10/32]
limits:
  host_disk_min_free: 5GiB
  reserved_memory: 2GiB
log:
  level: debug
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Panel.URL != "https://example.test" || cfg.Identity.Key != "/etc/raptor/node.key" {
		t.Errorf("panel = %+v", cfg.Panel)
	}
	if cfg.Limits.HostDiskMinFree != 5<<30 || cfg.Limits.ReservedMemory != 2<<30 {
		t.Errorf("limits = %+v", cfg.Limits)
	}
	server, install := cfg.Docker.Subnets()
	if server.String() != "10.50.0.0/16" || install.IsValid() {
		t.Errorf("subnets = %v, %v", server, install)
	}
	if p := cfg.Docker.AllowedPrefixes(); len(p) != 1 || p[0].String() != "192.168.1.10/32" {
		t.Errorf("install_allow = %v", p)
	}
}

func TestParseRejects(t *testing.T) {
	for name, in := range map[string]string{
		"unknown key":    "nodeid: abc\n",
		"nested unknown": "panel:\n  urll: x\n",
		"bad level":      "log:\n  level: loud\n",
		"bad port":       "ports:\n  sftp: 70000\n",
		"relative path":  "paths:\n  state: state.db\n",
		"bad size":       "limits:\n  host_disk_min_free: lots\n",
		"bad channel":    "updates:\n  channel: nightly\n",
		"bad pin":        "updates:\n  pin: \"1.4\"\n",
		"bad subnet":     "docker:\n  subnet: 10.50.0.1/16\n",
		"ipv6 subnet":    "docker:\n  subnet: fd00::/64\n",
		"bad allow":      "docker:\n  install_allow: [lan]\n",
		"same networks":  "docker:\n  install_network: raptor_nw\n",
		"notify type":    "notifications:\n  - type: slack\n    url: https://hooks.slack.com/x\n",
		"notify url":     "notifications:\n  - type: webhook\n    url: not a url\n",
		"notify discord": "notifications:\n  - type: discord\n    url: https://evil.example/api/webhooks/1/x\n",
		"notify http":    "notifications:\n  - type: discord\n    url: http://discord.com/api/webhooks/1/x\n",
		"notify event":   "notifications:\n  - type: webhook\n    url: https://example.com/hook\n    events: [everything]\n",
		"notify secret":  "notifications:\n  - type: discord\n    url: https://discord.com/api/webhooks/1/x\n    secret: s\n",
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		} else if name == "unknown key" && !strings.Contains(err.Error(), "nodeid") {
			t.Errorf("error doesn't name the key: %v", err)
		}
	}
}

func TestParseNotifications(t *testing.T) {
	cfg, err := Parse([]byte(`notifications:
  - name: ops
    type: discord
    url: https://discord.com/api/webhooks/123/abc
    events: [security, crash]
  - type: webhook
    url: http://10.0.0.5:8080/raptor
    secret: s3cret
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Notifications) != 2 || cfg.Notifications[0].Events[1] != "crash" || cfg.Notifications[1].Secret != "s3cret" {
		t.Fatalf("notifications: %+v", cfg.Notifications)
	}
}

func TestParseByteSize(t *testing.T) {
	for in, want := range map[string]ByteSize{"10GiB": 10 << 30, "512MiB": 512 << 20, "1GB": 1e9, "123": 123} {
		got, err := ParseByteSize(in)
		if err != nil || got != want {
			t.Errorf("ParseByteSize(%q) = %d, %v", in, got, err)
		}
	}
}
