package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/wings/host"
)

// Supported systems (docs/ROADMAP.md: the systems the installer is tested
// on).
var supportedOS = map[string][]string{"debian": {"12", "13"}, "ubuntu": {"24.04"}}

// minDocker is the oldest Docker major version Raptor is tested with.
const minDocker = 24

// Checks returns every check, in the order they're shown.
func Checks() []Check {
	return []Check{
		{ID: "os", Title: "Operating system", Run: checkOS},
		{ID: "cgroups", Title: "cgroups v2", Run: checkCgroups},
		{ID: "systemd", Title: "systemd", Run: checkSystemd},
		{ID: "config", Title: "Config file", Run: checkConfig},
		{ID: "wings", Title: "Wings", Run: checkWings},
		{ID: "docker", Title: "Docker", Run: checkDocker},
		{ID: "storage", Title: "Server data volume", Run: checkStorage},
		{ID: "host-disk", Title: "Host disk", Run: checkHostDisk},
		{ID: "clock", Title: "Clock", Run: checkClock},
		{ID: "firewall", Title: "Firewall rules", Run: checkFirewall},
		{ID: "panel", Title: "Panel", Run: checkPanel},
		{ID: "sftp", Title: "SFTP", Run: checkSFTP},
		{ID: "pterodactyl", Title: "Pterodactyl", Run: checkPterodactyl},
		{ID: "ssh", Title: "SSH logins", Run: checkSSH},
		{ID: "os-updates", Title: "Automatic OS updates", Run: checkOSUpdates},
		{ID: "self-update", Title: "Wings updates", Run: checkSelfUpdate},
	}
}

func pass(detail string) []Result { return []Result{{Status: Pass, Detail: detail}} }
func skip(detail string) []Result { return []Result{{Status: Skip, Detail: detail}} }

func warn(detail, why, fix string) []Result {
	return []Result{{Status: Warn, Detail: detail, Why: why, Fix: fix}}
}

func fail(detail, why, fix string) []Result {
	return []Result{{Status: Fail, Detail: detail, Why: why, Fix: fix}}
}

// sub is a result under another title, for checks that report several.
func sub(title string, rs []Result) Result {
	r := rs[0]
	r.Title = title
	return r
}

func checkOS(ctx context.Context, e *Env) []Result {
	b, err := e.System.ReadFile("/etc/os-release")
	if err != nil {
		return skip("can't read /etc/os-release")
	}
	kv := map[string]string{}
	for line := range strings.SplitSeq(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			kv[k] = strings.Trim(v, `"'`)
		}
	}
	name := kv["PRETTY_NAME"]
	if name == "" {
		name = kv["ID"] + " " + kv["VERSION_ID"]
	}
	kernel := ""
	if out, err := e.System.Run(ctx, "uname", "-r"); err == nil {
		kernel = ", kernel " + strings.TrimSpace(string(out))
	}
	detail := fmt.Sprintf("%s%s, %s", name, kernel, runtime.GOARCH)
	if slices.Contains(supportedOS[kv["ID"]], kv["VERSION_ID"]) {
		return pass(detail)
	}
	return warn(detail,
		"Raptor is tested on Debian 12 and 13 and Ubuntu 24.04. Other systems may work, but problems specific to them aren't supported.",
		"Use Debian 12 or 13, or Ubuntu 24.04, for this node.")
}

func checkCgroups(_ context.Context, e *Env) []Result {
	if _, err := e.System.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return pass("unified hierarchy")
	}
	return fail("not in use (cgroups v1)",
		"Wings limits each server's memory and CPU, and keeps game servers from starving the system, with cgroups v2.",
		"Boot with the unified hierarchy: add systemd.unified_cgroup_hierarchy=1 to GRUB_CMDLINE_LINUX in /etc/default/grub, run update-grub, and reboot.")
}

func checkSystemd(ctx context.Context, e *Env) []Result {
	if _, err := e.System.Stat("/run/systemd/system"); err != nil {
		return fail("not running as the init system",
			"Wings, its shutdown hook, and the game servers' resource limits all run under systemd.",
			"Use a system booted with systemd (every supported system is).")
	}
	var out []Result
	if s, _ := e.System.Run(ctx, "systemctl", "is-active", "raptor-wings.service"); strings.TrimSpace(string(s)) == "active" {
		out = append(out, sub("raptor-wings.service", pass("active")))
	} else {
		out = append(out, sub("raptor-wings.service", fail(strings.TrimSpace(string(s)),
			"Wings isn't running, so servers aren't watched, restarted after crashes, backed up, or reachable from the Panel. (Running servers keep running.)",
			"systemctl start raptor-wings; if it stops again: journalctl -u raptor-wings -n 50")))
	}
	if s, _ := e.System.Run(ctx, "systemctl", "is-enabled", "raptor-shutdown.service"); strings.TrimSpace(string(s)) == "enabled" {
		out = append(out, sub("raptor-shutdown.service", pass("enabled")))
	} else {
		out = append(out, sub("raptor-shutdown.service", warn(strings.TrimSpace(string(s)),
			"Without it, servers are killed at shutdown instead of saving and stopping cleanly, and worlds can lose recent changes.",
			"systemctl enable raptor-shutdown.service")))
	}
	return out
}

func checkConfig(_ context.Context, e *Env) []Result {
	if e.ConfigErr != nil {
		return fail(e.ConfigErr.Error(),
			"Wings won't start with a config file it can't read.",
			"Fix the file (see docs/WINGS.md#config-file) and run raptor doctor again.")
	}
	return pass("valid")
}

func checkWings(_ context.Context, e *Env) []Result {
	if e.Status == nil {
		detail := "not answering"
		if e.StatusErr != nil {
			detail += ": " + e.StatusErr.Error()
		}
		return fail(detail,
			"The CLI, and the checks below that ask Wings, need its local API.",
			"systemctl status raptor-wings; journalctl -u raptor-wings -n 50")
	}
	if e.Status.GetServers() == nil {
		return warn(fmt.Sprintf("%s, but the container runtime isn't ready", e.Status.GetVersion()),
			"Until it is, servers can't be started, installed, or restored. Wings retries every minute.",
			"journalctl -u raptor-wings -n 50 shows why (usually Docker isn't reachable).")
	}
	return pass(fmt.Sprintf("%s, %d servers (%d up)", e.Status.GetVersion(), e.Status.GetServers().GetTotal(), e.Status.GetServers().GetUp()))
}

func checkDocker(ctx context.Context, e *Env) []Result {
	if e.Docker == nil {
		detail := "not reachable"
		if e.DockerErr != nil {
			detail += ": " + e.DockerErr.Error()
		}
		return fail(detail, "Game servers run in Docker.", "systemctl start docker; journalctl -u docker -n 50")
	}
	info, err := e.Docker.Info(ctx)
	if err != nil {
		return fail("not reachable: "+err.Error(), "Game servers run in Docker.", "systemctl start docker; journalctl -u docker -n 50")
	}
	var out []Result
	major, _ := strconv.Atoi(strings.SplitN(info.Version, ".", 2)[0])
	if major >= minDocker {
		out = append(out, sub("Docker", pass(info.Version)))
	} else {
		out = append(out, sub("Docker", warn(info.Version,
			fmt.Sprintf("Raptor is tested with Docker %d and later.", minDocker),
			"Update Docker from Docker's own repository (docs.docker.com/engine/install).")))
	}
	if info.LiveRestore {
		out = append(out, sub("Docker live-restore", pass("on")))
	} else {
		out = append(out, sub("Docker live-restore", fail("off",
			"Restarting or updating Docker stops every game server.",
			`Add "live-restore": true to /etc/docker/daemon.json, then systemctl reload docker (a reload doesn't stop containers).`)))
	}
	var daemon map[string]any
	if b, err := e.System.ReadFile("/etc/docker/daemon.json"); err == nil {
		_ = json.Unmarshal(b, &daemon)
	}
	if proxy, ok := daemon["userland-proxy"].(bool); ok && !proxy {
		out = append(out, sub("Docker userland-proxy", pass("off")))
	} else {
		out = append(out, sub("Docker userland-proxy", warn("on",
			"Docker relays every published port through a proxy process, which adds latency and hides players' addresses from UDP game servers.",
			`Add "userland-proxy": false to /etc/docker/daemon.json, then systemctl restart docker (with live-restore on, servers keep running).`)))
	}
	if info.CgroupDriver == "systemd" {
		out = append(out, sub("Docker cgroup driver", pass("systemd")))
	} else {
		out = append(out, sub("Docker cgroup driver", warn(info.CgroupDriver,
			"Containers run outside raptor.slice, so the memory kept free for the system (limits.reserved_memory) doesn't apply to game servers.",
			`Add "exec-opts": ["native.cgroupdriver=systemd"] to /etc/docker/daemon.json, then systemctl restart docker.`)))
	}
	return out
}

func checkStorage(_ context.Context, e *Env) []Result {
	path := e.Config.Paths.Volumes
	if e.VolumeCheck != nil {
		if err := e.VolumeCheck(); err != nil {
			return fail(err.Error(),
				"Servers are refused until the volume is back, so nothing is written to the empty directory underneath (on the host disk, without limits).",
				"raptor storage status explains what's wrong. A volume that isn't mounted: systemctl start the mount unit it names, or raptor storage setup on a new node.")
		}
	}
	mode := "quotas"
	if !e.Config.Storage.Quotas {
		mode = "soft limits (no quotas)"
	}
	total, free, err := e.System.Space(path)
	if err != nil {
		return pass(fmt.Sprintf("%s, %s", path, mode))
	}
	detail := fmt.Sprintf("%s, %s, %s free of %s", path, mode, host.Bytes(free), host.Bytes(total))
	if total > 0 && free*10 < total {
		return warn(detail,
			"Servers that need more space will fail to save once the volume is full, whatever their own limits.",
			"raptor storage grow -size <new size>, or free space (old backups on the local destination, deleted servers' files).")
	}
	return pass(detail)
}

func checkHostDisk(ctx context.Context, e *Env) []Result {
	c := e.Config
	paths := []string{filepath.Dir(c.Paths.State), c.Paths.Logs, c.Paths.Tmp, c.Paths.Backups}
	if e.Docker != nil {
		if info, err := e.Docker.Info(ctx); err == nil && info.RootDir != "" {
			paths = append(paths, info.RootDir)
		}
	}
	minFree := int64(c.Limits.HostDiskMinFree)
	var lowest *host.DiskSpace
	for _, p := range paths {
		total, free, err := e.System.Space(p)
		if err != nil {
			continue
		}
		if lowest == nil || free < lowest.Free {
			lowest = &host.DiskSpace{Path: p, Total: total, Free: free}
		}
	}
	if lowest == nil {
		return skip("couldn't measure")
	}
	detail := fmt.Sprintf("%s free (least on %s)", host.Bytes(lowest.Free), lowest.Path)
	fix := "Free space: unused Docker images (docker image prune -a), old logs (journalctl --vacuum-size=500M), or local backups."
	switch {
	case minFree > 0 && lowest.Free < minFree:
		return fail(detail,
			fmt.Sprintf("Below limits.host_disk_min_free (%s), Wings refuses installs and image pulls so its own state never ends up on a full disk.", host.Bytes(minFree)),
			fix)
	case minFree > 0 && lowest.Free < 2*minFree:
		return warn(detail,
			fmt.Sprintf("Close to limits.host_disk_min_free (%s), below which installs and image pulls are refused.", host.Bytes(minFree)),
			fix)
	}
	return pass(detail)
}

func checkClock(ctx context.Context, e *Env) []Result {
	out, err := e.System.Run(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value")
	if err != nil {
		return skip("timedatectl isn't available")
	}
	if strings.TrimSpace(string(out)) == "yes" {
		return pass("synchronized (NTP)")
	}
	return warn("not synchronized",
		"A drifting clock breaks the Panel's signed grants and passkey signatures (they expire within minutes), and runs schedules at the wrong time.",
		"timedatectl set-ntp true (systemd-timesyncd), or install chrony.")
}

func checkFirewall(ctx context.Context, e *Env) []Result {
	if e.Status == nil || e.Status.GetServers() == nil {
		return skip("checked once Wings' runtime is ready")
	}
	if e.Firewall == nil {
		return skip("can't check")
	}
	if e.Firewall(ctx) {
		return pass("inet raptor table in place")
	}
	return fail("the inet raptor nftables table is missing",
		"Without it, install scripts can reach the host and private networks, and game servers can reach each other.",
		"Wings puts it back within a minute. If it keeps disappearing, another firewall tool is flushing nftables; configure it to leave the raptor table alone.")
}

func checkPanel(ctx context.Context, e *Env) []Result {
	c := e.Config
	if c.NodeID == "" {
		return skip("not linked to a Panel yet")
	}
	if _, err := e.System.Stat(c.Identity.Key); err != nil {
		return fail("node key missing: "+c.Identity.Key,
			"The node proves who it is to the Panel with this key; without it, it can't connect.",
			"Relink the node: raptor relink (the key can't be recovered; it never leaves the node).")
	}
	if e.HTTPGet == nil {
		return skip("can't check")
	}
	if err := e.HTTPGet(ctx, c.Panel.URL); err != nil {
		return fail(fmt.Sprintf("%s unreachable: %v", c.Panel.URL, err),
			"Servers keep running, but the node can't be managed from the Panel.",
			"Check DNS and outbound HTTPS (port 443) from this box: curl -I "+c.Panel.URL)
	}
	return pass(c.Panel.URL + " reachable")
}

func checkSFTP(ctx context.Context, e *Env) []Result {
	st := e.Status.GetSftp()
	if st == nil {
		return skip("checked once Wings' runtime is ready")
	}
	if !st.GetEnabled() {
		return skip("off (turned on per node in the Panel)")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", st.GetPort())
	banner, err := e.System.Dial(ctx, addr)
	if err != nil || !strings.HasPrefix(banner, "SSH-2.0-Raptor") {
		detail := fmt.Sprintf("port %d doesn't answer as Raptor's SFTP", st.GetPort())
		if err != nil {
			detail += ": " + err.Error()
		} else if banner != "" {
			detail += fmt.Sprintf(" (it says %q)", banner)
		}
		return fail(detail,
			"SFTP is on in the Panel, but users can't connect.",
			"journalctl -u raptor-wings | grep -i sftp shows why; another program may have the port.")
	}
	return pass(fmt.Sprintf("port %d, host key %s (make sure your provider's firewall allows the port)", st.GetPort(), st.GetHostKeyFingerprint()))
}

// pteroConfig is the part of Pterodactyl Wings' config.yml that matters.
type pteroConfig struct {
	System struct {
		SFTP struct {
			Port int `yaml:"bind_port"`
		} `yaml:"sftp"`
	} `yaml:"system"`
	Docker struct {
		Network struct {
			Name       string `yaml:"name"`
			Interfaces struct {
				V4 struct {
					Subnet string `yaml:"subnet"`
				} `yaml:"v4"`
			} `yaml:"interfaces"`
		} `yaml:"network"`
	} `yaml:"docker"`
}

func checkPterodactyl(ctx context.Context, e *Env) []Result {
	b, err := e.System.ReadFile("/etc/pterodactyl/config.yml")
	if err != nil {
		return skip("not installed")
	}
	var pc pteroConfig
	if err := yaml.Unmarshal(b, &pc); err != nil {
		return warn("/etc/pterodactyl/config.yml can't be parsed: "+err.Error(),
			"Pterodactyl's network and SFTP port can't be compared with Raptor's.", "Check the file with Pterodactyl's own tools.")
	}
	name := pc.Docker.Network.Name
	if name == "" {
		name = "pterodactyl_nw"
	}
	var ptero []netip.Prefix
	if p, err := netip.ParsePrefix(pc.Docker.Network.Interfaces.V4.Subnet); err == nil {
		ptero = append(ptero, p)
	}
	var mine []netip.Prefix
	if e.Docker != nil {
		if s, err := e.Docker.NetworkSubnets(ctx, name); err == nil {
			ptero = append(ptero, s...)
		}
		for _, n := range []string{e.Config.Docker.Network, e.Config.Docker.InstallNetwork} {
			if s, err := e.Docker.NetworkSubnets(ctx, n); err == nil {
				mine = append(mine, s...)
			}
		}
	}
	for _, a := range mine {
		for _, b := range ptero {
			if a.Overlaps(b) {
				return fail(fmt.Sprintf("Raptor's network %s overlaps Pterodactyl's %s", a, b),
					"Containers on both can't reach the right gateway, and Raptor's firewall rules could apply to Pterodactyl's servers.",
					"Set docker.subnet and docker.install_subnet in /etc/raptor/config.yml to free ranges, then remove Raptor's networks while its servers are stopped (Wings recreates them).")
			}
		}
	}
	port := pc.System.SFTP.Port
	if port == 0 {
		port = 2022
	}
	detail := fmt.Sprintf("Pterodactyl Wings is installed too: its SFTP is on port %d, its network %s; they don't conflict", port, name)
	if st := e.Status.GetSftp(); st.GetEnabled() && int(st.GetPort()) == port {
		return fail(fmt.Sprintf("Raptor's and Pterodactyl's SFTP both use port %d", port),
			"Only one can listen; users of the other get connection errors.",
			"Turn Raptor's SFTP off and on again in the Panel: it picks the first free port from 2022 to 2099.")
	}
	return pass(detail)
}

func checkSSH(ctx context.Context, e *Env) []Result {
	if e.System.Geteuid() != 0 {
		return skip("run raptor doctor as root to check")
	}
	out, err := e.System.Run(ctx, "sshd", "-T")
	if err != nil {
		return skip("sshd isn't installed")
	}
	opts := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), " ")
		opts[strings.ToLower(k)] = strings.ToLower(v)
	}
	if opts["permitrootlogin"] == "yes" && opts["passwordauthentication"] == "yes" {
		return warn("root can log in with a password",
			"Bots try passwords on every public SSH server; a guessed root password owns the node and every server on it.",
			"Log in with an SSH key, then set PermitRootLogin prohibit-password (or PasswordAuthentication no) in /etc/ssh/sshd_config and systemctl reload ssh.")
	}
	return pass("no password logins for root")
}

func checkOSUpdates(ctx context.Context, e *Env) []Result {
	out, err := e.System.Run(ctx, "apt-config", "dump", "APT::Periodic::Unattended-Upgrade")
	if err != nil {
		return skip("not an apt-based system")
	}
	if strings.Contains(string(out), `"1"`) {
		return pass("unattended-upgrades on")
	}
	return warn("off",
		"Security fixes for the kernel, OpenSSH, and Docker aren't installed until someone does it by hand.",
		"apt install unattended-upgrades && dpkg-reconfigure -plow unattended-upgrades")
}

func checkSelfUpdate(_ context.Context, e *Env) []Result {
	target, err := e.System.Readlink("/usr/local/bin/raptor")
	if err == nil && target == "/usr/local/lib/raptor/current" {
		return pass("installed for raptor update")
	}
	return warn("/usr/local/bin/raptor isn't the installer's link to /usr/local/lib/raptor/current",
		"raptor update only updates Wings installed by the installer, so it can switch versions and roll back.",
		"Reinstall Wings with the installer (servers keep running).")
}
