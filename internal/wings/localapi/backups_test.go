package localapi

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/jobs"
)

const survival = "0190a1b2-0000-7000-8000-00000000aaaa"

// fakeBackups is a backup manager with fixed backups. Its jobs finish with
// the status in fail (Succeeded if empty).
type fakeBackups struct {
	mu       sync.Mutex
	backups  []*backup.Backup
	created  []string // "server user locked"
	restored []string // "server backup user"
	fail     string
}

func newFakeBackups() *fakeBackups {
	at := time.Date(2026, 3, 2, 4, 0, 0, 0, time.UTC)
	return &fakeBackups{backups: []*backup.Backup{
		{ID: "01a0f000-0000-7000-8000-0000000b0001", ServerID: survival, Kind: backup.KindScheduled, Status: backup.StatusOK, CreatedAt: at},
		{ID: "01a0f000-0000-7000-8000-0000000b0002", ServerID: survival, Kind: backup.KindManual, Status: backup.StatusOK, CreatedAt: at.Add(time.Hour)},
		{ID: "01a0f000-0000-7000-8000-1000000b0002", ServerID: survival, Kind: backup.KindManual, Status: backup.StatusOK, CreatedAt: at.Add(2 * time.Hour)},
		// A final backup of a deleted server.
		{ID: "01a0f000-0000-7000-8000-0000000b0003", ServerID: "0190a1b2-0000-7000-8000-00000000dead", Kind: backup.KindFinal, Status: backup.StatusOK, CreatedAt: at},
		// A backup of another server that still exists.
		{ID: "01a0f000-0000-7000-8000-0000000b0004", ServerID: "0190a1b2-0000-7000-8000-00000000bbbb", Kind: backup.KindManual, Status: backup.StatusOK, CreatedAt: at},
	}}
}

func (f *fakeBackups) List(_ context.Context, serverID string) ([]*backup.Backup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*backup.Backup
	for _, b := range f.backups {
		if serverID == "" || b.ServerID == serverID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeBackups) Get(ctx context.Context, serverID, id string) (*backup.Backup, error) {
	list, _ := f.List(ctx, serverID)
	for _, b := range list {
		if b.ID == id {
			return b, nil
		}
	}
	return nil, backup.ErrNotFound
}

func (f *fakeBackups) Create(_ context.Context, serverID string, o backup.CreateOptions) (*backup.Backup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, serverID+" "+o.User+" "+map[bool]string{true: "locked", false: "unlocked"}[o.Locked])
	b := &backup.Backup{ID: "01a0f000-0000-7000-8000-0000000b0009", ServerID: serverID, Kind: o.Kind, Status: backup.StatusOK, JobID: "job-create", CreatedAt: time.Now()}
	f.backups = append(f.backups, b)
	return b, nil
}

func (f *fakeBackups) Restore(_ context.Context, serverID, id, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored = append(f.restored, serverID+" "+id+" "+user)
	return "job-restore", nil
}

func (f *fakeBackups) Wait(_ context.Context, id string) (jobs.Job, error) {
	j := jobs.Job{ID: id, Status: jobs.Succeeded, Result: json.RawMessage(`{"safety_backup_id":"01a0f000-0000-7000-8000-0000000b0005"}`)}
	if f.fail != "" {
		j.Status, j.Error = jobs.Failed, f.fail
	}
	return j, nil
}

func backupService() (*Service, *fakeBackups) {
	s := &Service{}
	s.SetServers(newFakeManager())
	b := newFakeBackups()
	s.SetBackups(b, b)
	return s, b
}

var (
	userCtx = context.WithValue(context.Background(), callerKey{}, "local:alice")
	rootCtx = context.WithValue(context.WithValue(context.Background(), rootKey{}, true), callerKey{}, "local:root")
)

func TestListBackups(t *testing.T) {
	s, _ := backupService()
	// Listing is read-only: the raptor group may.
	res, err := s.ListBackups(userCtx, &localv1.ListBackupsRequest{Server: "survival"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetBackups()) != 3 || res.GetBackups()[0].GetServerName() != "survival" {
		t.Fatalf("server list: %v", res.GetBackups())
	}
	res, err = s.ListBackups(userCtx, &localv1.ListBackupsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(res.GetBackups()); n != 5 || res.GetBackups()[3].GetServerName() != "" {
		t.Fatalf("node list: %v", res.GetBackups())
	}
	if _, err := s.ListBackups(userCtx, &localv1.ListBackupsRequest{Server: "nope"}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("unknown server: %v", err)
	}
	// Not ready yet.
	if _, err := (&Service{}).ListBackups(userCtx, &localv1.ListBackupsRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("before setup: %v", err)
	}
}

func TestCreateAndRestoreBackup(t *testing.T) {
	s, f := backupService()
	if _, err := s.CreateBackup(userCtx, &localv1.CreateBackupRequest{Server: "survival"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-root create: %v", err)
	}
	if _, err := s.RestoreBackup(userCtx, &localv1.RestoreBackupRequest{Server: "survival", Backup: "0000b0001"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-root restore: %v", err)
	}

	res, err := s.CreateBackup(rootCtx, &localv1.CreateBackupRequest{Server: "0000aaaa", Locked: true, Wait: true})
	if err != nil || res.GetBackup().GetServerName() != "survival" || res.GetBackup().GetStatus() != "ok" {
		t.Fatalf("create: %v, %v", res, err)
	}
	if !slices.Equal(f.created, []string{survival + " local:root locked"}) {
		t.Fatalf("created %q", f.created)
	}

	for ref, code := range map[string]connect.Code{
		"":               connect.CodeInvalidArgument,
		"b0001":          connect.CodeNotFound,        // suffixes need 8 characters
		"000b0002":       connect.CodeInvalidArgument, // two backups end like this
		"0000000b0004":   connect.CodeNotFound,        // another existing server's backup
		"does-not-exist": connect.CodeNotFound,
	} {
		if _, err := s.RestoreBackup(rootCtx, &localv1.RestoreBackupRequest{Server: "survival", Backup: ref}); connect.CodeOf(err) != code {
			t.Errorf("restore %q: %v, want %v", ref, err, code)
		}
	}
	rres, err := s.RestoreBackup(rootCtx, &localv1.RestoreBackupRequest{Server: "survival", Backup: "0000000b0001", Wait: true})
	if err != nil || rres.GetJobId() != "job-restore" || rres.GetSafetyBackupId() != "01a0f000-0000-7000-8000-0000000b0005" {
		t.Fatalf("restore: %v, %v", rres, err)
	}
	if !slices.Equal(f.restored, []string{survival + " 01a0f000-0000-7000-8000-0000000b0001 local:root"}) {
		t.Fatalf("restored %q", f.restored)
	}
	// A deleted server's final backup restores onto another server.
	if _, err := s.RestoreBackup(rootCtx, &localv1.RestoreBackupRequest{Server: "survival", Backup: "0000000b0003"}); err != nil {
		t.Fatalf("restore a deleted server's backup: %v", err)
	}
	if got := f.restored[len(f.restored)-1]; got != survival+" 01a0f000-0000-7000-8000-0000000b0003 local:root" {
		t.Fatalf("restored %q", got)
	}

	// A failed job is an error when waiting, and not when not.
	f.fail = "disk full"
	if _, err := s.CreateBackup(rootCtx, &localv1.CreateBackupRequest{Server: "survival", Wait: true}); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("failed create: %v", err)
	}
	if _, err := s.RestoreBackup(rootCtx, &localv1.RestoreBackupRequest{Server: "survival", Backup: "0000000b0001", Wait: true}); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("failed restore: %v", err)
	}
	if _, err := s.RestoreBackup(rootCtx, &localv1.RestoreBackupRequest{Server: "survival", Backup: "0000000b0001"}); err != nil {
		t.Fatalf("queued restore: %v", err)
	}
}
