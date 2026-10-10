package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Backups is the part of the backup manager the local API uses
// (*backup.Manager).
type Backups interface {
	List(ctx context.Context, serverID string) ([]*backup.Backup, error)
	Get(ctx context.Context, serverID, id string) (*backup.Backup, error)
	Create(ctx context.Context, serverID string, opts backup.CreateOptions) ([]*backup.Backup, error)
	ShowKey(ctx context.Context) (backup.Key, error)
	KeyMode(ctx context.Context) (string, error)
	Restore(ctx context.Context, serverID, id, user string) (string, error)
}

// Jobs lets calls wait for the jobs they queue (*jobs.Engine).
type Jobs interface {
	Wait(ctx context.Context, id string) (jobs.Job, error)
}

// SetBackups makes the backup manager available (with the server manager,
// once the container runtime is ready).
func (s *Service) SetBackups(b Backups, j Jobs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backups, s.jobs = b, j
}

func (s *Service) backupManager() (Servers, Backups, Jobs, error) {
	srv, err := s.serverManager()
	if err != nil {
		return nil, nil, nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.backups == nil {
		return nil, nil, nil, connect.NewError(connect.CodeUnavailable, errors.New("backups aren't ready yet; see raptor-wings logs"))
	}
	return srv, s.backups, s.jobs, nil
}

// backupErr maps backup errors to Connect codes.
func backupErr(err error) error {
	switch {
	case errors.Is(err, backup.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, backup.ErrInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, backup.ErrNotReady), errors.Is(err, backup.ErrLowDisk), errors.Is(err, backup.ErrDestination),
		errors.Is(err, server.ErrRestoring), errors.Is(err, server.ErrDeleting):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return connectErr(err)
}

// resolveBackup finds a backup to restore onto a server by full ID or ID
// suffix (at least shortID characters): one of the server's own, or of a
// server that was deleted (its final backup, or offsite backups kept after
// the deletion). Backup IDs are UUIDv7 like server IDs, so their ends are
// the distinctive part.
func resolveBackup(ctx context.Context, b Backups, serverID, ref string, deleted func(serverID string) bool) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("no backup given"))
	}
	list, err := b.List(ctx, "")
	if err != nil {
		return "", err
	}
	var matches []string
	for _, bk := range list {
		if bk.ServerID != serverID && !deleted(bk.ServerID) {
			continue
		}
		if bk.ID == ref {
			return bk.ID, nil
		}
		if len(ref) >= shortID && strings.HasSuffix(bk.ID, ref) {
			matches = append(matches, bk.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("the server has no backup %q (see raptor backup list)", ref))
	}
	sort.Strings(matches)
	return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%q matches several backups (%s); use the ID", ref, strings.Join(matches, ", ")))
}

func backupInfo(b *backup.Backup, serverName string) *localv1.BackupInfo {
	info := &localv1.BackupInfo{
		Id: b.ID, ServerId: b.ServerID, ServerName: serverName, DestinationId: b.DestinationID,
		Kind: b.Kind, Status: b.Status, Locked: b.Locked, SizeBytes: b.Size, Files: b.Files,
		UploadedBytes: b.Uploaded, Warning: b.Warning, Error: b.Error, CreatedBy: b.CreatedBy,
		CreatedAt: timestamppb.New(b.CreatedAt), JobId: b.JobID,
	}
	if !b.FinishedAt.IsZero() {
		info.FinishedAt = timestamppb.New(b.FinishedAt)
	}
	if !b.ExpiresAt.IsZero() {
		info.ExpiresAt = timestamppb.New(b.ExpiresAt)
	}
	return info
}

// ListBackups lists a server's backups, or all of them.
func (s *Service) ListBackups(ctx context.Context, req *localv1.ListBackupsRequest) (*localv1.ListBackupsResponse, error) {
	srv, b, _, err := s.backupManager()
	if err != nil {
		return nil, err
	}
	id := ""
	if req.GetServer() != "" {
		if id, err = resolve(ctx, srv, req.GetServer()); err != nil {
			return nil, err
		}
	}
	list, err := b.List(ctx, id)
	if err != nil {
		return nil, backupErr(err)
	}
	names := map[string]string{}
	out := make([]*localv1.BackupInfo, len(list))
	for i, bk := range list {
		name, ok := names[bk.ServerID]
		if !ok {
			if cfg, err := srv.Get(ctx, bk.ServerID); err == nil {
				name = cfg.Name
			}
			names[bk.ServerID] = name
		}
		out[i] = backupInfo(bk, name)
	}
	return &localv1.ListBackupsResponse{Backups: out}, nil
}

// GetBackupKey shows the backup key, to root.
func (s *Service) GetBackupKey(ctx context.Context, _ *localv1.GetBackupKeyRequest) (*localv1.GetBackupKeyResponse, error) {
	if err := requireRoot(ctx, "see the backup key"); err != nil {
		return nil, err
	}
	_, b, _, err := s.backupManager()
	if err != nil {
		return nil, err
	}
	k, err := b.ShowKey(ctx)
	if err != nil {
		return nil, err
	}
	mode, err := b.KeyMode(ctx)
	if err != nil {
		return nil, err
	}
	return &localv1.GetBackupKeyResponse{Key: k.Key, Fingerprint: k.Fingerprint, Mode: mode}, nil
}

// CreateBackup backs up a server as the calling Unix user.
func (s *Service) CreateBackup(ctx context.Context, req *localv1.CreateBackupRequest) (*localv1.CreateBackupResponse, error) {
	if err := requireRoot(ctx, "make backups"); err != nil {
		return nil, err
	}
	srv, b, j, err := s.backupManager()
	if err != nil {
		return nil, err
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return nil, err
	}
	list, err := b.Create(ctx, id, backup.CreateOptions{Kind: backup.KindManual, User: Caller(ctx), Locked: req.GetLocked()})
	if err != nil {
		return nil, backupErr(err)
	}
	bk := list[0] // the primary destination's; the others are in the same job
	if req.GetWait() {
		job, err := j.Wait(ctx, bk.JobID)
		if err != nil {
			return nil, err
		}
		if bk, err = b.Get(ctx, id, bk.ID); err != nil {
			return nil, backupErr(err)
		}
		if job.Status != jobs.Succeeded {
			return nil, connect.NewError(connect.CodeAborted, fmt.Errorf("backup %s: %s", job.Status, job.Error))
		}
	}
	cfg, _ := srv.Get(ctx, id)
	name := ""
	if cfg != nil {
		name = cfg.Name
	}
	return &localv1.CreateBackupResponse{Backup: backupInfo(bk, name)}, nil
}

// RestoreBackup restores one of a server's backups over its files, as the
// calling Unix user. Unlike the same command from the Panel it isn't signed
// with a passkey: root on the box already controls everything on it.
func (s *Service) RestoreBackup(ctx context.Context, req *localv1.RestoreBackupRequest) (*localv1.RestoreBackupResponse, error) {
	if err := requireRoot(ctx, "restore backups"); err != nil {
		return nil, err
	}
	srv, b, j, err := s.backupManager()
	if err != nil {
		return nil, err
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return nil, err
	}
	existing := srv.List()
	bid, err := resolveBackup(ctx, b, id, req.GetBackup(), func(sid string) bool {
		_, ok := existing[sid]
		return !ok
	})
	if err != nil {
		return nil, backupErr(err)
	}
	jobID, err := b.Restore(ctx, id, bid, Caller(ctx))
	if err != nil {
		return nil, backupErr(err)
	}
	resp := &localv1.RestoreBackupResponse{JobId: jobID}
	if !req.GetWait() {
		return resp, nil
	}
	job, err := j.Wait(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != jobs.Succeeded {
		return nil, connect.NewError(connect.CodeAborted, fmt.Errorf("restore %s: %s", job.Status, job.Error))
	}
	var res struct {
		SafetyBackupID string `json:"safety_backup_id"`
	}
	if len(job.Result) > 0 {
		_ = json.Unmarshal(job.Result, &res)
	}
	resp.SafetyBackupId = res.SafetyBackupID
	return resp, nil
}
