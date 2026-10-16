package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/store"
)

type fakeHost struct {
	hostname string
	tz       string
	ntp      bool
	calls    []string
}

func (f *fakeHost) run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	switch {
	case call == "hostnamectl hostname":
		return []byte(f.hostname + "\n"), nil
	case name == "hostnamectl" && args[0] == "set-hostname":
		f.hostname = args[1]
	case name == "timedatectl" && args[0] == "show":
		ntp := map[bool]string{true: "yes", false: "no"}[f.ntp]
		return []byte("Timezone=" + f.tz + "\nNTP=" + ntp + "\nNTPSynchronized=" + ntp + "\n"), nil
	case name == "timedatectl" && args[0] == "set-timezone":
		f.tz = args[1]
	case name == "timedatectl" && args[0] == "set-ntp":
		f.ntp = args[1] == "true"
	case call == "nft list ruleset":
		return []byte("table ip nat {\n\tchain DOCKER {\n\t\tiifname \"raptor0\" counter packets 0 bytes 0 return\n\t}\n}\n"), nil
	case call == "systemctl reboot":
	default:
		return nil, errors.New("unexpected " + call)
	}
	return nil, nil
}

func newManager(t *testing.T) (*Manager, *fakeHost, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	hosts := filepath.Join(dir, "hosts")
	_ = os.WriteFile(hosts, []byte("127.0.0.1\tlocalhost\n127.0.1.1\tbox.example.com\tbox\n::1\tlocalhost\n"), 0o600)
	ssh := filepath.Join(dir, "sshd_config")
	_ = os.WriteFile(ssh, []byte("# sshd\nPort 2200\nMatch User git\n  Port 9999\n"), 0o600)
	f := &fakeHost{hostname: "box.example.com", tz: "UTC", ntp: true}
	return &Manager{DB: db, Run: f.run, Hosts: hosts, SSHConfig: ssh}, f, hosts
}

func TestHostnameAndTime(t *testing.T) {
	ctx := context.Background()
	m, f, hosts := newManager(t)
	for _, bad := range []string{"", "-box", "box-", "a b", "box;reboot", "bo_x", strings.Repeat("a", 64)} {
		if _, err := m.SetHostname(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if note, err := m.SetHostname(ctx, "Game-1.example.com"); err != nil || note != "" || f.hostname != "game-1.example.com" {
		t.Fatalf("set: %q, %v, %s", note, err, f.hostname)
	}
	b, _ := os.ReadFile(hosts)
	if !strings.Contains(string(b), "127.0.1.1\tgame-1.example.com\tgame-1\n") || !strings.Contains(string(b), "127.0.0.1\tlocalhost") {
		t.Errorf("hosts:\n%s", b)
	}
	// /etc/hosts not writable (an older install's sandbox): a note, not an error.
	m.Hosts = filepath.Join(t.TempDir(), "missing", "hosts")
	if note, err := m.SetHostname(ctx, "other"); err != nil || !strings.Contains(note, "127.0.1.1") {
		t.Errorf("unwritable hosts: %q, %v", note, err)
	}

	if err := m.SetTime(ctx, "Mars/Olympus", nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad zone: %v", err)
	}
	off := false
	if err := m.SetTime(ctx, "Europe/Berlin", &off); err != nil || f.tz != "Europe/Berlin" || f.ntp {
		t.Fatalf("time: %v, %+v", err, f)
	}
	st, err := m.State(ctx)
	if err != nil || st.Hostname != "other" || st.Timezone != "Europe/Berlin" || st.NTP || len(st.SSHPorts) != 1 || st.SSHPorts[0] != 2200 {
		t.Errorf("state: %+v, %v", st, err)
	}
	if len(st.Ruleset) != 1 || st.Ruleset[0].Owner != firewall.OwnerDocker || st.RulesetError != "" {
		t.Errorf("rule set: %+v %q", st.Ruleset, st.RulesetError)
	}
}

func TestFirewall(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newManager(t)
	var applied *firewall.HostRules
	m.ApplyFirewall = func(_ context.Context, h *firewall.HostRules) error { applied = h; return nil }
	if h, err := m.Firewall(ctx); err != nil || h != nil {
		t.Fatalf("never set: %+v, %v", h, err)
	}
	// Blocking SSH (port 2200 here) from everywhere, with nothing let in,
	// is refused.
	lock := firewall.HostRules{Rules: []firewall.HostRule{{Action: firewall.Block, Ports: "2000-3000"}}}
	if err := m.SetFirewall(ctx, lock); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "SSH") || applied != nil {
		t.Fatalf("lockout: %v", err)
	}
	lock.Rules = append(lock.Rules, firewall.HostRule{Action: firewall.Allow, Protocol: firewall.TCP, Ports: "2200", Source: "198.51.100.4"})
	lock.BlockIncoming = true
	if err := m.SetFirewall(ctx, lock); err != nil || applied == nil {
		t.Fatalf("with an exception: %v", err)
	}
	st, _ := m.State(ctx)
	if !st.Firewall.BlockIncoming || len(st.Firewall.Rules) != 2 || st.Firewall.Rules[1].Source != "198.51.100.4/32" {
		t.Errorf("stored: %+v", st.Firewall)
	}
	// Loading fails: nothing is stored.
	m.ApplyFirewall = func(context.Context, *firewall.HostRules) error { return errors.New("nft: syntax") }
	if err := m.SetFirewall(ctx, firewall.HostRules{}); err == nil || strings.Contains(err.Error(), "nft") {
		t.Errorf("failed load: %v", err)
	}
	if h, _ := m.Firewall(ctx); h == nil || !h.BlockIncoming {
		t.Error("a firewall that didn't load was stored")
	}
}

func TestReboot(t *testing.T) {
	m, f, _ := newManager(t)
	var waited time.Duration
	m.After = func(d time.Duration, fn func()) { waited = d; fn() }
	if err := m.Reboot(context.Background()); err != nil || waited == 0 || f.calls[len(f.calls)-1] != "systemctl reboot" {
		t.Errorf("reboot: %v, %v", err, f.calls)
	}
}
