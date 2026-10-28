package docker

import (
	"bufio"
	_ "embed"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// curlWrapper is the curl install scripts find first on their PATH: the
// image's own curl with retries, recording downloads that still failed
// (docs/EGGS.md#downloads).
//
//go:embed installbin/curl
var curlWrapper []byte

const (
	// installBin is where the wrapper is in the install container, inside
	// the script's read-only mount.
	installBin = "/mnt/install/bin"
	// installReport is the writable directory the wrapper records failed
	// downloads in (installReportFile).
	installReport     = "/mnt/install-report"
	installReportFile = "downloads"
	// dockerPath is Docker's PATH for an image that doesn't set one.
	dockerPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	// reportLimit caps how much of the report is read: the script can write
	// to it too.
	reportLimit = 64 << 10
)

// writeInstallBin puts the wrapper in the script directory's bin/.
func writeInstallBin(scriptDir string) error {
	bin := filepath.Join(scriptDir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil { //nolint:gosec // mounted read-only into the install container
		return err
	}
	name := filepath.Join(bin, "curl")
	if err := os.WriteFile(name, curlWrapper, 0o755); err != nil { //nolint:gosec // an executable for the install container
		return err
	}
	return os.Chmod(name, 0o755) //nolint:gosec // the umask may have dropped the execute bits
}

// installEnv returns env with the wrapper's directory first on PATH. The
// rest of PATH is the egg's own PATH variable if it has one, else the
// image's, else Docker's default, so every other command resolves as it
// would without Raptor.
func installEnv(env, imageEnv []string) []string {
	path := ""
	for _, kv := range imageEnv {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
			continue
		}
		out = append(out, kv)
	}
	if path == "" {
		path = dockerPath
	}
	return append(out, "PATH="+installBin+":"+path)
}

// readFailedDownloads reads what the wrapper recorded: a line per failed
// curl, its exit code and then its URLs. Credentials in a URL aren't kept.
func readFailedDownloads(reportDir string) []containers.FailedDownload {
	f, err := os.Open(filepath.Join(reportDir, installReportFile)) //nolint:gosec // our own temporary directory
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []containers.FailedDownload
	sc := bufio.NewScanner(io.LimitReader(f, reportLimit))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		code, err := strconv.Atoi(fields[0])
		if err != nil || code <= 0 {
			continue
		}
		d := containers.FailedDownload{Code: code}
		if len(fields) > 1 {
			d.URL = cleanURL(fields[1])
		}
		out = append(out, d)
	}
	return out
}

// cleanURL drops a URL's user and password and anything unprintable, and
// shortens it for a status line.
func cleanURL(s string) string {
	if u, err := url.Parse(s); err == nil && u.User != nil {
		u.User = nil
		s = u.String()
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > 300 {
		s = strings.ToValidUTF8(s[:300], "") + "…"
	}
	return s
}
