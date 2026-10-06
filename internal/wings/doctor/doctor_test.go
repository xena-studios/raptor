package doctor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/docker"
)

// fakeSystem is a box made of maps.
type fakeSystem struct {
	files  map[string]string
	exists map[string]bool
	modes  map[string]fs.FileMode
	links  map[string]string
	cmds   map[string]string // "name arg…" → output; missing = not installed
	dial   map[string]string // addr → banner
	free   map[string]int64
	root   bool
}

func (f *fakeSystem) ReadFile(p string) ([]byte, error) {
	if s, ok := f.files[p]; ok {
		return []byte(s), nil
	}
	return nil, fs.ErrNotExist
}

func (f *fakeSystem) Stat(p string) (fs.FileInfo, error) {
	if m, ok := f.modes[p]; ok {
		return fakeInfo{m}, nil
	}
	if f.exists[p] {
		return nil, nil
	}
	return nil, fs.ErrNotExist
}

type fakeInfo struct{ mode fs.FileMode }

func (i fakeInfo) Name() string       { return "" }
func (i fakeInfo) Size() int64        { return 0 }
func (i fakeInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return false }
func (i fakeInfo) Sys() any           { return nil }

func (f *fakeSystem) Readlink(p string) (string, error) {
	if t, ok := f.links[p]; ok {
		return t, nil
	}
	return "", fs.ErrNotExist
}

func (f *fakeSystem) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	out, ok := f.cmds[strings.Join(append([]string{name}, args...), " ")]
	if !ok {
		return nil, errors.New("executable file not found")
	}
	return []byte(out), nil
}

func (f *fakeSystem) Dial(_ context.Context, addr string) (string, error) {
	if b, ok := f.dial[addr]; ok {
		return b, nil
	}
	return "", errors.New("connection refused")
}

func (f *fakeSystem) Space(p string) (int64, int64, error) {
	if n, ok := f.free[p]; ok {
		return 100 << 30, n, nil
	}
	return 0, 0, fs.ErrNotExist
}

func (f *fakeSystem) Geteuid() int {
	if f.root {
		return 0
	}
	return 1000
}

type fakeDocker struct {
	info     docker.Info
	networks map[string][]netip.Prefix
}

func (d *fakeDocker) Info(context.Context) (docker.Info, error) { return d.info, nil }
func (d *fakeDocker) NetworkSubnets(_ context.Context, name string) ([]netip.Prefix, error) {
	return d.networks[name], nil
}

// healthy is a node where everything passes.
func healthy() (*Env, *fakeSystem, *fakeDocker) {
	sys := &fakeSystem{
		files: map[string]string{
			"/etc/os-release":         "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nID=debian\nVERSION_ID=\"12\"\n",
			"/etc/docker/daemon.json": `{"live-restore": true, "userland-proxy": false}`,
		},
		exists: map[string]bool{"/sys/fs/cgroup/cgroup.controllers": true, "/run/systemd/system": true},
		links:  map[string]string{"/usr/local/bin/raptor": "/usr/local/lib/raptor/current"},
		cmds: map[string]string{
			"uname -r": "6.1.0-25-arm64\n",
			"systemctl is-active raptor-wings.service":     "active\n",
			"systemctl is-enabled raptor-shutdown.service": "enabled\n",
			"timedatectl show -p NTPSynchronized --value":  "yes\n",
			"sshd -T": "permitrootlogin prohibit-password\npasswordauthentication yes\n",
			"apt-config dump APT::Periodic::Unattended-Upgrade": `APT::Periodic::Unattended-Upgrade "1";`,
		},
		dial: map[string]string{"127.0.0.1:2022": "SSH-2.0-Raptor"},
		free: map[string]int64{"/var/lib/raptor": 50 << 30, "/var/lib/raptor/volumes": 80 << 30, "/var/lib/docker": 50 << 30},
		root: true,
	}
	dk := &fakeDocker{
		info:     docker.Info{Version: "29.1.0", LiveRestore: true, CgroupDriver: "systemd", RootDir: "/var/lib/docker"},
		networks: map[string][]netip.Prefix{"raptor_nw": {netip.MustParsePrefix("172.30.0.0/24")}, "raptor_install": {netip.MustParsePrefix("172.30.1.0/24")}},
	}
	e := &Env{
		Config: config.Default(), System: sys, Docker: dk,
		Status: &localv1.GetStatusResponse{
			Version: "1.0.0", Servers: &localv1.ServerCounts{Total: 3, Up: 2},
			Sftp: &localv1.SFTPStatus{Enabled: true, Port: 2022, HostKeyFingerprint: "SHA256:abc"},
		},
		VolumeCheck: func() error { return nil },
		Firewall:    func(context.Context) bool { return true },
		HTTPGet:     func(context.Context, string) error { return nil },
	}
	return e, sys, dk
}

func byTitle(results []Result) map[string]Result {
	m := map[string]Result{}
	for _, r := range results {
		m[r.Title] = r
	}
	return m
}

func TestHealthyNode(t *testing.T) {
	e, _, _ := healthy()
	results := Run(context.Background(), e)
	for _, r := range results {
		if r.Status == Fail || r.Status == Warn {
			t.Errorf("%s: %s %s", r.Title, r.Status, r.Detail)
		}
		if r.ID == "" || r.Title == "" {
			t.Errorf("result without an ID or title: %+v", r)
		}
	}
	if Failed(results) {
		t.Error("a healthy node failed")
	}
	got := byTitle(results)
	if got["Panel"].Status != Skip || got["Pterodactyl"].Status != Skip {
		t.Errorf("panel %+v, pterodactyl %+v: should be skipped", got["Panel"], got["Pterodactyl"])
	}
}

// Every problem is reported with why it matters and how to fix it.
func TestProblems(t *testing.T) {
	tests := []struct {
		title  string
		status string
		break_ func(e *Env, s *fakeSystem, d *fakeDocker)
	}{
		{"Operating system", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.files["/etc/os-release"] = "ID=centos\nVERSION_ID=\"7\"\n"
		}},
		{"cgroups v2", Fail, func(_ *Env, s *fakeSystem, _ *fakeDocker) { delete(s.exists, "/sys/fs/cgroup/cgroup.controllers") }},
		{"raptor-wings.service", Fail, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.cmds["systemctl is-active raptor-wings.service"] = "inactive\n"
		}},
		{"raptor-shutdown.service", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.cmds["systemctl is-enabled raptor-shutdown.service"] = "disabled\n"
		}},
		{"Config file", Fail, func(e *Env, _ *fakeSystem, _ *fakeDocker) { e.ConfigErr = errors.New("limits: bad size") }},
		{"Config file", Warn, func(e *Env, s *fakeSystem, _ *fakeDocker) {
			e.ConfigPath = "/etc/raptor/config.yml"
			e.Config.Notifications = []config.Notification{{Type: "discord", URL: "https://discord.com/api/webhooks/1/x"}}
			s.modes = map[string]fs.FileMode{e.ConfigPath: 0o644}
		}},
		{"Wings", Fail, func(e *Env, _ *fakeSystem, _ *fakeDocker) { e.Status, e.StatusErr = nil, errors.New("no such file") }},
		{"Wings", Warn, func(e *Env, _ *fakeSystem, _ *fakeDocker) { e.Status.Servers = nil }},
		{"Docker", Fail, func(e *Env, _ *fakeSystem, _ *fakeDocker) {
			e.Docker, e.DockerErr = nil, errors.New("connection refused")
		}},
		{"Docker", Warn, func(_ *Env, _ *fakeSystem, d *fakeDocker) { d.info.Version = "20.10.24" }},
		{"Docker live-restore", Fail, func(_ *Env, _ *fakeSystem, d *fakeDocker) { d.info.LiveRestore = false }},
		{"Docker userland-proxy", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) { s.files["/etc/docker/daemon.json"] = `{}` }},
		{"Docker cgroup driver", Warn, func(_ *Env, _ *fakeSystem, d *fakeDocker) { d.info.CgroupDriver = "cgroupfs" }},
		{"Server data volume", Fail, func(e *Env, _ *fakeSystem, _ *fakeDocker) {
			e.VolumeCheck = func() error { return errors.New("not mounted with project quotas") }
		}},
		{"Server data volume", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) { s.free["/var/lib/raptor/volumes"] = 5 << 30 }},
		{"Host disk", Fail, func(_ *Env, s *fakeSystem, _ *fakeDocker) { s.free["/var/lib/docker"] = 3 << 30 }},
		{"Host disk", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) { s.free["/var/lib/raptor"] = 15 << 30 }},
		{"Clock", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.cmds["timedatectl show -p NTPSynchronized --value"] = "no\n"
		}},
		{"Firewall rules", Fail, func(e *Env, _ *fakeSystem, _ *fakeDocker) { e.Firewall = func(context.Context) bool { return false } }},
		{"Panel", Fail, func(e *Env, s *fakeSystem, _ *fakeDocker) {
			e.Config.NodeID = "node-1"
			s.exists[e.Config.Identity.Key] = true
			e.HTTPGet = func(context.Context, string) error { return errors.New("no such host") }
		}},
		{"Panel", Fail, func(e *Env, _ *fakeSystem, _ *fakeDocker) { e.Config.NodeID = "node-1" }}, // key missing
		{"Panel", Fail, func(e *Env, s *fakeSystem, _ *fakeDocker) {
			e.Config.NodeID = "node-1"
			s.exists[e.Config.Identity.Key] = true
			e.Status.Connection = &localv1.ConnectionStatus{State: "disconnected", LastError: "the Panel refused the connection: this node's key was revoked in the Panel"}
		}},
		{"Panel", Warn, func(e *Env, s *fakeSystem, _ *fakeDocker) {
			e.Config.NodeID = "node-1"
			s.exists[e.Config.Identity.Key] = true
			e.Status.Connection = &localv1.ConnectionStatus{State: "connecting"}
		}},
		{"SFTP", Fail, func(_ *Env, s *fakeSystem, _ *fakeDocker) { delete(s.dial, "127.0.0.1:2022") }},
		{"SFTP", Fail, func(_ *Env, s *fakeSystem, _ *fakeDocker) { s.dial["127.0.0.1:2022"] = "SSH-2.0-OpenSSH_9.2" }},
		{"Pterodactyl", Fail, func(_ *Env, s *fakeSystem, d *fakeDocker) {
			s.files["/etc/pterodactyl/config.yml"] = "docker:\n  network:\n    name: pterodactyl_nw\n"
			d.networks["pterodactyl_nw"] = []netip.Prefix{netip.MustParsePrefix("172.30.0.0/16")}
		}},
		{"Pterodactyl", Fail, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.files["/etc/pterodactyl/config.yml"] = "system:\n  sftp:\n    bind_port: 2022\n"
		}},
		{"SSH logins", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.cmds["sshd -T"] = "permitrootlogin yes\npasswordauthentication yes\n"
		}},
		{"Automatic OS updates", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) {
			s.cmds["apt-config dump APT::Periodic::Unattended-Upgrade"] = `APT::Periodic::Unattended-Upgrade "0";`
		}},
		{"Wings updates", Warn, func(_ *Env, s *fakeSystem, _ *fakeDocker) { s.links["/usr/local/bin/raptor"] = "/opt/raptor" }},
	}
	for _, tt := range tests {
		e, s, d := healthy()
		tt.break_(e, s, d)
		got, ok := byTitle(Run(context.Background(), e))[tt.title]
		switch {
		case !ok:
			t.Errorf("%s: no result", tt.title)
		case got.Status != tt.status:
			t.Errorf("%s: %s (%s), want %s", tt.title, got.Status, got.Detail, tt.status)
		case got.Why == "" || got.Fix == "":
			t.Errorf("%s: no why or fix: %+v", tt.title, got)
		}
	}
}

// Without root, checks that need it are skipped, not failed.
func TestNotRoot(t *testing.T) {
	e, s, _ := healthy()
	s.root = false
	if r := byTitle(Run(context.Background(), e))["SSH logins"]; r.Status != Skip {
		t.Errorf("ssh as non-root: %+v", r)
	}
}

func TestPrint(t *testing.T) {
	var b bytes.Buffer
	Print(&b, []Result{
		{Title: "Docker", Status: Pass, Detail: "29.1.0"},
		{Title: "Clock", Status: Warn, Detail: "not synchronized", Why: "because", Fix: "do this\nthen that"},
	})
	want := "✓ Docker                     29.1.0\n! Clock                      not synchronized\n    Why: because\n    Fix: do this\n         then that\n\n1 passed, 1 warnings, 0 failed, 0 skipped\n"
	if b.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", b.String(), want)
	}
}

func TestRedact(t *testing.T) {
	in := `access_key: AKIAABC
"secret_key": "fake-secret-value"
password=hunter2 user=bob
Authorization: Bearer eyJhbGciOi.abc
s3 endpoint https://user:pa55@minio.local/bucket
-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAA
-----END OPENSSH PRIVATE KEY-----
server started on port 25565`
	out := string(Redact([]byte(in)))
	for _, secret := range []string{"AKIAABC", "fake-secret-value", "hunter2", "eyJhbGciOi", "pa55", "b3BlbnNzaC1"} {
		if strings.Contains(out, secret) {
			t.Errorf("%q left in:\n%s", secret, out)
		}
	}
	for _, kept := range []string{"user=bob", "server started on port 25565", "minio.local/bucket"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q removed:\n%s", kept, out)
		}
	}
}

func TestBundle(t *testing.T) {
	e, s, _ := healthy()
	s.cmds["journalctl -u raptor-wings -n 5000 --no-pager -o short-iso"] = "wings started\ntoken=abcdef123\n"
	s.files["/etc/raptor/config.yml"] = "panel:\n  url: https://api.raptorpanel.net\n"
	results := Run(context.Background(), e)
	dir := t.TempDir()
	p, err := Bundle(context.Background(), e, results, "/etc/raptor/config.yml", dir, time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("bundle file: %v, %v", fi, err)
	}
	f, _ := os.Open(p)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	files := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		files[h.Name] = string(b)
	}
	for _, name := range []string{"doctor.txt", "doctor.json", "wings.log", "config.yml", "system/os-release", "docker/daemon.json"} {
		if _, ok := files[name]; !ok {
			t.Errorf("%s missing from the bundle", name)
		}
	}
	if strings.Contains(files["wings.log"], "abcdef123") || !strings.Contains(files["wings.log"], "wings started") {
		t.Errorf("wings.log not redacted:\n%s", files["wings.log"])
	}
	if !strings.Contains(files["system/uname.txt"], "not found") {
		t.Errorf("a missing command should say so: %q", files["system/uname.txt"])
	}
}
