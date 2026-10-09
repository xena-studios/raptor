package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// What a server's backups are doing (backup.activity), for the Panel to
// show progress: kept in memory, the running ones and the last few that
// finished since Wings started.

// Activity kinds.
const (
	ActBackup  = "backup"
	ActRestore = "restore"
	ActExtract = "extract"
)

// Activity is one backup, restore, or extraction.
type Activity struct {
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"`
	BackupID string `json:"backup_id"`
	// BackupKind is a backup's kind (manual, scheduled), for a backup.
	BackupKind string `json:"backup_kind,omitempty"`
	Status     string `json:"status"` // queued, running, ok, or failed
	Bytes      int64  `json:"bytes"`
	Total      int64  `json:"total"`
	Files      int64  `json:"files,omitempty"`
	Error      string `json:"error,omitempty"`
	// Folder is where an extraction put its files.
	Folder     string `json:"folder,omitempty"`
	Paths      int    `json:"paths,omitempty"`
	StartedAt  int64  `json:"started_at"` // unix ms
	FinishedAt int64  `json:"finished_at,omitempty"`
}

// keepFinished is how many finished activities a server keeps.
const keepFinished = 10

type activities struct {
	mu  sync.Mutex
	all map[string][]*Activity // by server, oldest first
}

func (m *Manager) begin(serverID string, a Activity) *Activity {
	a.Status, a.StartedAt = "running", m.o.Now().UnixMilli()
	m.acts.mu.Lock()
	defer m.acts.mu.Unlock()
	if m.acts.all == nil {
		m.acts.all = map[string][]*Activity{}
	}
	list := m.acts.all[serverID]
	// A resumed job takes over its entry.
	list = slices.DeleteFunc(list, func(x *Activity) bool { return x.JobID == a.JobID })
	p := &a
	m.acts.all[serverID] = append(list, p)
	return p
}

// queued shows a job that's waiting to run (for its server's lock, or a
// free worker).
func (m *Manager) queued(serverID string, a Activity) {
	q := m.begin(serverID, a)
	m.acts.mu.Lock()
	q.Status = "queued"
	m.acts.mu.Unlock()
}

// track reports progress to the activity and the job's log.
func (m *Manager) track(a *Activity, log io.Writer) func(engine.Progress) {
	logged := progressLog(log)
	return func(p engine.Progress) {
		m.acts.mu.Lock()
		a.Bytes, a.Total = p.Bytes, p.Total
		m.acts.mu.Unlock()
		logged(p)
	}
}

func (m *Manager) end(serverID string, a *Activity, err error, files, size int64) {
	m.acts.mu.Lock()
	defer m.acts.mu.Unlock()
	a.FinishedAt = m.o.Now().UnixMilli()
	if err != nil {
		a.Status, a.Error = "failed", err.Error()
	} else {
		a.Status, a.Files = "ok", files
		if size > 0 {
			a.Bytes = size
			if a.Total < size {
				a.Total = size
			}
		}
	}
	// Drop the oldest finished ones past the limit.
	list := m.acts.all[serverID]
	finished := 0
	for i := len(list) - 1; i >= 0; i-- {
		if list[i].Status == "ok" || list[i].Status == "failed" {
			finished++
			if finished > keepFinished {
				list = slices.Delete(list, i, i+1)
			}
		}
	}
	m.acts.all[serverID] = list
}

// Activity returns a server's activities, newest first.
func (m *Manager) Activity(serverID string) []Activity {
	m.acts.mu.Lock()
	defer m.acts.mu.Unlock()
	list := m.acts.all[serverID]
	out := make([]Activity, 0, len(list))
	for i := len(list) - 1; i >= 0; i-- {
		out = append(out, *list[i])
	}
	return out
}

// --- browsing and extracting ---

// Browse lists one folder of a server's backup.
func (m *Manager) Browse(ctx context.Context, serverID, id, dir string) ([]engine.BrowseEntry, error) {
	b, err := m.Restorable(ctx, serverID, id)
	if err != nil {
		return nil, err
	}
	if b.Status != StatusOK {
		return nil, ErrNotReady
	}
	src, err := m.o.Servers.BackupSource(ctx, serverID)
	if err != nil {
		return nil, err
	}
	out, err := m.run(ctx, b.DestinationID, request{Op: opBrowse, Browse: &engine.BrowseRequest{
		SnapshotID: b.snapshotID, Path: dir, Deny: src.Denylist,
	}}, nil, io.Discard)
	if err != nil {
		return nil, err
	}
	var list []engine.BrowseEntry
	return list, json.Unmarshal(out, &list)
}

type extractPayload struct {
	BackupID string   `json:"backup_id"`
	Paths    []string `json:"paths"`
	Folder   string   `json:"folder"`
	User     string   `json:"user"`
}

// RestoreFolder is where extractions go, in the server's directory.
const RestoreFolder = ".restore"

// Extract restores some of a backup's files and folders into a new folder
// under .restore, named for when the backup was taken. The server keeps
// running and nothing it has is replaced.
func (m *Manager) Extract(ctx context.Context, serverID, id string, paths []string, user string) (jobID, folder string, err error) {
	b, err := m.Restorable(ctx, serverID, id)
	if err != nil {
		return "", "", err
	}
	if b.Status != StatusOK {
		return "", "", ErrNotReady
	}
	if len(paths) == 0 || len(paths) > 1000 {
		return "", "", errors.New("pick 1 to 1000 files and folders")
	}
	src, err := m.o.Servers.BackupSource(ctx, serverID)
	if err != nil {
		return "", "", err
	}
	folder = freeFolder(src.Dir, path.Join(RestoreFolder, b.CreatedAt.In(m.o.Location).Format("2006-01-02_15-04")))
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		jobID, err = m.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobExtract, ServerID: serverID, Payload: extractPayload{
			BackupID: id, Paths: paths, Folder: folder, User: user,
		}})
		return err
	})
	if err != nil {
		return "", "", err
	}
	m.queued(serverID, Activity{JobID: jobID, Kind: ActExtract, BackupID: id, Folder: folder, Paths: len(paths)})
	m.o.Jobs.Wake()
	return jobID, folder, nil
}

// freeFolder is name, or name-2, name-3, … if it's taken.
func freeFolder(dir, name string) string {
	try := name
	for i := 2; i < 1000; i++ {
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(try))); errors.Is(err, os.ErrNotExist) {
			return try
		}
		try = fmt.Sprintf("%s-%d", name, i)
	}
	return try
}

func (m *Manager) extractJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p extractPayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	b, err := m.row(ctx, p.BackupID)
	if err != nil {
		return nil, err
	}
	a := m.begin(j.ServerID, Activity{JobID: j.ID, Kind: ActExtract, BackupID: b.ID, Folder: p.Folder, Paths: len(p.Paths)})
	src, err := m.o.Servers.BackupSource(ctx, j.ServerID)
	var res engine.RestoreResult
	if err == nil {
		_, _ = fmt.Fprintf(log, "restoring %d files and folders from backup %s into %s\n", len(p.Paths), b.ID, p.Folder)
		var out json.RawMessage
		out, err = m.run(ctx, b.DestinationID, request{Op: opExtract, Extract: &engine.ExtractRequest{
			SnapshotID: b.snapshotID, Dir: src.Dir, Into: p.Folder, Paths: p.Paths, Deny: src.Denylist,
			UID: src.UID, GID: src.GID, MaxSize: src.DiskLimit,
		}}, m.track(a, log), log)
		if err == nil {
			err = json.Unmarshal(out, &res)
		}
	}
	m.end(j.ServerID, a, err, res.Files, res.Size)
	data := map[string]any{"backup_id": b.ID, "job_id": j.ID, "user": p.User, "folder": p.Folder, "ok": err == nil}
	if err != nil {
		data["error"] = err.Error()
	}
	if _, aerr := m.o.Events.Append(context.WithoutCancel(ctx), events.Event{Type: EventExtracted, ServerID: j.ServerID, Data: data}); aerr != nil {
		m.log.Error("recording an extraction failed", "job", j.ID, "err", aerr)
	}
	m.o.Events.Wake()
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(log, "restored %d files, %d bytes\n", res.Files, res.Size)
	return map[string]any{"backup_id": b.ID, "folder": p.Folder, "files": res.Files, "size": res.Size}, nil
}
