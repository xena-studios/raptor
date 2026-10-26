package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/config"
)

// fakeBox is a node's files under a temporary root, and a command runner
// that records what it was asked to run.
type fakeBox struct {
	root string
	u    *uninstaller
	ran  []string
	// fail makes commands starting with a prefix fail.
	fail []string
	// deleted iptables jumps, so the loop sees them go.
	jumps map[string]int
}

func newFakeBox(t *testing.T, wipe bool) *fakeBox {
	t.Helper()
	root := t.TempDir()
	b := &fakeBox{root: root, jumps: map[string]int{"PREROUTING": 1, "DOCKER-USER": 1}}
	cfg := config.Default()
	cfg.NodeID = "node-1"
	cfg.Paths.State = b.path("var/lib/raptor/state.db")
	cfg.Paths.Volumes = b.path("var/lib/raptor/volumes")
	cfg.Paths.Backups = b.path("var/lib/raptor/backups")
	cfg.Paths.Tmp = b.path("var/lib/raptor/tmp")
	cfg.Paths.Logs = b.path("var/log/raptor")
	cfg.Paths.Socket = b.path("run/raptor/wings.sock")
	u := newUninstaller(cfg, b.path("etc/raptor/config.yml"), wipe)
	u.out = io.Discard
	u.unitDir = b.path("etc/systemd/system")
	u.libDir = b.path("usr/local/lib/raptor")
	u.binLink = b.path("usr/local/bin/raptor")
	u.mountinfo = b.path("proc/mountinfo")
	u.lookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	u.sh = b.sh
	b.u = u

	b.write(t, "etc/systemd/system/raptor-wings.service", "[Unit]\n")
	b.write(t, "etc/systemd/system/raptor-shutdown.service", "[Unit]\n")
	b.write(t, "etc/systemd/system/docker-.scope.d/10-raptor.conf", "[Unit]\n")
	b.write(t, "etc/systemd/system/var-lib-raptor-volumes.mount", "# Raptor server data volume. Written by Wings.\n")
	b.write(t, "etc/systemd/system/mnt-other.mount", "# The owner's own disk\n")
	b.write(t, "usr/local/lib/raptor/raptor-1.0.0", "binary")
	b.write(t, "etc/raptor/config.yml", "log:\n  level: info\n")
	b.write(t, "etc/raptor/node.key", "key")
	b.write(t, "run/raptor/wings.sock", "")
	b.write(t, "var/lib/raptor/state.db", "state")
	b.write(t, "var/lib/raptor/volumes.xfs", "image")
	b.write(t, "var/log/raptor/wings.log", "log")
	b.write(t, "proc/mountinfo", "22 1 8:1 / / rw - ext4 /dev/sda1 rw\n")
	if err := os.MkdirAll(filepath.Dir(u.binLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(u.libDir, "current"), u.binLink); err != nil {
		t.Fatal(err)
	}
	return b
}

func (b *fakeBox) path(rel string) string { return filepath.Join(b.root, rel) }

func (b *fakeBox) write(t *testing.T, rel, data string) {
	t.Helper()
	p := b.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (b *fakeBox) exists(rel string) bool {
	_, err := os.Lstat(b.path(rel))
	return err == nil
}

func (b *fakeBox) sh(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := strings.Join(append([]string{name}, args...), " ")
	b.ran = append(b.ran, cmd)
	for _, f := range b.fail {
		if strings.HasPrefix(cmd, f) {
			return []byte("boom"), errors.New("exit status 1")
		}
	}
	switch {
	case strings.HasPrefix(cmd, "docker ps"):
		return []byte("c1\nc2\n"), nil
	case strings.HasPrefix(cmd, "docker network ls"):
		return []byte("n1\n"), nil
	case cmd == "apt-mark showhold":
		return []byte("docker-ce\ndocker-ce-cli\ncontainerd.io\n"), nil
	case strings.HasPrefix(cmd, "iptables -w -t raw -D PREROUTING"):
		return b.jump("PREROUTING")
	case strings.HasPrefix(cmd, "iptables -w -t filter -D DOCKER-USER"):
		return b.jump("DOCKER-USER")
	}
	return nil, nil
}

func (b *fakeBox) jump(chain string) ([]byte, error) {
	if b.jumps[chain] == 0 {
		return []byte("Bad rule"), errors.New("exit status 1")
	}
	b.jumps[chain]--
	return nil, nil
}

func (b *fakeBox) ranAll(t *testing.T, cmds ...string) {
	t.Helper()
	for _, c := range cmds {
		if !slices.Contains(b.ran, c) {
			t.Errorf("didn't run %q; ran:\n%s", c, strings.Join(b.ran, "\n"))
		}
	}
}

func TestUninstallKeepsData(t *testing.T) {
	b := newFakeBox(t, false)
	if err := b.u.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.ran[0] != "systemctl stop raptor-shutdown" {
		t.Errorf("servers weren't stopped first: %q", b.ran[0])
	}
	b.ranAll(t,
		"systemctl disable --now raptor-wings.service",
		"systemctl disable --now raptor-shutdown.service",
		"docker ps -aq --filter label=raptor.wings.managed=true",
		"docker stop -t 60 c1 c2",
		"docker rm -f c1 c2",
		"docker network rm n1",
		"apt-mark unhold docker-ce docker-ce-cli containerd.io",
		"nft delete table inet raptor",
		"iptables -w -t raw -F RAPTOR-BETWEEN",
		"iptables -w -t raw -X RAPTOR-BETWEEN",
		"iptables -w -t filter -F RAPTOR-BETWEEN",
		"iptables -w -t filter -X RAPTOR-BETWEEN",
		"systemctl disable --now var-lib-raptor-volumes.mount",
	)
	if slices.Contains(b.ran, "systemctl disable --now mnt-other.mount") {
		t.Error("touched a mount unit Raptor didn't write")
	}
	if b.jumps["PREROUTING"] != 0 || b.jumps["DOCKER-USER"] != 0 {
		t.Errorf("jumps left: %v", b.jumps)
	}
	for _, gone := range []string{
		"etc/systemd/system/raptor-wings.service", "etc/systemd/system/raptor-shutdown.service",
		"etc/systemd/system/docker-.scope.d", "etc/systemd/system/var-lib-raptor-volumes.mount",
		"usr/local/bin/raptor", "usr/local/lib/raptor", "etc/raptor", "run/raptor",
	} {
		if b.exists(gone) {
			t.Errorf("%s is still there", gone)
		}
	}
	for _, kept := range []string{"var/lib/raptor/state.db", "var/lib/raptor/volumes.xfs", "var/log/raptor/wings.log", "etc/systemd/system/mnt-other.mount"} {
		if !b.exists(kept) {
			t.Errorf("%s was deleted", kept)
		}
	}
	if slices.ContainsFunc(b.ran, func(c string) bool { return strings.HasPrefix(c, "userdel") }) {
		t.Error("removed the raptor user without -wipe-data")
	}
}

func TestUninstallWipesData(t *testing.T) {
	b := newFakeBox(t, true)
	if err := b.u.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"var/lib/raptor", "var/log/raptor", "etc/raptor", "usr/local/lib/raptor"} {
		if b.exists(gone) {
			t.Errorf("%s is still there", gone)
		}
	}
	b.ranAll(t, "userdel raptor", "groupdel raptor")
}

// A disk that didn't unmount keeps its files, and the command stays so the
// uninstall can be run again.
func TestUninstallWipeSkipsMounts(t *testing.T) {
	b := newFakeBox(t, true)
	b.write(t, "proc/mountinfo", "22 1 8:1 / / rw - ext4 /dev/sda1 rw\n40 22 8:17 / "+b.path("var/lib/raptor/disks/abc\\040d")+" rw - xfs /dev/sdb rw\n")
	b.write(t, "var/lib/raptor/disks/abc d/world/level.dat", "save")
	if err := b.u.run(context.Background()); err == nil {
		t.Fatal("no error with a disk still mounted")
	}
	if !b.exists("var/lib/raptor/disks/abc d/world/level.dat") {
		t.Error("deleted files on a mounted disk")
	}
	if b.exists("var/log/raptor") {
		t.Error("didn't delete what it could")
	}
	for _, kept := range []string{"usr/local/lib/raptor/raptor-1.0.0", "etc/raptor/config.yml"} {
		if !b.exists(kept) {
			t.Errorf("%s was removed before the uninstall finished", kept)
		}
	}
}

func TestUninstallCarriesOnAfterAFailure(t *testing.T) {
	b := newFakeBox(t, false)
	b.fail = []string{"docker rm"}
	if err := b.u.run(context.Background()); err == nil {
		t.Fatal("no error")
	}
	b.ranAll(t, "nft delete table inet raptor", "systemctl disable --now var-lib-raptor-volumes.mount")
	if !b.exists("usr/local/lib/raptor/raptor-1.0.0") {
		t.Error("removed the command though a step failed")
	}
}

func TestUninstallConfirm(t *testing.T) {
	for _, tc := range []struct {
		wipe   bool
		answer string
		want   bool
	}{
		{false, "y\n", true},
		{false, "yes\n", true},
		{false, "\n", false},
		{true, "y\n", false},
		{true, "delete\n", true},
	} {
		b := newFakeBox(t, tc.wipe)
		if got := b.u.confirm(bufio.NewReader(strings.NewReader(tc.answer))); got != tc.want {
			t.Errorf("wipe=%v answer %q: got %v", tc.wipe, tc.answer, got)
		}
	}
}

func TestDeletable(t *testing.T) {
	for dir, want := range map[string]bool{
		"/etc/raptor":             true,
		"/var/lib/raptor":         true,
		"/var/lib/raptor/volumes": true,
		"/run/raptor/":            true,
		"/var/lib":                false,
		"/raptor":                 false,
		"/":                       false,
		"/srv/games":              false,
		"var/lib/raptor":          false,
	} {
		if got := deletable(dir); got != want {
			t.Errorf("deletable(%q) = %v, want %v", dir, got, want)
		}
	}
}
