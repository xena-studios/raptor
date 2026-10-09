package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Servers is what file operations need from the server manager.
type Servers interface {
	// FilesDir returns the server's directory.
	FilesDir(id string) (string, error)
	// CheckFiles returns an error while the server's files can't be read,
	// or written if write is set (installing, restoring, deleted, over the
	// soft disk limit).
	CheckFiles(ctx context.Context, id string, write bool) error
	// Denylist returns the file_denylist of the server's egg.
	Denylist(id string) ([]string, error)
	// SpaceLeft returns how many bytes the server may still use (< 0: no
	// limit).
	SpaceLeft(ctx context.Context, id string) (int64, error)
}

// Limits of the web file manager (docs/WINGS.md#files-and-sftp).
const (
	// MaxEdit is the largest file read or written whole, for the editor.
	// Larger files are downloaded and uploaded.
	MaxEdit = 4 << 20
	// MaxList is the most entries a listing returns.
	MaxList = 10_000
)

// Job types.
const (
	JobCompress   = "files.compress"
	JobDecompress = "files.decompress"
)

// Events recorded when an archive job finishes.
const (
	EventCompressed   = "server.files.compressed"
	EventDecompressed = "server.files.decompressed"
)

// Options configure a Service.
type Options struct {
	Servers  Servers
	Store    *store.DB
	Jobs     *jobs.Engine
	Events   *events.Outbox
	Log      *slog.Logger
	UID, GID int // the servers' user
	Now      func() time.Time
}

// Service runs the web file manager's operations on servers' files.
type Service struct {
	o Options

	mu        sync.Mutex
	downloads map[string]*Download
}

// NewService returns a Service and registers its jobs.
func NewService(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Service{o: o, downloads: map[string]*Download{}}
	if o.Jobs != nil {
		o.Jobs.Register(JobCompress, jobs.Handler{Class: "files", ServerLock: true, Resumable: true, MaxAttempts: 2, Run: s.compressJob})
		o.Jobs.Register(JobDecompress, jobs.Handler{Class: "files", ServerLock: true, Resumable: true, MaxAttempts: 2, Run: s.decompressJob})
	}
	return s
}

// FS opens a server's directory with its egg's denylist. It doesn't check
// whether the files may be touched now; open does.
func (s *Service) FS(id string) (*FS, error) {
	return OpenServer(s.o.Servers, id, s.o.UID, s.o.GID)
}

// OpenServer opens a server's directory with its egg's denylist.
func OpenServer(servers interface {
	FilesDir(id string) (string, error)
	Denylist(id string) ([]string, error)
}, id string, uid, gid int,
) (*FS, error) {
	dir, err := servers.FilesDir(id)
	if err != nil {
		return nil, err
	}
	deny, err := servers.Denylist(id)
	if err != nil {
		return nil, err
	}
	return Open(dir, uid, gid, Compile(deny))
}

// open checks that the server's files may be touched now (written, and
// grow, if write is set) and opens them.
func (s *Service) open(ctx context.Context, id string, write bool) (*FS, error) {
	if err := s.o.Servers.CheckFiles(ctx, id, write); err != nil {
		return nil, err
	}
	return s.FS(id)
}

// Join resolves name against dir, both from the server's directory; the
// result can't climb above it.
func Join(dir, name string) string { return Rel(dir + "/" + name) }

// Entry is a file in a listing.
type Entry struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "file", "dir", "symlink", "other"
	Size     int64  `json:"size"`
	Mode     uint32 `json:"mode"`        // permission bits
	Modified int64  `json:"modified_at"` // unix ms
	// Target is a symlink's target as stored; TargetType is what it
	// reaches inside the server's directory ("file", "dir", "other"), or ""
	// if nothing (missing, or outside).
	Target     string `json:"target,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	// Denied: on the egg's denylist. Shown, but can't be opened or changed.
	Denied bool `json:"denied,omitempty"`
}

// Listing is a directory's entries, in name order.
type Listing struct {
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated,omitempty"` // more than MaxList entries
}

func typeOf(m fs.FileMode) string {
	switch {
	case m.IsRegular():
		return "file"
	case m.IsDir():
		return "dir"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	}
	return "other"
}

func (f *FS) entry(p string, fi fs.FileInfo) Entry {
	e := Entry{
		Name: fi.Name(), Type: typeOf(fi.Mode()), Size: fi.Size(), Mode: uint32(fi.Mode().Perm()),
		Modified: fi.ModTime().UnixMilli(), Denied: f.Denied(p, fi.IsDir()),
	}
	if e.Type == "symlink" {
		e.Target, _ = f.root.Readlink(p)
		if t, err := f.root.Stat(p); err == nil {
			e.TargetType = typeOf(t.Mode())
		}
	}
	return e
}

// List lists a directory.
func (s *Service) List(ctx context.Context, id, dir string) (Listing, error) {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return Listing{}, err
	}
	defer func() { _ = f.Close() }()
	dir = Rel(dir)
	infos, err := f.List(dir)
	if err != nil {
		return Listing{}, err
	}
	l := Listing{Path: dir, Entries: make([]Entry, 0, min(len(infos), MaxList))}
	for _, fi := range infos {
		if dir == "." && IsStaging(fi.Name()) {
			continue // uploads in progress
		}
		if len(l.Entries) == MaxList {
			l.Truncated = true
			break
		}
		l.Entries = append(l.Entries, f.entry(path.Join(dir, fi.Name()), fi))
	}
	return l, nil
}

// Stat describes one file without following a symlink.
func (s *Service) Stat(ctx context.Context, id, name string) (Entry, error) {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return Entry{}, err
	}
	defer func() { _ = f.Close() }()
	name = Rel(name)
	fi, err := f.Lstat(name)
	if err != nil {
		return Entry{}, err
	}
	return f.entry(name, fi), nil
}

// Content is a file read whole.
type Content struct {
	Entry
	Data []byte `json:"data"`
}

// Read reads a regular file of at most MaxEdit bytes.
func (s *Service) Read(ctx context.Context, id, name string) (Content, error) {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return Content{}, err
	}
	defer func() { _ = f.Close() }()
	name = Rel(name)
	data, fi, err := f.ReadFile(name, MaxEdit)
	if errors.Is(err, ErrTooLarge) {
		return Content{}, fmt.Errorf("%w for the editor (over %d MiB); download it instead", err, MaxEdit>>20)
	}
	if err != nil {
		return Content{}, err
	}
	return Content{Entry: f.entry(name, fi), Data: data}, nil
}

// Write creates or replaces a file with data (at most MaxEdit bytes).
func (s *Service) Write(ctx context.Context, id, name string, data []byte) error {
	if len(data) > MaxEdit {
		return fmt.Errorf("%w for the editor (over %d MiB); upload it instead", ErrTooLarge, MaxEdit>>20)
	}
	f, err := s.open(ctx, id, true)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.WriteFile(name, data)
}

// Mkdir creates a directory and any missing parents.
func (s *Service) Mkdir(ctx context.Context, id, name string) error {
	f, err := s.open(ctx, id, true)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Lstat(name); err == nil {
		return &fs.PathError{Op: "mkdir", Path: Rel(name), Err: fs.ErrExist}
	}
	return f.MkdirAll(name)
}

// Move is one rename.
type Move struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Rename moves files, relative to dir. It never replaces an existing file,
// and stops at the first failure (earlier moves stay done).
func (s *Service) Rename(ctx context.Context, id, dir string, moves []Move) error {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, m := range moves {
		if err := f.Rename(Join(dir, m.From), Join(dir, m.To), false); err != nil {
			return err
		}
	}
	return nil
}

// Copy copies a regular file next to itself and returns the copy's name.
func (s *Service) Copy(ctx context.Context, id, name string) (string, error) {
	f, err := s.open(ctx, id, true)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	return f.Copy(name)
}

// Delete removes files and directories (with everything in them), relative
// to dir. Symlinks are removed, never their targets. It stops at the first
// failure; a name that's already gone isn't one.
func (s *Service) Delete(ctx context.Context, id, dir string, names []string) error {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, n := range names {
		if err := f.RemoveAll(Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Chmod is one permission change.
type Chmod struct {
	Name string `json:"name"`
	Mode uint32 `json:"mode"` // permission bits only
}

// Chmod sets permission bits, relative to dir.
func (s *Service) Chmod(ctx context.Context, id, dir string, changes []Chmod) error {
	f, err := s.open(ctx, id, false)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, c := range changes {
		if c.Mode > 0o777 {
			return fmt.Errorf("%s: mode %o: only permission bits (0-0777) can be set", c.Name, c.Mode)
		}
		if err := f.Chmod(Join(dir, c.Name), fs.FileMode(c.Mode)); err != nil {
			return err
		}
	}
	return nil
}

// ArchiveJob is the payload of the archive jobs.
type ArchiveJob struct {
	Dir     string   `json:"dir,omitempty"`     // compress: where the names are, and the archive goes
	Names   []string `json:"names,omitempty"`   // compress
	Format  string   `json:"format,omitempty"`  // compress; default tar.gz
	Name    string   `json:"name,omitempty"`    // compress: the archive's name without its extension
	Archive string   `json:"archive,omitempty"` // decompress
	Dest    string   `json:"dest,omitempty"`    // decompress; default the archive's directory
	UserID  string   `json:"user_id,omitempty"`
}

// Compress queues a job that archives names (relative to dir) into a new
// archive in dir, and returns its ID.
func (s *Service) Compress(ctx context.Context, id, user, dir string, names []string, opts CompressOptions) (string, error) {
	if len(names) == 0 {
		return "", errors.New("nothing to compress")
	}
	if opts.Format != "" && !slices.Contains(Formats, opts.Format) {
		return "", fmt.Errorf("unknown archive format %q (one of %s)", opts.Format, strings.Join(Formats, ", "))
	}
	if n := opts.Name; strings.ContainsAny(n, "/\\\x00") || n == "." || n == ".." || len(n) > 200 {
		return "", errors.New(`an archive's name can't contain a slash, or be "." or ".."`)
	}
	if err := s.o.Servers.CheckFiles(ctx, id, true); err != nil {
		return "", err
	}
	return s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobCompress, ServerID: id, Payload: ArchiveJob{Dir: Rel(dir), Names: names, Format: opts.Format, Name: opts.Name, UserID: user}})
}

// Decompress queues a job that extracts an archive into dest (default its
// directory), and returns its ID.
func (s *Service) Decompress(ctx context.Context, id, user, archive, dest string) (string, error) {
	if err := s.o.Servers.CheckFiles(ctx, id, true); err != nil {
		return "", err
	}
	f, err := s.FS(id)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	// Fail now, not in the job, for what's obviously wrong.
	if fi, err := f.Stat(archive); err != nil {
		return "", err
	} else if !fi.Mode().IsRegular() {
		return "", &fs.PathError{Op: "decompress", Path: Rel(archive), Err: ErrNotRegular}
	}
	if err := f.check(Rel(archive), false); err != nil {
		return "", err
	}
	if dest != "" {
		dest = Rel(dest)
	}
	return s.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobDecompress, ServerID: id, Payload: ArchiveJob{Archive: Rel(archive), Dest: dest, UserID: user}})
}

func (s *Service) compressJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	return s.archiveJob(ctx, j, log, EventCompressed, func(f *FS, p ArchiveJob, space int64) (ArchiveResult, error) {
		_, _ = fmt.Fprintf(log, "compressing %d item(s) in /%s\n", len(p.Names), p.Dir)
		return f.Compress(ctx, p.Dir, p.Names, space, s.o.Now(), CompressOptions{Format: p.Format, Name: p.Name})
	})
}

func (s *Service) decompressJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	return s.archiveJob(ctx, j, log, EventDecompressed, func(f *FS, p ArchiveJob, space int64) (ArchiveResult, error) {
		_, _ = fmt.Fprintf(log, "extracting /%s\n", p.Archive)
		return f.Extract(ctx, p.Archive, p.Dest, space)
	})
}

func (s *Service) archiveJob(ctx context.Context, j jobs.Job, log io.Writer, event string, run func(*FS, ArchiveJob, int64) (ArchiveResult, error)) (any, error) {
	var p ArchiveJob
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	res, err := func() (ArchiveResult, error) {
		f, err := s.open(ctx, j.ServerID, true)
		if err != nil {
			return ArchiveResult{}, err
		}
		defer func() { _ = f.Close() }()
		space, err := s.o.Servers.SpaceLeft(ctx, j.ServerID)
		if err != nil {
			return ArchiveResult{}, err
		}
		return run(f, p, space)
	}()
	data := map[string]any{"job_id": j.ID, "user_id": p.UserID, "files": res.Files, "bytes": res.Bytes}
	if res.Archive != "" {
		data["archive"] = res.Archive
	}
	if res.Skipped > 0 {
		data["skipped"] = res.Skipped
		_, _ = fmt.Fprintf(log, "left out %d denied, unsafe, or special file(s)\n", res.Skipped)
	}
	if err != nil {
		data["error"] = err.Error()
		_, _ = fmt.Fprintf(log, "failed after %d file(s): %v\n", res.Files, err)
	} else {
		_, _ = fmt.Fprintf(log, "done: %d file(s), %d bytes\n", res.Files, res.Bytes)
	}
	// Interrupted by a Wings stop: the job resumes; nothing to report yet.
	if ctx.Err() == nil && s.o.Events != nil {
		if _, aerr := s.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: event, ServerID: j.ServerID, Data: data}); aerr != nil {
			s.o.Log.Error("recording event failed", "server", j.ServerID, "event", event, "err", aerr)
		}
	}
	if err != nil {
		return nil, err
	}
	return res, nil
}
