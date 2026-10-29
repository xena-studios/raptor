//go:build e2e

package databases

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeInstanceBackups hands back an instance's dumps from a folder, and
// records the backups jobs took.
type fakeInstanceBackups struct {
	fakeBackups
	mu    sync.Mutex
	kinds []string
	gone  []string
}

func (f *fakeInstanceBackups) InstanceJobBackup(_ context.Context, _, _, kind string, _ io.Writer) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kinds = append(f.kinds, kind)
	return kind + "-backup", nil
}

func (f *fakeInstanceBackups) ExtractInstanceDatabase(_ context.Context, _, _, _, file, dir string, _ io.Writer) (string, error) {
	b, err := os.ReadFile(filepath.Join(f.dumps, file))
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "dump", file)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, b, 0o600)
}

func (f *fakeInstanceBackups) InstanceDeleted(_ context.Context, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gone = append(f.gone, id)
}

// TestInstanceBackupDumps dumps a whole instance as its own backup does,
// restores one database from it after a safety backup, and deletes the
// instance after a final one.
func TestInstanceBackupDumps(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E instance backups")
	for _, eng := range []string{MariaDB, Postgres} {
		t.Run(eng, func(t *testing.T) {
			in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E instance backups " + eng, Engine: eng, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 512}})
			if err != nil {
				t.Fatal(err)
			}
			deleted := false
			t.Cleanup(func() {
				if !deleted {
					_ = e.svc.Delete(context.Background(), in.ID)
				}
			})
			waitRunning(t, e.svc, in.ID)
			d, _, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "perms")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "CREATE TABLE ranks (id INT PRIMARY KEY, name VARCHAR(40));\n" +
				"INSERT INTO ranks VALUES (1, 'admin'), (2, 'mod'), (3, 'member')"}, false); err != nil {
				t.Fatal(err)
			}

			ids, err := e.svc.InstanceIDs(ctx)
			if err != nil || !slices.Contains(ids, in.ID) {
				t.Fatalf("instances: %v %v", ids, err)
			}
			if name, engine := e.svc.InstanceName(ctx, in.ID); name != in.Name || engine != eng {
				t.Errorf("name: %q %q", name, engine)
			}
			dir, names, err := e.svc.DumpInstance(ctx, in.ID, io.Discard)
			if err != nil || !slices.Equal(names, []string{d.Name}) {
				t.Fatalf("dump: %q %v %v", dir, names, err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			dump, _ := os.ReadFile(filepath.Join(dir, d.Name+".sql"))
			if !strings.Contains(string(dump), "admin") {
				t.Fatalf("the dump has no rows:\n%s", dump)
			}

			// One database from the instance's backup, after a safety backup.
			if _, err := e.svc.Query(ctx, e.server, d.ID, QueryInput{SQL: "DELETE FROM ranks WHERE id > 1"}, false); err != nil {
				t.Fatal(err)
			}
			fb := &fakeInstanceBackups{fakeBackups: fakeBackups{dumps: dir}}
			e.svc.SetBackups(fb)
			jobID, err := e.svc.RestoreFromInstanceBackup(ctx, in.ID, d.ID, "b1")
			if err != nil {
				t.Fatal(err)
			}
			if j := e.wait(t, jobID); j.Status != "succeeded" {
				t.Fatalf("restore: %s", j.Error)
			}
			if n := e.count(t, d, "ranks"); n != "3" {
				t.Errorf("after the restore, %s ranks", n)
			}
			if _, err := e.svc.RestoreFromInstanceBackup(ctx, "another", d.ID, "b1"); err == nil {
				t.Error("restored a database through another instance")
			}

			// Deleting it: a final backup first, as a job.
			jobID, err = e.svc.DeleteInstance(ctx, in.ID, false)
			if err != nil || jobID == "" {
				t.Fatalf("delete: %q %v", jobID, err)
			}
			if j := e.wait(t, jobID); j.Status != "succeeded" {
				t.Fatalf("delete job: %s", j.Error)
			}
			deleted = true
			fb.mu.Lock()
			kinds, gone := slices.Clone(fb.kinds), slices.Clone(fb.gone)
			fb.mu.Unlock()
			if !slices.Equal(kinds, []string{"safety", "final"}) || !slices.Equal(gone, []string{in.ID}) {
				t.Errorf("backups taken %v, instances forgotten %v", kinds, gone)
			}
			if _, err := e.svc.Get(ctx, in.ID); err == nil {
				t.Error("the instance is still there")
			}
		})
	}
}
