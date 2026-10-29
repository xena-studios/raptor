package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const instID = "0199a000-0000-7000-8000-0000000000d1"

// instances is a fake databases service with one instance and its dumps.
type instances struct {
	mu      sync.Mutex
	content map[string]string // db_name → dump
	fail    error
	ids     []string
}

func (f *instances) DumpInstance(_ context.Context, _ string, _ io.Writer) (string, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return "", nil, f.fail
	}
	dir, err := os.MkdirTemp("", "instance-")
	if err != nil {
		return "", nil, err
	}
	var names []string
	for name, c := range f.content {
		if err := os.WriteFile(filepath.Join(dir, name+".sql"), []byte(c), 0o600); err != nil {
			return "", nil, err
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return dir, names, nil
}

func (f *instances) InstanceIDs(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ids), nil
}

func (f *instances) InstanceName(context.Context, string) (string, string) { return "main", "mariadb" }

func instanceEnv(t *testing.T) (*env, *instances) {
	t.Helper()
	v := newEnv(t)
	if err := v.db.Write.InsertInstance(context.Background(), store.InsertInstanceParams{
		ID: instID, Name: "main", Engine: "mariadb", Version: "11.8", Image: "x", RootPassword: []byte("x"),
		Networks: "[]", Limits: "{}", Settings: "{}", DesiredState: "running",
	}); err != nil {
		t.Fatal(err)
	}
	f := &instances{content: map[string]string{"app": "CREATE TABLE a (id int);"}, ids: []string{instID}}
	v.m.o.Instances = f
	return v, f
}

func (v *env) instanceBackup() *Backup {
	v.t.Helper()
	b, err := v.m.BackupInstance(context.Background(), instID, KindManual, "u1")
	if err != nil {
		v.t.Fatal(err)
	}
	if j := v.wait(b.JobID); j.Status != jobs.Succeeded {
		log, _ := v.jobs.Log(j.ID)
		v.t.Fatalf("instance backup job %s: %s\n%s", j.Status, j.Error, log)
	}
	got, err := v.m.InstanceBackup(context.Background(), instID, b.ID)
	if err != nil {
		v.t.Fatal(err)
	}
	return got
}

// An instance's backup holds a dump of each database, comes out one
// database at a time, is kept apart from servers' backups, and follows the
// instance's retention.
func TestInstanceBackups(t *testing.T) {
	ctx := context.Background()
	v, f := instanceEnv(t)

	b := v.instanceBackup()
	if b.Status != StatusOK || b.Subject != SubjectInstance || !slices.Equal(b.Databases, []string{"app"}) || b.DestinationID != LocalDestination {
		t.Fatalf("backup = %+v", b)
	}

	// Its dump comes back out.
	tmp := t.TempDir()
	p, err := v.m.ExtractInstanceDatabase(ctx, instID, b.ID, "app", "app.sql", tmp, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != "CREATE TABLE a (id int);" {
		t.Fatalf("extracted %q", got)
	}
	if _, err := v.m.ExtractInstanceDatabase(ctx, instID, b.ID, "other", "other.sql", tmp, io.Discard); !errors.Is(err, ErrNotFound) {
		t.Errorf("a database it doesn't hold: %v", err)
	}

	// Never a server's: not listed, restored, or found for one.
	if all, _ := v.m.List(ctx, ""); slices.ContainsFunc(all, func(x *Backup) bool { return x.ID == b.ID }) {
		t.Error("an instance backup is listed with servers'")
	}
	if _, err := v.m.Restorable(ctx, srvID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("restorable onto a server: %v", err)
	}
	if _, err := v.m.Get(ctx, instID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("found as a server's: %v", err)
	}
	if orphans, _ := v.m.Orphans(ctx); len(orphans) != 0 {
		t.Errorf("orphans = %+v", orphans)
	}

	// Its events are the database ones, naming the instance.
	all, _ := v.events.Since(ctx, 0, 1000)
	var finished *events.Event
	for i := range all {
		if all[i].Type == "db.backup.finished" {
			finished = &all[i]
		}
		if all[i].Type == EventFinished {
			t.Errorf("a server backup event for an instance's: %+v", all[i])
		}
	}
	if finished == nil || finished.ServerID != "" || finished.Data["instance_id"] != instID || finished.Data["instance"] != "main" {
		t.Fatalf("finished event = %+v", finished)
	}

	// Retention keeps the last keep_last.
	if err := v.m.SetInstancePolicy(ctx, instID, InstancePolicy{Enabled: true, At: "04:00", KeepLast: 2}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		v.clock.add(time.Hour)
		v.instanceBackup()
	}
	if l, _ := v.m.InstanceBackups(ctx, instID); len(l) != 2 {
		t.Fatalf("after retention: %d backups", len(l))
	}

	// A dump that fails fails the backup.
	f.mu.Lock()
	f.fail = errors.New("the instance isn't running")
	f.mu.Unlock()
	bad, err := v.m.BackupInstance(ctx, instID, KindManual, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if j := v.wait(bad.JobID); j.Status == jobs.Succeeded {
		t.Fatal("a backup whose dump failed succeeded")
	}
	if got, _ := v.m.InstanceBackup(ctx, instID, bad.ID); got.Status != StatusFailed {
		t.Errorf("failed backup = %+v", got)
	}
	f.mu.Lock()
	f.fail = nil
	f.mu.Unlock()

	// Deleting one.
	l, _ := v.m.InstanceBackups(ctx, instID)
	var ok *Backup
	for _, x := range l {
		if x.Status == StatusOK {
			ok = x
			break
		}
	}
	jobID, err := v.m.DeleteInstanceBackup(ctx, instID, ok.ID)
	if err != nil {
		t.Fatal(err)
	}
	v.wait(jobID)
	if _, err := v.m.InstanceBackup(ctx, instID, ok.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted backup still there: %v", err)
	}
	if _, err := v.m.DeleteInstanceBackup(ctx, "another", bad.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another instance's backup: %v", err)
	}
}

// A job's safety and final backups, and what a deleted instance keeps.
func TestInstanceJobBackups(t *testing.T) {
	ctx := context.Background()
	v, _ := instanceEnv(t)
	v.instanceBackup()
	final, err := v.m.InstanceJobBackup(ctx, instID, "job-1", KindFinal, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// Resumed: the same backup.
	if again, err := v.m.InstanceJobBackup(ctx, instID, "job-1", KindFinal, io.Discard); err != nil || again != final {
		t.Fatalf("resumed: %s, %v", again, err)
	}
	b, _ := v.m.InstanceBackup(ctx, instID, final)
	if b.Kind != KindFinal || b.ExpiresAt.IsZero() {
		t.Errorf("a local final backup = %+v", b)
	}
	v.m.InstanceDeleted(ctx, instID)
	deadline := time.Now().Add(10 * time.Second)
	for {
		l, _ := v.m.InstanceBackups(ctx, instID)
		if len(l) == 1 && l[0].ID == final {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the instance was deleted: %+v", l)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The daily backup: first seen, an instance gets the default settings
// and no backup yet; once its time has come, one, and not again that day.
func TestInstanceSchedule(t *testing.T) {
	ctx := context.Background()
	v, _ := instanceEnv(t)
	v.clock.t = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	v.m.scheduleInstances(ctx)
	pol, err := v.m.InstancePolicy(ctx, instID)
	if err != nil || pol != (InstancePolicy{Enabled: true, At: DefaultInstanceAt, KeepLast: DefaultInstanceKeep, DestinationID: LocalDestination}) {
		t.Fatalf("default settings = %+v, %v", pol, err)
	}
	if l, _ := v.m.InstanceBackups(ctx, instID); len(l) != 0 {
		t.Fatalf("a backup on first sight: %+v", l)
	}
	v.clock.add(17 * time.Hour) // 03:00 the next day
	v.m.scheduleInstances(ctx)
	if l, _ := v.m.InstanceBackups(ctx, instID); len(l) != 0 {
		t.Fatal("a backup before its time")
	}
	v.clock.add(90 * time.Minute) // 04:30
	v.m.scheduleInstances(ctx)
	v.m.scheduleInstances(ctx)
	l, _ := v.m.InstanceBackups(ctx, instID)
	if len(l) != 1 || l[0].Kind != KindScheduled {
		t.Fatalf("at its time: %+v", l)
	}
	v.wait(l[0].JobID)

	// Turned off: none.
	pol.Enabled = false
	if err := v.m.SetInstancePolicy(ctx, instID, pol); err != nil {
		t.Fatal(err)
	}
	v.clock.add(24 * time.Hour)
	v.m.scheduleInstances(ctx)
	if l, _ := v.m.InstanceBackups(ctx, instID); len(l) != 1 {
		t.Fatalf("while off: %d backups", len(l))
	}
}

func TestInstancePolicy(t *testing.T) {
	ctx := context.Background()
	v, _ := instanceEnv(t)
	for _, p := range []InstancePolicy{
		{At: "4am", KeepLast: 7},
		{At: "25:00", KeepLast: 7},
		{At: "04:00", KeepLast: 0},
		{At: "04:00", KeepLast: 101},
	} {
		if err := v.m.SetInstancePolicy(ctx, instID, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", p, err)
		}
	}
	if err := v.m.SetInstancePolicy(ctx, instID, InstancePolicy{At: "04:00", KeepLast: 7, DestinationID: "nope"}); !errors.Is(err, ErrDestination) {
		t.Errorf("an unknown destination: %v", err)
	}
	if err := v.m.SetInstancePolicy(ctx, "not-an-instance", InstancePolicy{At: "04:00", KeepLast: 7}); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unknown instance: %v", err)
	}
	cur := InstancePolicy{At: "04:00", KeepLast: 7, DestinationID: LocalDestination}
	if !(InstancePolicy{At: "04:00", KeepLast: 3, DestinationID: LocalDestination}).KeepsLess(cur) {
		t.Error("keeping fewer isn't keeping less")
	}
	if (InstancePolicy{At: "04:00", KeepLast: 9, DestinationID: LocalDestination}).KeepsLess(cur) {
		t.Error("keeping more is keeping less")
	}
	// The default goes offsite once the node backs up anywhere offsite.
	s3, err := v.m.SaveDestination(ctx, Destination{Name: "B2", Type: engine.S3, Config: engine.Config{S3: &engine.S3Config{
		Endpoint: "s3.example.com", Bucket: "b", AccessKey: "k", SecretKey: "secret",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := v.m.defaultInstancePolicy(ctx); err != nil || p.DestinationID != s3 {
		t.Errorf("default with an offsite destination = %+v, %v", p, err)
	}
	// A destination an instance uses can't be deleted.
	if err := v.m.SetInstancePolicy(ctx, instID, InstancePolicy{At: "04:00", KeepLast: 7, DestinationID: s3}); err != nil {
		t.Fatal(err)
	}
	if err := v.m.DeleteDestination(ctx, s3); !errors.Is(err, ErrInUse) {
		t.Errorf("deleting an instance's destination: %v", err)
	}
	if err := v.m.SetInstancePolicy(ctx, instID, cur); err != nil {
		t.Fatal(err)
	}
	if err := v.m.DeleteDestination(ctx, s3); err != nil {
		t.Errorf("deleting it once unused: %v", err)
	}
}

func TestDue(t *testing.T) {
	loc := time.UTC
	at := func(h, m int) time.Time { return time.Date(2026, 3, 2, h, m, 0, 0, loc) }
	for _, c := range []struct {
		now  time.Time
		last time.Time
		want bool
	}{
		{at(3, 59), time.Time{}, false},
		{at(4, 0), time.Time{}, true},
		{at(9, 0), at(3, 0), true},  // missed while off: made up once
		{at(9, 0), at(4, 1), false}, // already ran today
	} {
		var last int64
		if !c.last.IsZero() {
			last = c.last.UnixMilli()
		}
		if got := due("04:00", last, c.now, loc); got != c.want {
			t.Errorf("due at %s, last %s = %v", c.now, c.last, got)
		}
	}
	if due("nope", 0, at(5, 0), loc) {
		t.Error("a bad time is due")
	}
}
