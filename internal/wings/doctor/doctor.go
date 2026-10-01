// Package doctor diagnoses a node: every check says what's wrong, why it
// matters, and how to fix it (docs/WINGS.md#doctor). It runs on the box
// without Wings, since a node that needs a doctor may not be running it, and
// asks Wings for its state when it is. It never changes anything.
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"time"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/docker"
)

// Result statuses.
const (
	Pass = "pass"
	Warn = "warn" // works, but something should change
	Fail = "fail" // broken, or will break servers
	Skip = "skip" // doesn't apply, or couldn't be checked
)

// Result is one check's outcome.
type Result struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Detail is what was found; Why and Fix are set for warnings and
	// failures.
	Detail string `json:"detail,omitempty"`
	Why    string `json:"why,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// System is the box, as the checks see it, so they can be tested.
type System interface {
	ReadFile(path string) ([]byte, error)
	Stat(path string) (fs.FileInfo, error)
	Readlink(path string) (string, error)
	// Run runs a command and returns its combined output.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// Dial connects to addr and returns what the server sends first (up to
	// a line), for banner checks.
	Dial(ctx context.Context, addr string) (string, error)
	// Space returns a path's filesystem size and free bytes.
	Space(path string) (total, free int64, err error)
	Geteuid() int
}

// Docker is what the checks ask Docker.
type Docker interface {
	Info(ctx context.Context) (docker.Info, error)
	NetworkSubnets(ctx context.Context, name string) ([]netip.Prefix, error)
}

// Env is what the checks run against.
type Env struct {
	Config     config.Config
	ConfigPath string
	ConfigErr  error // loading config.yml failed (Config holds the defaults)
	System     System
	Docker     Docker // nil if Docker isn't reachable
	DockerErr  error
	// Status is Wings' own report (nil if Wings isn't answering, with
	// StatusErr).
	Status    *localv1.GetStatusResponse
	StatusErr error
	// VolumeCheck checks the server data volume (storage.Volume.Check).
	VolumeCheck func() error
	// Firewall reports whether Wings' nftables table is in place.
	Firewall func(ctx context.Context) bool
	// HTTPGet fetches a URL (for the Panel check).
	HTTPGet func(ctx context.Context, url string) error
}

// Check is one diagnosis. Run returns one or more results.
type Check struct {
	ID    string
	Title string
	Run   func(ctx context.Context, e *Env) []Result
}

// Run runs every check, each with a timeout, and returns the results in
// order.
func Run(ctx context.Context, e *Env) []Result {
	var out []Result
	for _, c := range Checks() {
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		for _, r := range c.Run(cctx, e) {
			if r.ID == "" {
				r.ID = c.ID
			}
			if r.Title == "" {
				r.Title = c.Title
			}
			out = append(out, r)
		}
		cancel()
	}
	return out
}

// Failed reports whether any result failed.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Status == Fail {
			return true
		}
	}
	return false
}

// Print writes results for people.
func Print(w io.Writer, results []Result) {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Status]++
		mark := map[string]string{Pass: "✓", Warn: "!", Fail: "✗", Skip: "-"}[r.Status]
		line := fmt.Sprintf("%s %-26s", mark, r.Title)
		if r.Detail != "" {
			line += " " + r.Detail
		}
		_, _ = fmt.Fprintln(w, strings.TrimRight(line, " "))
		if r.Why != "" {
			_, _ = fmt.Fprintf(w, "    Why: %s\n", r.Why)
		}
		if r.Fix != "" {
			_, _ = fmt.Fprintf(w, "    Fix: %s\n", strings.ReplaceAll(r.Fix, "\n", "\n         "))
		}
	}
	_, _ = fmt.Fprintf(w, "\n%d passed, %d warnings, %d failed, %d skipped\n", counts[Pass], counts[Warn], counts[Fail], counts[Skip])
}

// PrintJSON writes results as JSON (for scripts, and the Panel later).
func PrintJSON(w io.Writer, results []Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"results": results, "failed": Failed(results)})
}

// OS is the real System.
type OS struct {
	// Space is storage.Space (Linux only).
	SpaceFunc func(path string) (int64, int64, error)
}

// ReadFile implements System.
func (OS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) } //nolint:gosec // fixed system paths

// Stat implements System.
func (OS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

// Readlink implements System.
func (OS) Readlink(path string) (string, error) { return os.Readlink(path) }

// Geteuid implements System.
func (OS) Geteuid() int { return os.Geteuid() }

// Run implements System.
func (OS) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // fixed commands
}

// Dial implements System.
func (OS) Dial(ctx context.Context, addr string) (string, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer func() { _ = c.Close() }()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 256)
	n, _ := c.Read(buf)
	line, _, _ := strings.Cut(string(buf[:n]), "\n")
	return strings.TrimSpace(line), nil
}

// Space implements System.
func (o OS) Space(path string) (int64, int64, error) { return o.SpaceFunc(path) }
