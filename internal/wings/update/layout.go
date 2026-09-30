package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultDir holds the installed versions. /usr/local/bin/raptor is a symlink
// to its "current" link, which points at the active version's binary:
//
//	/usr/local/lib/raptor/raptor-1.4.2
//	/usr/local/lib/raptor/raptor-1.5.0
//	/usr/local/lib/raptor/current -> raptor-1.5.0
//
// Switching versions is one atomic rename of "current", and the previous
// version stays next to it for a rollback.
const DefaultDir = "/usr/local/lib/raptor"

const (
	binPrefix  = "raptor-"
	currentLnk = "current"
	tmpPrefix  = ".tmp-"
)

// Layout is the directory of installed versions.
type Layout struct {
	Dir string
}

// Binary is where a version's binary is installed.
func (l Layout) Binary(version string) string {
	return filepath.Join(l.Dir, binPrefix+version)
}

// Holds reports whether exe (a resolved path, like os.Executable returns) is
// one of the installed versions.
func (l Layout) Holds(exe string) bool {
	dir, err := filepath.EvalSymlinks(l.Dir)
	return err == nil && filepath.Dir(exe) == dir && strings.HasPrefix(filepath.Base(exe), binPrefix)
}

// Current returns the version "current" points at.
func (l Layout) Current() (string, error) {
	target, err := os.Readlink(filepath.Join(l.Dir, currentLnk))
	if err != nil {
		return "", err
	}
	v, ok := strings.CutPrefix(filepath.Base(target), binPrefix)
	if !ok {
		return "", fmt.Errorf("%s points at %s, not a raptor version", filepath.Join(l.Dir, currentLnk), target)
	}
	return v, nil
}

// Promote points "current" at a version. It replaces the link with a rename,
// so it's always either the old version or the new one, even after a crash.
func (l Layout) Promote(version string) error {
	if _, err := os.Stat(l.Binary(version)); err != nil {
		return err
	}
	tmp := filepath.Join(l.Dir, tmpPrefix+currentLnk)
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(binPrefix+version, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(l.Dir, currentLnk)); err != nil {
		return err
	}
	return syncDir(l.Dir)
}

// Prune removes every installed version except keep (and whatever "current"
// points at), and leftovers of interrupted downloads.
func (l Layout) Prune(keep ...string) error {
	if cur, err := l.Current(); err == nil {
		keep = append(keep, cur)
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		v, isBin := strings.CutPrefix(name, binPrefix)
		switch {
		case strings.HasPrefix(name, tmpPrefix):
		case isBin && !slices.Contains(keep, v):
		default:
			continue
		}
		if err := os.Remove(filepath.Join(l.Dir, name)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // an installation directory
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
