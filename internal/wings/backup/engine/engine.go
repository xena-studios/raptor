// Package engine does the backup work with Kopia: snapshots, restores,
// deletes, and repository maintenance. It runs in the backup worker process,
// not in Wings itself (docs/WINGS.md#backups).
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/maintenance"
	"github.com/kopia/kopia/repo/manifest"
	"github.com/kopia/kopia/snapshot"
	"github.com/kopia/kopia/snapshot/policy"
	"github.com/kopia/kopia/snapshot/restore"
	"github.com/kopia/kopia/snapshot/snapshotfs"
	"github.com/kopia/kopia/snapshot/snapshotmaintenance"
	"github.com/kopia/kopia/snapshot/upload"
)

// Engine works on one destination's repository.
type Engine struct {
	Dest     Destination
	Password string
	// StateDir holds the repository connection and cache (per destination).
	StateDir string
}

// Snapshot source naming: every backup of a server shares a source, so
// uploads are incremental against its previous backup.
const (
	sourceHost = "raptor"
	sourceUser = "wings"
)

func source(serverID string) snapshot.SourceInfo {
	return snapshot.SourceInfo{Host: sourceHost, UserName: sourceUser, Path: "/servers/" + serverID}
}

// ErrTooLarge is returned by Restore when the backup is bigger than the
// allowed size.
var ErrTooLarge = errors.New("the backup is larger than the server's disk limit")

// SnapshotRequest backs up a server directory.
type SnapshotRequest struct {
	ServerID string   `json:"server_id"`
	BackupID string   `json:"backup_id"`
	Dir      string   `json:"dir"`
	Ignore   []string `json:"ignore,omitempty"` // gitignore-style patterns
}

// SnapshotResult describes a finished snapshot.
type SnapshotResult struct {
	SnapshotID string `json:"snapshot_id"`
	Size       int64  `json:"size"`     // total size of the files
	Files      int64  `json:"files"`    // files backed up
	Uploaded   int64  `json:"uploaded"` // new data written to the repository
	// Skipped counts files and directories that couldn't be read (they
	// vanished or changed type while being backed up).
	Skipped int `json:"skipped,omitempty"`
}

// Progress reports how far an operation is.
type Progress struct {
	Bytes int64 `json:"bytes"` // processed so far
	Total int64 `json:"total"` // estimate; 0 = unknown
}

// Snapshot backs up req.Dir.
func (e *Engine) Snapshot(ctx context.Context, req SnapshotRequest, progress func(Progress)) (SnapshotResult, error) {
	root, err := os.OpenRoot(req.Dir)
	if err != nil {
		return SnapshotResult{}, err
	}
	defer func() { _ = root.Close() }()
	fi, err := root.Stat(".")
	if err != nil {
		return SnapshotResult{}, err
	}
	dir := newEntry(root, ".", fi)

	rep, err := e.open(ctx)
	if err != nil {
		return SnapshotResult{}, err
	}
	defer func() { _ = rep.Close(ctx) }()

	src := source(req.ServerID)
	var res SnapshotResult
	var uploaded atomic.Int64
	opts := repo.WriteSessionOptions{Purpose: "backup", OnUpload: func(n int64) { uploaded.Add(n) }}
	err = repo.WriteSession(ctx, rep, opts, func(ctx context.Context, w repo.RepositoryWriter) error {
		previous, err := snapshot.FindPreviousManifests(ctx, w, src, nil)
		if err != nil {
			return err
		}
		u := upload.NewUploader(w)
		p := &uploadProgress{cb: progress}
		u.Progress = p
		man, err := u.Upload(ctx, dir, snapshotPolicy(req.Ignore, src), src, previous...)
		if err != nil {
			return err
		}
		if man.IncompleteReason != "" {
			return fmt.Errorf("backup incomplete: %s", man.IncompleteReason)
		}
		man.Description = req.BackupID
		man.Tags = map[string]string{"tag:backup": req.BackupID}
		id, err := snapshot.SaveSnapshot(ctx, w, man)
		if err != nil {
			return err
		}
		res = SnapshotResult{SnapshotID: string(id)}
		if s := man.RootEntry.DirSummary; s != nil {
			res.Size, res.Files = s.TotalFileSize, s.TotalFileCount
			res.Skipped = s.IgnoredErrorCount
		}
		return nil
	})
	res.Uploaded = uploaded.Load() // counted until the session's final flush
	return res, err
}

var parallelReads policy.OptionalInt = 2

// snapshotPolicy is the policy for every backup: nothing is stored in the
// repository, so the rules come from Wings each time.
func snapshotPolicy(ignore []string, src snapshot.SourceInfo) *policy.Tree {
	yes := policy.NewOptionalBool(true)
	p := &policy.Policy{
		FilesPolicy: policy.FilesPolicy{
			IgnoreRules: ignore,
			// Pterodactyl's ignore file, for servers moved from it.
			DotIgnoreFiles: []string{".pteroignore"},
		},
		// A file deleted or replaced while it's read (a game rotating a log)
		// is skipped rather than failing the backup; the count is reported.
		ErrorHandlingPolicy: policy.ErrorHandlingPolicy{
			IgnoreFileErrors: yes, IgnoreDirectoryErrors: yes, IgnoreUnknownTypes: yes,
		},
		CompressionPolicy: policy.CompressionPolicy{CompressorName: "zstd-fastest"},
		// Few parallel reads: backups mustn't compete with game servers for
		// disk (the worker also runs with low I/O weight).
		UploadPolicy: policy.UploadPolicy{MaxParallelFileReads: &parallelReads},
	}
	merged, _ := policy.MergePolicies([]*policy.Policy{p, policy.DefaultPolicy}, src)
	merged.Actions = policy.ActionsPolicy{} // never run commands from a policy
	return policy.BuildTree(map[string]*policy.Policy{".": merged}, merged)
}

// RestoreRequest restores a snapshot into a directory.
type RestoreRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Dir        string `json:"dir"`
	UID        int    `json:"uid"`
	GID        int    `json:"gid"`
	// MaxSize > 0 refuses backups whose files add up to more (the server's
	// disk limit), before anything is changed.
	MaxSize int64 `json:"max_size,omitempty"`
}

// RestoreResult describes a finished restore.
type RestoreResult struct {
	Size  int64 `json:"size"`
	Files int64 `json:"files"`
}

// Restore replaces the contents of req.Dir with the snapshot. The directory
// itself (its owner, mode, and disk quota) is kept.
func (e *Engine) Restore(ctx context.Context, req RestoreRequest, progress func(Progress)) (RestoreResult, error) {
	rep, err := e.open(ctx)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = rep.Close(ctx) }()
	man, err := snapshot.LoadSnapshot(ctx, rep, manifest.ID(req.SnapshotID))
	if err != nil {
		return RestoreResult{}, err
	}
	var total int64
	if s := man.RootEntry.DirSummary; s != nil {
		total = s.TotalFileSize
	}
	if req.MaxSize > 0 && total > req.MaxSize {
		return RestoreResult{}, fmt.Errorf("%w (%d bytes, limit %d)", ErrTooLarge, total, req.MaxSize)
	}
	entry, err := snapshotfs.SnapshotRoot(rep, man)
	if err != nil {
		return RestoreResult{}, err
	}

	root, err := os.OpenRoot(req.Dir)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = root.Close() }()
	if err := clearDir(root); err != nil {
		return RestoreResult{}, fmt.Errorf("clear the server directory: %w", err)
	}
	st, err := restore.Entry(ctx, rep, &rootOutput{root: root, uid: req.UID, gid: req.GID}, entry, restore.Options{
		Parallel:               2,
		RestoreDirEntryAtDepth: math.MaxInt32, // everything, no placeholders
		ProgressCallback: func(_ context.Context, s restore.Stats) {
			if progress != nil {
				progress(Progress{Bytes: s.RestoredTotalFileSize, Total: total})
			}
		},
	})
	if err != nil {
		return RestoreResult{}, err
	}
	return RestoreResult{Size: st.RestoredTotalFileSize, Files: int64(st.RestoredFileCount)}, nil
}

// clearDir removes everything inside the root, without following symlinks.
func clearDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := root.RemoveAll(n); err != nil {
			return err
		}
	}
	return nil
}

// Delete deletes snapshots. Their data is freed by later maintenance.
// Snapshots that don't exist are ignored.
func (e *Engine) Delete(ctx context.Context, snapshotIDs []string) error {
	rep, err := e.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = rep.Close(ctx) }()
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "delete"}, func(ctx context.Context, w repo.RepositoryWriter) error {
		for _, id := range snapshotIDs {
			if err := w.DeleteManifest(ctx, manifest.ID(id)); err != nil && !errors.Is(err, manifest.ErrNotFound) {
				return err
			}
		}
		return nil
	})
}

// Maintain runs repository maintenance when it's due: quick maintenance
// hourly and full maintenance (which frees the data of deleted snapshots,
// after a safety delay) daily. Running it more often does nothing.
func (e *Engine) Maintain(ctx context.Context) error {
	rep, err := e.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = rep.Close(ctx) }()
	dr, ok := rep.(repo.DirectRepository)
	if !ok {
		return errors.New("not a direct repository")
	}
	return repo.DirectWriteSession(ctx, dr, repo.WriteSessionOptions{Purpose: "maintenance"}, func(ctx context.Context, w repo.DirectRepositoryWriter) error {
		return snapshotmaintenance.Run(ctx, w, maintenance.ModeAuto, false, maintenance.SafetyFull)
	})
}

// clientOptions identify Wings as the repository's only client, which is
// also the maintenance owner.
func clientOptions() repo.ClientOptions {
	return repo.ClientOptions{Hostname: sourceHost, Username: sourceUser}
}

// open opens the repository, creating it on first use. The connection
// (config file) is named after a hash of the destination settings, so
// changed settings connect again.
func (e *Engine) open(ctx context.Context) (repo.Repository, error) {
	cfg, err := json.Marshal(e.Dest)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(cfg)
	name := e.Dest.ID + "-" + hex.EncodeToString(sum[:6])
	configFile := filepath.Join(e.StateDir, name+".config")
	if _, err := os.Stat(configFile); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(e.StateDir, 0o700); err != nil {
			return nil, err
		}
		if err := e.connect(ctx, configFile, filepath.Join(e.StateDir, "cache", e.Dest.ID)); err != nil {
			return nil, err
		}
		e.removeStale(configFile)
	}
	return repo.Open(ctx, configFile, e.Password, &repo.Options{})
}

func (e *Engine) connect(ctx context.Context, configFile, cacheDir string) error {
	st, err := e.storage(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(ctx) }()
	created := false
	// A read-only destination is someone else's backups (or this node's,
	// in the past): a repository is found there, never made.
	if !e.Dest.ReadOnly {
		switch err := repo.Initialize(ctx, st, &repo.NewRepositoryOptions{}, e.Password); {
		case err == nil:
			created = true
		case !errors.Is(err, repo.ErrAlreadyInitialized):
			return fmt.Errorf("create repository: %w", err)
		}
	}
	caching := content.CachingOptions{}
	if e.Dest.Type != Local {
		// Remote repositories cache their indexes and metadata locally.
		caching = content.CachingOptions{CacheDirectory: cacheDir, MetadataCacheSizeBytes: 256 << 20}
	}
	opts := clientOptions()
	opts.ReadOnly = e.Dest.ReadOnly
	if err := repo.Connect(ctx, configFile, st, e.Password, &repo.ConnectOptions{ClientOptions: opts, CachingOptions: caching}); err != nil {
		if e.Dest.ReadOnly && errors.Is(err, repo.ErrRepositoryNotInitialized) {
			return ErrNoRepository
		}
		return fmt.Errorf("connect to repository: %w", err)
	}
	if created {
		return e.claimMaintenance(ctx, configFile)
	}
	return nil
}

// ErrNoRepository means a read-only destination has no backups to find.
var ErrNoRepository = errors.New("there are no Raptor backups there; check the folder and the key")

// Found is a backup in a repository: what a recovery lists.
type Found struct {
	SnapshotID string    `json:"snapshot_id"`
	ServerID   string    `json:"server_id"`
	BackupID   string    `json:"backup_id"` // the ID it had where it was made
	At         time.Time `json:"at"`
	Size       int64     `json:"size"`
	Files      int64     `json:"files"`
}

// List finds every server backup in the repository.
func (e *Engine) List(ctx context.Context) ([]Found, error) {
	rep, err := e.open(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rep.Close(ctx) }()
	ids, err := snapshot.ListSnapshotManifests(ctx, rep, nil, nil)
	if err != nil {
		return nil, err
	}
	mans, err := snapshot.LoadSnapshots(ctx, rep, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Found, 0, len(mans))
	for _, m := range mans {
		id, ok := strings.CutPrefix(m.Source.Path, "/servers/")
		if !ok || m.Source.Host != sourceHost || m.IncompleteReason != "" {
			continue // not a server backup made by Raptor
		}
		f := Found{SnapshotID: string(m.ID), ServerID: id, BackupID: m.Description, At: m.StartTime.ToTime()}
		if s := m.RootEntry.DirSummary; s != nil {
			f.Size, f.Files = s.TotalFileSize, s.TotalFileCount
		}
		out = append(out, f)
	}
	return out, nil
}

// claimMaintenance makes Wings the repository's maintenance owner.
func (e *Engine) claimMaintenance(ctx context.Context, configFile string) error {
	rep, err := repo.Open(ctx, configFile, e.Password, &repo.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = rep.Close(ctx) }()
	return repo.WriteSession(ctx, rep, repo.WriteSessionOptions{Purpose: "maintenance owner"}, func(ctx context.Context, w repo.RepositoryWriter) error {
		p := maintenance.DefaultParams()
		p.Owner = clientOptions().UsernameAtHost()
		return maintenance.SetParams(ctx, w, &p)
	})
}

func (e *Engine) removeStale(keep string) {
	matches, _ := filepath.Glob(filepath.Join(e.StateDir, e.Dest.ID+"-*.config"))
	for _, m := range matches {
		if m != keep {
			_ = os.Remove(m)
		}
	}
}

// uploadProgress reports hashed bytes against the estimated total.
type uploadProgress struct {
	upload.NullUploadProgress
	cb     func(Progress)
	total  atomic.Int64
	hashed atomic.Int64
	last   atomic.Int64 // unix ms of the last report
}

func (p *uploadProgress) Enabled() bool { return true }

func (p *uploadProgress) EstimatedDataSize(_, bytes int64) { p.total.Store(bytes) }

func (p *uploadProgress) HashedBytes(n int64) { p.report(p.hashed.Add(n)) }

func (p *uploadProgress) CachedFile(_ string, n int64) { p.report(p.hashed.Add(n)) }

// report calls back at most once a second.
func (p *uploadProgress) report(done int64) {
	if p.cb == nil {
		return
	}
	now := time.Now().UnixMilli()
	last := p.last.Load()
	if now-last < 1000 || !p.last.CompareAndSwap(last, now) {
		return
	}
	p.cb(Progress{Bytes: done, Total: p.total.Load()})
}
