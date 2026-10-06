package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/xena-studios/raptor/install"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/update"
)

// bootstrapCmd turns a fresh box into a linked node
// (docs/ARCHITECTURE.md#enrollment): preflight checks, Docker, the raptor
// user, the binary layout, the systemd units, the config, the server data
// volume, and then `raptor link`. Every step checks before it changes
// anything, so running it again after a failure carries on where it
// stopped.
func bootstrapCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	token := fs.String("token", "", "join token from the Panel (rpt_join_…)")
	panelURL := fs.String("panel", "", "Panel URL (default https://api.raptorpanel.net)")
	storageMode := fs.String("storage", "", `server data: "image" (an XFS image file with hard limits; the default), "disk" (`+"`/var/lib/raptor/volumes`"+` is already an XFS disk with prjquota), or "soft" (no quotas)`)
	size := fs.String("size", "", `size of the volume image (with -storage image), e.g. "200GiB"`)
	yes := fs.Bool("yes", false, "don't ask: restart Docker even with other containers running, and take the defaults")
	force := fs.Bool("force", false, "continue on an operating system Raptor doesn't support")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b := &bootstrap{
		token: *token, panelURL: *panelURL, storage: *storageMode, size: *size, yes: *yes, force: *force,
		interactive: !*yes && term.IsTerminal(int(os.Stdin.Fd())), in: bufio.NewReader(os.Stdin), out: os.Stdout,
	} //nolint:gosec // a file descriptor
	return b.run(ctx)
}

type bootstrap struct {
	token, panelURL, storage, size string
	yes, force, interactive        bool
	in                             *bufio.Reader
	out                            io.Writer
	cfg                            config.Config
}

func (b *bootstrap) step(format string, a ...any) {
	_, _ = fmt.Fprintf(b.out, "==> "+format+"\n", a...)
}

func (b *bootstrap) note(format string, a ...any) {
	_, _ = fmt.Fprintf(b.out, "    "+format+"\n", a...)
}

func (b *bootstrap) ask(question, def string) string {
	if !b.interactive {
		return def
	}
	_, _ = fmt.Fprintf(b.out, "    %s [%s] ", question, def)
	line, _ := b.in.ReadString('\n')
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

func (b *bootstrap) run(ctx context.Context) error {
	if b.token == "" {
		return errors.New("usage: raptor bootstrap -token rpt_join_… (the Panel shows the full command under Add Node)")
	}
	if os.Geteuid() != 0 {
		return errors.New("raptor bootstrap must run as root")
	}
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"Checking the box", b.preflight},
		{"Installing packages", b.packages},
		{"Docker", b.docker},
		{"The raptor user", b.user},
		{"Installing raptor " + buildinfo.Version, b.binary},
		{"Config", b.config},
		{"Server data volume", b.volume},
		{"Services", b.services},
	}
	for _, s := range steps {
		b.step("%s", s.name)
		if err := s.fn(ctx); err != nil {
			return fmt.Errorf("%s: %w", strings.ToLower(s.name[:1])+s.name[1:], err)
		}
	}
	b.step("Linking to the Panel")
	if b.cfg.NodeID != "" {
		b.note("already linked as %s", b.cfg.NodeID)
		return nil
	}
	return doLink(ctx, linkOptions{token: b.token, panelURL: b.panelURL, cfgPath: config.DefaultPath})
}

// Supported systems (docs/WINGS.md#doctor).
var supportedOS = map[string][]string{"debian": {"12", "13"}, "ubuntu": {"24.04"}}

func osRelease() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = strings.Trim(v, `"`)
		}
	}
	return out
}

func (b *bootstrap) preflight(context.Context) error {
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return errors.New("systemd isn't running; Raptor needs it")
	}
	for _, f := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(f); err == nil {
			return errors.New("this is a container; Raptor runs on a VM or a physical box")
		}
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("architecture %s isn't supported (amd64 and arm64 are)", runtime.GOARCH)
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		return errors.New("cgroups v2 isn't enabled; Raptor needs it for resource limits")
	}
	rel := osRelease()
	name := rel["PRETTY_NAME"]
	if !contains(supportedOS[rel["ID"]], rel["VERSION_ID"]) {
		if !b.force {
			return fmt.Errorf("%s isn't supported yet (Debian 12 and 13, Ubuntu 24.04 are); -force continues anyway", name)
		}
		b.note("%s isn't supported; continuing because of -force", name)
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return errors.New("apt-get isn't available")
	}
	b.note("%s, %s, systemd, cgroups v2", name, runtime.GOARCH)
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (b *bootstrap) sh(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed commands from this file
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail)
	}
	return nil
}

func installed(ctx context.Context, pkg string) bool {
	out, err := exec.CommandContext(ctx, "dpkg-query", "-W", "-f=${Status}", pkg).Output() //nolint:gosec // package names from this file
	return err == nil && strings.Contains(string(out), "install ok installed")
}

// Packages Wings needs besides Docker: XFS tools for the volume, nftables
// for the firewall, and what adding Docker's repository takes.
var basePackages = []string{"ca-certificates", "curl", "gnupg", "xfsprogs", "nftables"}

func (b *bootstrap) packages(ctx context.Context) error {
	var missing []string
	for _, p := range basePackages {
		if !installed(ctx, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		b.note("already installed")
		return nil
	}
	b.note("installing %s", strings.Join(missing, ", "))
	if err := b.sh(ctx, "apt-get", "update", "-q"); err != nil {
		return err
	}
	return b.sh(ctx, "apt-get", append([]string{"install", "-y", "-q", "--no-install-recommends"}, missing...)...)
}

// Docker CE from Docker's own repository, held so unattended upgrades can't
// restart it (docs/WINGS.md#docker-configuration-merged-by-installer).
var dockerPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io"}

func (b *bootstrap) docker(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		rel := osRelease()
		distro, codename := rel["ID"], rel["VERSION_CODENAME"]
		if distro != "debian" && distro != "ubuntu" {
			return fmt.Errorf("install Docker yourself on %s, then run bootstrap again", rel["PRETTY_NAME"])
		}
		b.note("installing Docker CE from download.docker.com")
		if err := b.sh(ctx, "install", "-m", "0755", "-d", "/etc/apt/keyrings"); err != nil {
			return err
		}
		if err := b.sh(ctx, "curl", "-fsSL", "-o", "/etc/apt/keyrings/docker.asc", "https://download.docker.com/linux/"+distro+"/gpg"); err != nil {
			return err
		}
		arch := runtime.GOARCH
		list := fmt.Sprintf("deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n", arch, distro, codename)
		if err := os.WriteFile("/etc/apt/sources.list.d/docker.list", []byte(list), 0o644); err != nil { //nolint:gosec // apt reads it
			return err
		}
		if err := b.sh(ctx, "apt-get", "update", "-q"); err != nil {
			return err
		}
		args := append([]string{"install", "-y", "-q", "--no-install-recommends"}, dockerPackages...)
		if err := b.sh(ctx, "apt-get", args...); err != nil {
			return err
		}
	}
	if err := b.sh(ctx, "systemctl", "enable", "--now", "docker"); err != nil {
		return err
	}
	if installed(ctx, "docker-ce") {
		args := append([]string{"hold"}, dockerPackages...)
		if err := b.sh(ctx, "apt-mark", args...); err != nil {
			return err
		}
	}
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return fmt.Errorf("docker isn't answering: %w", err)
	}
	version := strings.TrimSpace(string(out))
	if major, _, _ := strings.Cut(version, "."); atoi(major) < 24 {
		return fmt.Errorf("Docker %s is too old (24 or newer)", version) //nolint:staticcheck // Docker is a name
	}
	b.note("Docker %s", version)
	return b.daemonJSON(ctx)
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// dockerSettings are merged into /etc/docker/daemon.json, never replacing
// the file.
var dockerSettings = map[string]any{
	"live-restore":     true,
	"userland-proxy":   false,
	"log-driver":       "local",
	"log-opts":         map[string]any{"max-size": "20m", "max-file": "3"},
	"shutdown-timeout": 90,
}

const daemonJSON = "/etc/docker/daemon.json"

// mergeDaemonJSON adds Raptor's settings to an existing daemon.json and says
// whether anything changed and whether that needs Docker restarted (a
// reload doesn't apply userland-proxy or the log driver).
func mergeDaemonJSON(existing []byte) (out []byte, changed, restart bool, err error) {
	cur := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		if err := json.Unmarshal(existing, &cur); err != nil {
			return nil, false, false, fmt.Errorf("%s isn't valid JSON: %w", daemonJSON, err)
		}
	}
	for k, v := range dockerSettings {
		was, ok := cur[k]
		wb, _ := json.Marshal(was)
		vb, _ := json.Marshal(v)
		if ok && bytes.Equal(wb, vb) {
			continue
		}
		cur[k] = v
		changed = true
		if k != "live-restore" && k != "shutdown-timeout" {
			restart = true
		}
	}
	out, err = json.MarshalIndent(cur, "", "  ")
	return append(out, '\n'), changed, restart, err
}

func (b *bootstrap) daemonJSON(ctx context.Context) error {
	existing, err := os.ReadFile(daemonJSON)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	merged, changed, restart, err := mergeDaemonJSON(existing)
	if err != nil {
		return err
	}
	if !changed {
		b.note("daemon.json already has Raptor's settings")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(daemonJSON), 0o755); err != nil { //nolint:gosec // Docker's directory
		return err
	}
	if len(existing) > 0 {
		if err := os.WriteFile(daemonJSON+".before-raptor", existing, 0o644); err != nil { //nolint:gosec // as Docker's
			return err
		}
	}
	if err := os.WriteFile(daemonJSON, merged, 0o644); err != nil { //nolint:gosec // Docker reads it
		return err
	}
	b.note("merged live-restore, userland-proxy off, log rotation into %s", daemonJSON)
	if !restart {
		return b.sh(ctx, "systemctl", "reload", "docker")
	}
	out, _ := exec.CommandContext(ctx, "docker", "ps", "-q").Output()
	if n := len(strings.Fields(string(out))); n > 0 && !b.yes {
		if a := strings.ToLower(b.ask(fmt.Sprintf("%d container(s) are running; restarting Docker stops them. Restart now?", n), "n")); a != "y" && a != "yes" {
			b.note("not restarted: run `systemctl restart docker` when it suits (raptor doctor reminds you)")
			return b.sh(ctx, "systemctl", "reload", "docker")
		}
	}
	return b.sh(ctx, "systemctl", "restart", "docker")
}

func (b *bootstrap) user(ctx context.Context) error {
	if err := exec.CommandContext(ctx, "getent", "group", "raptor").Run(); err != nil {
		if err := b.sh(ctx, "groupadd", "--system", "raptor"); err != nil {
			return err
		}
	}
	if err := exec.CommandContext(ctx, "id", "raptor").Run(); err != nil {
		if err := b.sh(ctx, "useradd", "--system", "--gid", "raptor", "--no-create-home", "--shell", "/usr/sbin/nologin", "raptor"); err != nil {
			return err
		}
	}
	b.note("raptor user and group")
	return nil
}

// binary puts the running binary in the self-update layout, unless the
// install script already did.
func (b *bootstrap) binary(context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	l := update.Layout{Dir: update.DefaultDir}
	if !l.Holds(exe) {
		if err := os.MkdirAll(l.Dir, 0o755); err != nil { //nolint:gosec // binaries are world-readable
			return err
		}
		dst := l.Binary(buildinfo.Version)
		if err := copyFile(exe, dst+".tmp", 0o755); err != nil {
			return err
		}
		if err := os.Rename(dst+".tmp", dst); err != nil {
			return err
		}
	}
	if cur, err := l.Current(); err != nil || cur == "" {
		if err := l.Promote(buildinfo.Version); err != nil {
			return err
		}
	}
	link := "/usr/local/bin/raptor"
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(l.Dir, "current") {
		_ = os.Remove(link)
		if err := os.Symlink(filepath.Join(l.Dir, "current"), link); err != nil {
			return err
		}
	}
	b.note("%s → %s/current", link, l.Dir)
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // our own executable
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode) //nolint:gosec // root-owned layout
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// newConfig is the config a new node starts with: everything at its default,
// so only what's set here is in the file.
const newConfig = `# Raptor Wings config (docs/WINGS.md#config-file). Written by raptor bootstrap;
# box-level settings only. Servers, schedules, and backups come from the Panel.
log:
  level: info
`

func (b *bootstrap) config(context.Context) error {
	if err := os.MkdirAll(filepath.Dir(config.DefaultPath), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(config.DefaultPath); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(config.DefaultPath, []byte(newConfig), 0o600); err != nil {
			return err
		}
		b.note("wrote %s", config.DefaultPath)
	}
	cfg, err := config.Load(config.DefaultPath)
	if err != nil {
		return err
	}
	b.cfg = cfg
	return nil
}

// volume sets up the server data volume (docs/WINGS.md#disk-quotas).
func (b *bootstrap) volume(ctx context.Context) error {
	vol := b.cfg.Paths.Volumes
	if m, ok, err := storage.FindMount(vol); err != nil {
		return err
	} else if ok && m.FSType == "xfs" && m.HasProjectQuota() {
		b.note("%s is XFS with project quotas (%s)", vol, m.Source)
		return nil
	} else if ok {
		return fmt.Errorf("%s is mounted (%s from %s) without XFS project quotas; mount it with prjquota, or unmount it and run bootstrap again", vol, m.FSType, m.Source)
	}
	if !b.cfg.Storage.Quotas {
		b.note("storage.quotas is off: soft limits")
		return nil
	}
	mode := b.storage
	if mode == "" {
		b.note("Server data needs disk limits. Options:")
		b.note("  image: an XFS image file on this disk, with hard limits. Game saves (fsync'd writes)")
		b.note("         run at about half speed; everything else at full speed. The default.")
		b.note("  disk:  mount an XFS disk with prjquota at %s first, for full speed.", vol)
		b.note("  soft:  no image; usage is checked every 5 minutes instead of enforced.")
		mode = b.ask("Which?", "image")
	}
	switch mode {
	case "disk":
		return fmt.Errorf("%s isn't mounted: mount your XFS data disk there with prjquota (in /etc/fstab), then run bootstrap again", vol)
	case "soft":
		if err := config.SetQuotas(config.DefaultPath, false); err != nil {
			return err
		}
		b.cfg.Storage.Quotas = false
		b.note("soft limits (storage.quotas: false)")
		return nil
	case "image":
	default:
		return fmt.Errorf("unknown -storage %q (image, disk, or soft)", mode)
	}
	size := b.size
	if size == "" {
		size = b.ask("Image size?", gibString(suggestedSize(vol, int64(b.cfg.Limits.HostDiskMinFree))))
	}
	n, err := config.ParseByteSize(size)
	if err != nil {
		return err
	}
	return storageSetup(ctx, &b.cfg, int64(n))
}

// suggestedSize is three quarters of what the host can spare, in whole GiB.
func suggestedSize(vol string, minFree int64) int64 {
	dir := filepath.Dir(vol)
	for {
		if _, err := os.Stat(dir); err == nil || dir == "/" {
			break
		}
		dir = filepath.Dir(dir)
	}
	_, free, err := storage.Space(dir)
	if err != nil {
		return minVolume
	}
	spare := (free - minFree) * 3 / 4
	spare -= spare % (1 << 30)
	return max(spare, minVolume)
}

func gibString(n int64) string { return strconv.FormatInt(n>>30, 10) + "GiB" }

func (b *bootstrap) services(ctx context.Context) error {
	units := map[string]string{
		"systemd/raptor-wings.service":           "/etc/systemd/system/raptor-wings.service",
		"systemd/raptor-shutdown.service":        "/etc/systemd/system/raptor-shutdown.service",
		"systemd/docker-.scope.d/10-raptor.conf": "/etc/systemd/system/docker-.scope.d/10-raptor.conf",
	}
	changed := false
	for src, dst := range units {
		data, err := install.Systemd.ReadFile(src)
		if err != nil {
			return err
		}
		if cur, err := os.ReadFile(dst); err == nil && bytes.Equal(cur, data) { //nolint:gosec // fixed paths
			continue
		}
		changed = true
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // systemd's directory
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil { //nolint:gosec // systemd reads units
			return err
		}
	}
	// Restart Wings only for new units; otherwise start it if it isn't
	// running (a rerun shouldn't bounce a working node). Servers keep
	// running either way.
	wings := []string{"start", "raptor-wings"}
	if changed {
		wings[0] = "restart"
	}
	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", "raptor-wings", "raptor-shutdown"},
		{"start", "raptor-shutdown"},
		wings,
	} {
		if err := b.sh(ctx, "systemctl", args...); err != nil {
			return err
		}
	}
	b.note("raptor-wings and raptor-shutdown enabled and started")
	return nil
}
