package host

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ErrLowDisk is returned for work refused because the host disk is low.
var ErrLowDisk = errors.New("the host disk is low on free space")

// DiskSpace is one watched path's filesystem.
type DiskSpace struct {
	Path  string `json:"path"`
	Total int64  `json:"total"`
	Free  int64  `json:"free"`
}

// DiskGuard watches free space where Wings and Docker keep their own state
// (SQLite, logs, Docker images, local backups): below MinFree, installs and
// image pulls are refused, so the node's state never ends up on a full disk
// (docs/RELIABILITY.md). Paths that don't exist (yet) are skipped.
type DiskGuard struct {
	MinFree int64
	// Space returns a path's filesystem size and free bytes (storage.Space).
	Space func(path string) (total, free int64, err error)
	// OnChange is called when the disk goes low or recovers, with every
	// watched path's space.
	OnChange func(low bool, spaces []DiskSpace)

	mu    sync.Mutex
	paths []string
	low   bool
}

// Watch adds paths to watch.
func (g *DiskGuard) Watch(paths ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, p := range paths {
		if p != "" && !slices.Contains(g.paths, p) {
			g.paths = append(g.paths, p)
		}
	}
}

// Check measures the watched paths and reports whether any is low. It calls
// OnChange if that changed since the last check.
func (g *DiskGuard) Check() (low bool, spaces []DiskSpace) {
	if g == nil || g.MinFree <= 0 {
		return false, nil
	}
	g.mu.Lock()
	paths := slices.Clone(g.paths)
	g.mu.Unlock()
	for _, p := range paths {
		total, free, err := g.Space(p)
		if err != nil {
			continue
		}
		spaces = append(spaces, DiskSpace{Path: p, Total: total, Free: free})
		if free < g.MinFree {
			low = true
		}
	}
	g.mu.Lock()
	changed := low != g.low
	g.low = low
	g.mu.Unlock()
	if changed && g.OnChange != nil {
		g.OnChange(low, spaces)
	}
	return low, spaces
}

// Err returns ErrLowDisk, with the lowest path, if any watched path is
// below MinFree now.
func (g *DiskGuard) Err() error {
	low, spaces := g.Check()
	if !low {
		return nil
	}
	lowest := slices.MinFunc(spaces, func(a, b DiskSpace) int { return cmp.Compare(a.Free, b.Free) })
	return fmt.Errorf("%w: %s has %s free, below limits.host_disk_min_free (%s); free up space (old Docker images: docker image prune)",
		ErrLowDisk, lowest.Path, Bytes(lowest.Free), Bytes(g.MinFree))
}

// Bytes formats a byte count for people, in binary units.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
