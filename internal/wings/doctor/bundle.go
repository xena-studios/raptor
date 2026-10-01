package doctor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// What goes in a bundle, besides the doctor's own results: logs and the
// state of the system, never server files or secrets. Every file is
// redacted.
var bundleCommands = []struct {
	file string
	cmd  []string
}{
	{"wings.log", []string{"journalctl", "-u", "raptor-wings", "-n", "5000", "--no-pager", "-o", "short-iso"}},
	{"shutdown.log", []string{"journalctl", "-u", "raptor-shutdown", "-n", "500", "--no-pager", "-o", "short-iso"}},
	{"docker.log", []string{"journalctl", "-u", "docker", "-n", "1000", "--no-pager", "-o", "short-iso"}},
	{"system/uname.txt", []string{"uname", "-a"}},
	{"system/uptime.txt", []string{"uptime"}},
	{"system/memory.txt", []string{"free", "-b"}},
	{"system/disks.txt", []string{"df", "-h"}},
	{"system/block-devices.txt", []string{"lsblk"}},
	{"system/mounts.txt", []string{"findmnt", "--list"}},
	{"system/units.txt", []string{"systemctl", "status", "raptor-wings", "raptor-shutdown", "docker", "--no-pager"}},
	{"system/failed-units.txt", []string{"systemctl", "--failed", "--no-pager"}},
	{"system/nftables.txt", []string{"nft", "list", "table", "inet", "raptor"}},
	{"system/addresses.txt", []string{"ip", "-brief", "address"}},
	{"docker/version.txt", []string{"docker", "version"}},
	{"docker/info.txt", []string{"docker", "info"}},
	{"docker/containers.txt", []string{"docker", "ps", "-a", "--filter", "label=raptor.wings.managed=true", "--format", "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}"}},
	{"docker/networks.txt", []string{"docker", "network", "ls"}},
}

// Files copied into the bundle when they exist (the config file's path is
// added from the environment).
var bundleFiles = map[string]string{
	"/etc/os-release":             "system/os-release",
	"/etc/docker/daemon.json":     "docker/daemon.json",
	"/var/lib/raptor/update.json": "update.json",
	"/var/log/raptor/install.log": "install.log",
}

// Bundle writes a redacted .tar.gz of diagnostics into dir (readable by root
// only) and returns its path.
func Bundle(ctx context.Context, e *Env, results []Result, configPath, dir string, now time.Time) (string, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, data []byte) error {
		data = Redact(data)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: now}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	var text bytes.Buffer
	Print(&text, results)
	if err := add("doctor.txt", text.Bytes()); err != nil {
		return "", err
	}
	var js bytes.Buffer
	if err := PrintJSON(&js, results); err != nil {
		return "", err
	}
	if err := add("doctor.json", js.Bytes()); err != nil {
		return "", err
	}
	for _, c := range bundleCommands {
		out, err := e.System.Run(ctx, c.cmd[0], c.cmd[1:]...)
		if err != nil {
			out = append(out, fmt.Sprintf("\n(%v)\n", err)...)
		}
		if err := add(c.file, out); err != nil {
			return "", err
		}
	}
	files := map[string]string{configPath: "config.yml"}
	for k, v := range bundleFiles {
		files[k] = v
	}
	for src, name := range files {
		if b, err := e.System.ReadFile(src); err == nil {
			if err := add(name, b); err != nil {
				return "", err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "raptor-doctor-"+now.UTC().Format("20060102-150405")+".tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

var (
	pemBlock   = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]+-----.*?-----END [A-Z0-9 ]+-----`)
	secretKV   = regexp.MustCompile(`(?i)((?:password|passwd|secret|token|api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|authorization)["']?\s*[:=]\s*)("[^"]*"|'[^']*'|[^\s,}]+)`)
	bearer     = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	urlUserPwd = regexp.MustCompile(`(://)[^/@\s:]+:[^/@\s]+@`)
)

// Redact removes secrets from text: keys (PEM blocks), values of fields
// named like secrets, bearer tokens, and passwords in URLs.
func Redact(b []byte) []byte {
	b = pemBlock.ReplaceAll(b, []byte("[redacted key]"))
	b = bearer.ReplaceAll(b, []byte("${1}[redacted]")) // before secretKV, which would take "Bearer" as the value
	b = secretKV.ReplaceAll(b, []byte("${1}[redacted]"))
	b = urlUserPwd.ReplaceAll(b, []byte("${1}[redacted]@"))
	return b
}
