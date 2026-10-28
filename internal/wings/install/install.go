// Package install runs an egg's installation for a server (docs/EGGS.md#install):
// variable validation, architecture checks, the isolated install container,
// a capped log, and the ownership fix afterwards.
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
)

// LogLimit is how much install output is kept (the end of it).
const LogLimit = 10 << 20

// Params describe one install or reinstall.
type Params struct {
	Egg      *eggs.Egg
	Image    string // the runtime image the server will use (checked for arch)
	ServerID string
	Dir      string // the server's directory
	TmpDir   string // parent for the per-install script directory
	// Variables are the owner's values; missing ones take the egg's defaults.
	Variables map[string]string
	// Env is everything else the environment needs (memory, allocation, …).
	// Its Variables field is replaced by the validated values.
	Env      eggs.Runtime
	UID, GID int
	// Skip runs no install script (Pterodactyl's "skip egg scripts"); the
	// directory is still created and its ownership fixed.
	Skip bool
	// Output, if set, also receives the script's output as it runs (the
	// job log).
	Output io.Writer
}

// Result describes a finished install.
type Result struct {
	ExitCode  int64 // non-zero is a warning, not a failure (docs/EGGS.md)
	Skipped   bool
	Duration  time.Duration
	Log       []byte // the last LogLimit bytes of output
	Truncated bool   // earlier output was dropped
	Variables map[string]string
}

// Run installs the server. It fails before anything is downloaded if the
// variables are invalid (a eggs.VariableErrors) or the egg or its images
// don't support this CPU (containers.ErrUnsupportedArch). A timeout or
// Docker error fails the install; the files are kept either way.
func Run(ctx context.Context, rt containers.Runtime, p Params) (Result, error) {
	vars, err := p.Egg.Validate(p.Variables)
	if err != nil {
		return Result{}, err
	}
	res := Result{Variables: vars}

	if !p.Egg.SupportsArch(goruntime.GOARCH) {
		return res, fmt.Errorf("%w: the egg supports %v, this machine is %s", containers.ErrUnsupportedArch, p.Egg.Raptor.Arch, goruntime.GOARCH)
	}
	skip := p.Skip || p.Egg.Install.Script == ""
	images := []string{p.Image}
	if !skip {
		images = append(images, p.Egg.Install.Container)
	}
	for _, img := range images {
		if img == "" {
			continue
		}
		if err := rt.CheckArch(ctx, img); err != nil {
			return res, err
		}
	}

	if err := os.MkdirAll(p.Dir, 0o755); err != nil { //nolint:gosec // the server's own directory, handed to its user below
		return res, err
	}
	if skip {
		res.Skipped = true
		return res, FixOwnership(p.Dir, p.UID, p.GID)
	}

	env := p.Env
	env.Variables = vars
	log := &tail{limit: LogLimit}
	var output io.Writer = log
	if p.Output != nil {
		output = io.MultiWriter(log, p.Output)
	}
	start := time.Now()
	out, runErr := rt.Install(ctx, containers.InstallSpec{
		ServerID:  p.ServerID,
		Dir:       p.Dir,
		TmpDir:    p.TmpDir,
		Install:   p.Egg.Install,
		Env:       env.Environment(),
		MemoryMiB: env.MemoryMiB,
		Timeout:   p.Egg.InstallTimeout(),
		Output:    output,
	})
	res.Duration = time.Since(start)
	res.ExitCode = out.ExitCode

	// The script ran, but a download it needed failed, or the server's jar
	// is broken: the server couldn't start, so the install failed.
	if runErr == nil {
		if f := check(out, p, vars); f != nil {
			_, _ = fmt.Fprintf(output, "\n%s\n", f.Reason)
			runErr = f
		}
	}
	res.Log, res.Truncated = log.bytes()

	// Hand the files to the server's user even after a failure, so the owner
	// can inspect or fix them over SFTP.
	if err := FixOwnership(p.Dir, p.UID, p.GID); err != nil {
		return res, errors.Join(runErr, fmt.Errorf("fix ownership: %w", err))
	}
	return res, runErr
}

// Failure is an install whose script ran but didn't leave a server that can
// start. Reason is written for the owner (it's the server's install error).
type Failure struct{ Reason string }

func (f *Failure) Error() string { return f.Reason }

// check judges a finished script (docs/EGGS.md#install): a download that
// failed on the network even after retries fails the install, and so does
// a startup jar that's there but isn't a valid jar.
func check(out containers.InstallResult, p Params, vars map[string]string) *Failure {
	if n := len(out.FailedDownloads); n > 0 {
		d := out.FailedDownloads[0]
		what := "A download failed"
		if d.URL != "" {
			what += ": " + d.URL
		}
		more := ""
		if n > 1 {
			more = fmt.Sprintf("; %d more failed too", n-1)
		}
		return &Failure{Reason: fmt.Sprintf("%s (curl exit %d%s). Reinstall to try again.", what, d.Code, more)}
	}
	startup := p.Env.Startup
	if startup == "" {
		startup = p.Egg.DefaultStartup()
	}
	if jar := startupJar(startup, vars); jar != "" {
		if err := checkJar(p.Dir, jar); err != nil {
			return &Failure{Reason: fmt.Sprintf("%s %v, so the server can't start. Reinstall to try again.", jar, err)}
		}
	}
	return nil
}

// FixOwnership hands every file in dir to uid:gid after an install. It uses
// lchown through os.Root, so it never follows symlinks and never leaves dir.
func FixOwnership(dir string, uid, gid int) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return fs.WalkDir(root.FS(), ".", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return root.Lchown(path, uid, gid)
	})
}

// tail keeps the last limit bytes written to it. It's safe for concurrent
// use: the runtime writes from its own goroutine.
type tail struct {
	mu        sync.Mutex
	limit     int
	buf       []byte
	truncated bool
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.limit {
		t.buf = append(t.buf[:0:0], t.buf[len(t.buf)-t.limit:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tail) bytes() ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) > t.limit {
		return append([]byte(nil), t.buf[len(t.buf)-t.limit:]...), true
	}
	return append([]byte(nil), t.buf...), t.truncated
}
