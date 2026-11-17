package backdoor

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

// Limits bound a scan, so a server with a huge resources folder can't keep
// the node busy: it stops at the first one reached and says which.
type Limits struct {
	Files    int           // files read
	FileSize int64         // a file larger is skipped
	Bytes    int64         // bytes read in all
	Time     time.Duration // how long it may take
	Findings int           // findings kept
}

// DefaultLimits are a scan's limits: far more than a big FiveM server has.
var DefaultLimits = Limits{Files: 20000, FileSize: 4 << 20, Bytes: 512 << 20, Time: 2 * time.Minute, Findings: 1000}

// Report is a scan's result.
type Report struct {
	// Dirs are the folders that were there to scan.
	Dirs  []string `json:"dirs"`
	Files int      `json:"files"`
	Bytes int64    `json:"bytes"`
	// Skipped is how many files were over Limits.FileSize.
	Skipped int `json:"skipped"`
	// Stopped says why the scan ended early ("files", "bytes", "time",
	// "findings"); empty if it read everything.
	Stopped  string    `json:"stopped,omitempty"`
	Findings []Finding `json:"findings"`
}

// FS is a server's directory: files.FS, which keeps the walk inside it and
// leaves out what the egg denies.
type FS interface {
	WalkDir(dir string, fn fs.WalkDirFunc) error
	ReadFile(name string, limit int64) ([]byte, fs.FileInfo, error)
}

// skipDirs are folders not scanned: packages a build tool downloads (they
// run child_process and eval honestly, and are huge) and version control.
var skipDirs = []string{"node_modules", ".git"}

var errStop = errors.New("stop")

// ScanDirs scans the files a rule set reads in dirs (from the server's
// directory). Folders that don't exist are left out of Report.Dirs.
func ScanDirs(ctx context.Context, f FS, set string, dirs []string, lim Limits) (Report, error) {
	exts := Extensions(set)
	if exts == nil {
		return Report{}, errors.New("unknown scan rules " + set)
	}
	ctx, cancel := context.WithTimeout(ctx, lim.Time)
	defer cancel()
	r := Report{Dirs: []string{}, Findings: []Finding{}}
	for _, dir := range dirs {
		started := false
		err := f.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if !started && errors.Is(err, fs.ErrNotExist) {
					return fs.SkipAll
				}
				if d != nil && d.IsDir() {
					return fs.SkipDir // unreadable: leave it out
				}
				return nil
			}
			if !started {
				started = true
				if !d.IsDir() {
					return fs.SkipAll
				}
				r.Dirs = append(r.Dirs, dir)
			}
			if ctx.Err() != nil {
				r.Stopped = "time"
				return errStop
			}
			if d.IsDir() {
				if slices.Contains(skipDirs, d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || !slices.Contains(exts, strings.ToLower(path.Ext(p))) {
				return nil
			}
			if info, err := d.Info(); err != nil || info.Size() > lim.FileSize {
				if err == nil {
					r.Skipped++
				}
				return nil
			}
			if r.Files >= lim.Files {
				r.Stopped = "files"
				return errStop
			}
			data, _, err := f.ReadFile(p, lim.FileSize)
			if err != nil {
				return nil //nolint:nilerr // changed or removed since listed: leave it out
			}
			r.Files++
			r.Bytes += int64(len(data))
			for _, fd := range Scan(p, data) {
				if len(r.Findings) >= lim.Findings {
					r.Stopped = "findings"
					return errStop
				}
				r.Findings = append(r.Findings, fd)
			}
			if r.Bytes >= lim.Bytes {
				r.Stopped = "bytes"
				return errStop
			}
			return nil
		})
		if errors.Is(err, errStop) {
			break
		}
		if err != nil {
			return r, err
		}
	}
	// A cancelled request isn't a timed-out scan.
	if r.Stopped == "time" && !errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
		return r, ctx.Err()
	}
	return r, nil
}
