// Package install runs an egg's installation for a server (docs/EGGS.md#install):
// variable validation, architecture checks, the isolated install container,
// a capped log, and the ownership fix afterwards.
package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	goruntime "runtime"
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
	start := time.Now()
	out, runErr := rt.Install(ctx, containers.InstallSpec{
		ServerID:  p.ServerID,
		Dir:       p.Dir,
		TmpDir:    p.TmpDir,
		Install:   p.Egg.Install,
		Env:       env.Environment(),
		MemoryMiB: env.MemoryMiB,
		Timeout:   p.Egg.InstallTimeout(),
		Output:    log,
	})
	res.Duration = time.Since(start)
	res.ExitCode = out.ExitCode
	res.Log, res.Truncated = log.bytes()

	// Hand the files to the server's user even after a failure, so the owner
	// can inspect or fix them over SFTP.
	if err := FixOwnership(p.Dir, p.UID, p.GID); err != nil {
		return res, errors.Join(runErr, fmt.Errorf("fix ownership: %w", err))
	}
	return res, runErr
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

// tail keeps the last limit bytes written to it.
type tail struct {
	limit     int
	buf       []byte
	truncated bool
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.limit {
		t.buf = append(t.buf[:0:0], t.buf[len(t.buf)-t.limit:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tail) bytes() ([]byte, bool) {
	if len(t.buf) > t.limit {
		return t.buf[len(t.buf)-t.limit:], true
	}
	return t.buf, t.truncated
}
