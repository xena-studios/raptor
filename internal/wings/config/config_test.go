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
limits:
  host_disk_min_free: 5GiB
log:
  level: debug
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Panel.URL != "https://example.test" || cfg.Panel.Tunnel != "tunnel.raptorpanel.net:443" {
		t.Errorf("panel = %+v", cfg.Panel)
	}
	if cfg.Limits.HostDiskMinFree != 5<<30 {
		t.Errorf("min free = %d", cfg.Limits.HostDiskMinFree)
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
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		} else if name == "unknown key" && !strings.Contains(err.Error(), "nodeid") {
			t.Errorf("error doesn't name the key: %v", err)
		}
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
