package backup

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/jobs"
)

// Node A backs up to a NAS folder and dies; node B finds those backups with
// A's key and restores one, without being able to change them.
func TestRecoverAnotherNodesBackups(t *testing.T) {
	ctx := context.Background()
	a := newEnv(t)
	a.write("world/level.dat", "node A's world")
	nasPath := filepath.Join(a.dir, "nas")
	nas, err := a.m.SaveDestination(ctx, Destination{Name: "NAS", Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: nasPath}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.m.SetPolicy(ctx, srvID, Policy{Targets: []Target{{DestinationID: nas, Retention: DefaultRetention}}}); err != nil {
		t.Fatal(err)
	}
	a.backup()
	aKey, _ := a.m.ShowKey(ctx)

	b := newEnv(t)
	b.m.o.FolderRoots = []string{b.dir, a.dir}
	b.write("world/level.dat", "node B's own world")
	if bKey, _ := b.m.ShowKey(ctx); bKey.Key == aKey.Key {
		t.Fatal("two nodes share a key")
	}
	from := func(key string) RecoverParams {
		return RecoverParams{Destination: &Destination{
			Name: "A's NAS", Type: engine.Folder, RepoPassword: key,
			Config: engine.Config{Folder: &engine.FolderConfig{Path: nasPath}},
		}}
	}

	// The wrong key finds nothing, and says so.
	_, job, err := b.m.Recover(ctx, from("not-the-key"), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if j := b.wait(job); j.Status != jobs.Failed {
		t.Fatalf("wrong key: %s", j.Status)
	}
	// Nowhere: no backups there, and none made.
	_, job, err = b.m.Recover(ctx, RecoverParams{Destination: &Destination{
		Name: "empty", Type: engine.Folder, RepoPassword: aKey.Key,
		Config: engine.Config{Folder: &engine.FolderConfig{Path: filepath.Join(b.dir, "empty")}},
	}}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if j := b.wait(job); j.Status != jobs.Failed {
		t.Fatalf("empty folder: %s", j.Status)
	}

	dest, job, err := b.m.Recover(ctx, from(aKey.Key), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if j := b.wait(job); j.Status != jobs.Succeeded {
		log, _ := b.jobs.Log(j.ID)
		t.Fatalf("recover: %s %s\n%s", j.Status, j.Error, log)
	}
	var found *Backup
	for _, x := range b.list() {
		if x.DestinationID == dest {
			found = x
		}
	}
	if found == nil || found.Kind != KindRecovered || found.Status != StatusOK || found.Size == 0 {
		t.Fatalf("recovered: %+v", found)
	}
	// Scanning the same destination again adds nothing new.
	if _, err := b.m.scanJob(ctx, jobs.Job{Payload: []byte(`{"destination_id": "` + dest + `"}`)}, discard{}); err != nil {
		t.Fatal(err)
	}
	if n := countKind(b, KindRecovered); n != 1 {
		t.Fatalf("%d recovered backups after scanning twice", n)
	}

	// Read-only: no backups to it, no deleting, no changing it.
	if err := b.m.SetPolicy(ctx, srvID, Policy{Targets: []Target{{DestinationID: dest, Retention: DefaultRetention}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("backing up to a recovered destination: %v", err)
	}
	if _, err := b.m.Delete(ctx, srvID, found.ID); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("deleting a recovered backup: %v", err)
	}
	if _, err := b.m.SaveDestination(ctx, Destination{ID: dest, Name: "x", Type: engine.Folder, Config: engine.Config{Folder: &engine.FolderConfig{Path: nasPath}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("changing a recovered destination: %v", err)
	}
	if _, err := b.m.SaveDestination(ctx, Destination{Name: "sneaky", Type: engine.Folder, ReadOnly: true, Config: engine.Config{Folder: &engine.FolderConfig{Path: nasPath}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("saving a read-only destination directly: %v", err)
	}

	// Restored onto B's server: A's world, with a safety backup of B's own
	// files on B's own destination.
	restoreJob, err := b.m.Restore(ctx, srvID, found.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if j := b.wait(restoreJob); j.Status != jobs.Succeeded {
		log, _ := b.jobs.Log(j.ID)
		t.Fatalf("restore: %s %s\n%s", j.Status, j.Error, log)
	}
	if got := b.read("world/level.dat"); got != "node A's world" {
		t.Fatalf("restored %q", got)
	}
	for _, x := range b.list() {
		if x.Kind == KindSafety && x.DestinationID != LocalDestination {
			t.Fatalf("safety backup went to %s", x.DestinationID)
		}
	}
	// A's folder is as A left it: B's restore wrote nothing there.
	if got := countKind(a, KindManual); got != 1 {
		t.Fatalf("node A's backups: %d", got)
	}

	// Looking back in time needs storage that keeps old versions.
	at := time.Now().Add(-time.Hour)
	if _, _, err := b.m.Recover(ctx, RecoverParams{FromDestinationID: LocalDestination, PointInTime: &at}, "owner"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("point in time on a folder: %v", err)
	}
}

func countKind(v *env, kind string) int {
	n := 0
	for _, x := range v.list() {
		if x.Kind == kind {
			n++
		}
	}
	return n
}
