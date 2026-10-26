package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/update"
)

// uninstallCmd takes Raptor off the box (docs/WINGS.md#uninstall): it stops
// the servers gracefully, then removes what bootstrap and Wings put on the
// system. Server files, backups, databases, and Wings' state stay in
// /var/lib/raptor unless -wipe-data is given. Every step carries on after a
// failure, so running it again finishes a half-removed install.
func uninstallCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	cfgPath := fs.String("config", config.DefaultPath, "Wings config file")
	wipe := fs.Bool("wipe-data", false, "also delete every server's files, backups on this box, databases, and logs")
	yes := fs.Bool("yes", false, "don't ask")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("raptor uninstall must run as root")
	}
	cfg, err := config.Load(*cfgPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg = config.Default()
	case err != nil:
		return fmt.Errorf("%w (fix it, or move it away to uninstall with the default paths)", err)
	}
	u := newUninstaller(cfg, *cfgPath, *wipe)
	if !*yes {
		if !term.IsTerminal(int(os.Stdin.Fd())) { //nolint:gosec // a file descriptor
			return errors.New("nothing to confirm with: run it in a terminal, or add -yes")
		}
		if !u.confirm(bufio.NewReader(os.Stdin)) {
			return errors.New("not uninstalled")
		}
	}
	return u.run(ctx)
}

type uninstaller struct {
	cfg     config.Config
	cfgDir  string // /etc/raptor
	wipe    bool
	out     io.Writer
	unitDir string // /etc/systemd/system
	libDir  string // /usr/local/lib/raptor
	binLink string // /usr/local/bin/raptor
	// mountinfo lists what's mounted (/proc/self/mountinfo).
	mountinfo string
	// sh runs a command and returns its combined output.
	sh       func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath func(string) (string, error)
	errs     []error
}

func newUninstaller(cfg config.Config, cfgPath string, wipe bool) *uninstaller {
	return &uninstaller{
		cfg: cfg, cfgDir: filepath.Dir(cfgPath), wipe: wipe, out: os.Stdout,
		unitDir: "/etc/systemd/system", libDir: update.DefaultDir, binLink: "/usr/local/bin/raptor",
		mountinfo: "/proc/self/mountinfo", lookPath: exec.LookPath,
		sh: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed commands from this file
		},
	}
}

func (u *uninstaller) stateDir() string { return filepath.Dir(u.cfg.Paths.State) }

// image is the volume image raptor storage setup makes.
func (u *uninstaller) image() string { return u.cfg.Paths.Volumes + ".xfs" }

func (u *uninstaller) confirm(in *bufio.Reader) bool {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(u.out, format+"\n", a...) }
	p("This takes Raptor off this machine:")
	p("  - stops every server gracefully, then removes Raptor's containers and networks")
	p("  - removes the raptor-wings and raptor-shutdown services, Raptor's firewall")
	p("    rules, and the mounts of its data volume and disks")
	p("  - removes the raptor command (%s) and its config and keys (%s)", u.libDir, u.cfgDir)
	if u.wipe {
		p("  - DELETES every server's files, backups on this box, and databases (%s),", u.stateDir())
		p("    and Raptor's logs (%s). This can't be undone.", u.cfg.Paths.Logs)
	} else {
		p("Server files, backups on this box, and databases stay in %s.", u.stateDir())
	}
	p("Docker stays installed.")
	if u.cfg.NodeID != "" {
		p("")
		p("This node is still on the Panel (%s). Remove it in the app, on the node's", u.cfg.NodeID)
		p("Settings tab, so it stops being billed; uninstalling doesn't tell the Panel.")
	}
	p("")
	want := "y"
	if u.wipe {
		want = "delete"
		_, _ = fmt.Fprint(u.out, `Type "delete" to uninstall and delete the data: `)
	} else {
		_, _ = fmt.Fprint(u.out, "Uninstall Raptor? [y/N] ")
	}
	line, _ := in.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == want || (want == "y" && a == "yes")
}

func (u *uninstaller) step(format string, a ...any) {
	_, _ = fmt.Fprintf(u.out, "==> "+format+"\n", a...)
}

func (u *uninstaller) note(format string, a ...any) {
	_, _ = fmt.Fprintf(u.out, "    "+format+"\n", a...)
}

// try runs a command, recording a failure and carrying on.
func (u *uninstaller) try(ctx context.Context, name string, args ...string) bool {
	out, err := u.sh(ctx, name, args...)
	if err != nil {
		u.fail(fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, bytes.TrimSpace(out)))
		return false
	}
	return true
}

func (u *uninstaller) fail(err error) {
	u.errs = append(u.errs, err)
	u.note("✗ %v", err)
}

func (u *uninstaller) run(ctx context.Context) error {
	u.stopServers(ctx)
	u.services(ctx)
	u.containers(ctx)
	u.firewall(ctx)
	u.mounts(ctx)
	if u.wipe {
		u.wipeData(ctx)
	}
	if len(u.errs) > 0 {
		u.step("Not finished")
		u.note("Some steps failed (above). Fix what they say and run raptor uninstall again;")
		u.note("it carries on where it stopped. The raptor command and its config stay until then.")
		return fmt.Errorf("%d step(s) failed", len(u.errs))
	}
	u.files()
	u.step("Done")
	if len(u.errs) > 0 {
		return fmt.Errorf("%d step(s) failed", len(u.errs))
	}
	u.note("Raptor is uninstalled.")
	if !u.wipe {
		u.note("Server files, backups, and databases are in %s.", u.stateDir())
		if _, err := os.Stat(u.image()); err == nil {
			u.note("Server files are inside the volume image; to read them:")
			u.note("  mkdir -p /mnt/raptor && mount -o loop,ro %s /mnt/raptor", u.image())
		}
		u.note("To delete them: rm -rf %s", u.stateDir())
	}
	return nil
}

// stopServers stops every server with its egg's stop command, through
// raptor-shutdown's stop (raptor wings shutdown-servers), while Wings still
// runs. Containers it couldn't stop are stopped by Docker below.
func (u *uninstaller) stopServers(ctx context.Context) {
	u.step("Stopping servers")
	if !u.unitExists("raptor-shutdown.service") {
		u.note("raptor-shutdown isn't installed")
		return
	}
	if out, err := u.sh(ctx, "systemctl", "stop", "raptor-shutdown"); err != nil {
		u.note("not every server stopped gracefully; Docker stops the rest (%s)", bytes.TrimSpace(out))
		return
	}
	u.note("stopped")
}

var raptorUnits = []string{"raptor-wings.service", "raptor-shutdown.service"}

const scopeDropIn = "docker-.scope.d/10-raptor.conf"

func (u *uninstaller) unitExists(name string) bool {
	_, err := os.Stat(filepath.Join(u.unitDir, name))
	return err == nil
}

func (u *uninstaller) services(ctx context.Context) {
	u.step("Services")
	removed := false
	for _, unit := range raptorUnits {
		if !u.unitExists(unit) {
			continue
		}
		if u.try(ctx, "systemctl", "disable", "--now", unit) {
			u.remove(filepath.Join(u.unitDir, unit))
			removed = true
		}
	}
	if u.unitExists(scopeDropIn) {
		u.remove(filepath.Join(u.unitDir, scopeDropIn))
		_ = os.Remove(filepath.Join(u.unitDir, filepath.Dir(scopeDropIn))) // only if empty
		removed = true
	}
	if !removed {
		u.note("none installed")
		return
	}
	u.try(ctx, "systemctl", "daemon-reload")
	u.note("raptor-wings and raptor-shutdown stopped and removed")
}

// containers removes every container and network Wings made (they all carry
// docker.LabelManaged); Docker's other containers aren't touched.
func (u *uninstaller) containers(ctx context.Context) {
	u.step("Containers and networks")
	if _, err := u.lookPath("docker"); err != nil {
		u.note("Docker isn't installed")
		return
	}
	filter := "label=" + docker.LabelManaged + "=true"
	out, err := u.sh(ctx, "docker", "ps", "-aq", "--filter", filter)
	if err != nil {
		u.fail(fmt.Errorf("docker ps: %w: %s", err, bytes.TrimSpace(out)))
		return
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		// Stopping first gives anything raptor-shutdown missed its SIGTERM.
		u.try(ctx, "docker", append([]string{"stop", "-t", "60"}, ids...)...)
		if u.try(ctx, "docker", append([]string{"rm", "-f"}, ids...)...) {
			u.note("removed %d container(s)", len(ids))
		}
	}
	out, err = u.sh(ctx, "docker", "network", "ls", "-q", "--filter", filter)
	if err != nil {
		u.fail(fmt.Errorf("docker network ls: %w: %s", err, bytes.TrimSpace(out)))
		return
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		if u.try(ctx, "docker", append([]string{"network", "rm"}, ids...)...) {
			u.note("removed %d network(s)", len(ids))
		}
	}
	if held, _ := u.sh(ctx, "apt-mark", "showhold"); slices.Contains(strings.Fields(string(held)), "docker-ce") {
		// Bootstrap held Docker so upgrades couldn't restart it under the
		// games; with Raptor gone it updates like everything else.
		if u.try(ctx, "apt-mark", append([]string{"unhold"}, dockerPackages...)...) {
			u.note("Docker's packages update with the system again (apt-mark unhold)")
		}
	}
}

// firewall removes nftables' inet raptor table and the RAPTOR-BETWEEN
// chains in iptables' raw and filter tables, with their jumps.
func (u *uninstaller) firewall(ctx context.Context) {
	u.step("Firewall")
	if _, err := u.sh(ctx, "nft", "list", "table", "inet", firewall.Table); err == nil {
		if u.try(ctx, "nft", "delete", "table", "inet", firewall.Table) {
			u.note("removed nftables table inet %s; its rules for this machine's own ports are gone too", firewall.Table)
		}
	}
	chain := firewall.DockerUserChain
	jumps := [][]string{{"-t", "raw", "-D", "PREROUTING", "-j", chain}, {"-t", "filter", "-D", "DOCKER-USER", "-j", chain}}
	for _, jump := range jumps {
		for i := 0; i < 10; i++ { // every copy, but never forever
			if _, err := u.sh(ctx, "iptables", append([]string{"-w"}, jump...)...); err != nil {
				break
			}
		}
	}
	for _, table := range []string{"raw", "filter"} {
		if _, err := u.sh(ctx, "iptables", "-w", "-t", table, "-n", "-L", chain); err != nil {
			continue
		}
		if u.try(ctx, "iptables", "-w", "-t", table, "-F", chain) && u.try(ctx, "iptables", "-w", "-t", table, "-X", chain) {
			u.note("removed iptables chain %s (%s)", chain, table)
		}
	}
}

// mountMarker is in every mount unit Wings writes (the volume and data
// disks), and in nothing else.
const mountMarker = "Written by Wings."

// mounts unmounts the volume and data disks and removes their units. The
// image and the disks keep their files.
func (u *uninstaller) mounts(ctx context.Context) {
	u.step("Data volume and disks")
	units, err := raptorMountUnits(u.unitDir)
	if err != nil {
		u.fail(err)
		return
	}
	if len(units) == 0 {
		u.note("no Raptor mounts")
		return
	}
	for _, unit := range units {
		if u.try(ctx, "systemctl", "disable", "--now", unit) {
			u.remove(filepath.Join(u.unitDir, unit))
			u.note("unmounted and removed %s", unit)
		}
	}
	u.try(ctx, "systemctl", "daemon-reload")
}

func raptorMountUnits(dir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.mount"))
	if err != nil {
		return nil, err
	}
	var units []string
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // systemd's unit directory
		if err != nil {
			return nil, err
		}
		if bytes.Contains(data, []byte(mountMarker)) {
			units = append(units, filepath.Base(p))
		}
	}
	return units, nil
}

// files removes the command, its config and keys, and the socket's
// directory. It runs last, once everything else worked, so a failed
// uninstall can be run again.
func (u *uninstaller) files() {
	u.step("The raptor command and its config")
	if target, err := os.Readlink(u.binLink); err == nil && strings.HasPrefix(target, u.libDir) {
		u.remove(u.binLink)
	}
	for _, dir := range []string{u.libDir, u.cfgDir, filepath.Dir(u.cfg.Paths.Socket)} {
		u.removeAll(dir)
	}
	u.note("removed %s, %s, and %s", u.binLink, u.libDir, u.cfgDir)
}

// wipeData deletes the data directories, the image, and the raptor user.
// Anything still mounted inside them is left alone, so a disk that didn't
// unmount never loses its files.
func (u *uninstaller) wipeData(ctx context.Context) {
	u.step("Deleting data")
	dirs := []string{u.stateDir(), u.cfg.Paths.Volumes, u.cfg.Paths.Backups, u.cfg.Paths.Tmp, u.cfg.Paths.Logs}
	mounted, err := mountpoints(u.mountinfo)
	if err != nil {
		u.fail(err)
		return
	}
	for _, dir := range dirs {
		if busy := mountedUnder(mounted, dir); busy != "" {
			u.fail(fmt.Errorf("%s is still mounted, so %s wasn't deleted", busy, dir))
			continue
		}
		u.removeAll(dir)
	}
	u.remove(u.image())
	if _, err := u.sh(ctx, "id", "raptor"); err == nil {
		u.try(ctx, "userdel", "raptor")
	}
	if _, err := u.sh(ctx, "getent", "group", "raptor"); err == nil {
		u.try(ctx, "groupdel", "raptor")
	}
	u.note("deleted %s", strings.Join(dirs, ", "))
}

func (u *uninstaller) remove(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		u.fail(err)
	}
}

// removeAll deletes a directory Raptor owns. It refuses anything outside a
// directory named raptor, so a config pointing at /var or /srv can't take
// the system with it.
func (u *uninstaller) removeAll(dir string) {
	if !deletable(dir) {
		u.fail(fmt.Errorf("not deleting %s: it isn't inside a directory named raptor; delete it yourself if it's Raptor's", dir))
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		u.fail(err)
	}
}

// deletable reports whether dir is absolute, at least two levels deep, and
// is or is inside a directory named raptor (/etc/raptor,
// /var/lib/raptor/volumes; not /var/lib or /raptor).
func deletable(dir string) bool {
	if !filepath.IsAbs(dir) {
		return false
	}
	parts := strings.Split(strings.Trim(filepath.Clean(dir), "/"), "/")
	return len(parts) >= 2 && slices.Contains(parts, "raptor")
}

// mountpoints reads the mount points from a mountinfo file.
func mountpoints(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // /proc/self/mountinfo
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.Lines(string(data)) {
		if f := strings.Fields(line); len(f) > 4 {
			out = append(out, unescapeOctal(f[4]))
		}
	}
	return out, nil
}

// unescapeOctal undoes mountinfo's \040-style escapes.
func unescapeOctal(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if c, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(c))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mountedUnder returns a mount point at or inside dir, or "".
func mountedUnder(mounts []string, dir string) string {
	dir = filepath.Clean(dir)
	for _, m := range mounts {
		if m == dir || strings.HasPrefix(m, dir+"/") {
			return m
		}
	}
	return ""
}
