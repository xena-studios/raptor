package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrash(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("a.txt", "first")
	e.write("d/x.txt", "12345")
	e.write("d/y.txt", "678")

	if err := e.svc.Trash(ctx, srvID, "u1", "/", []string{"a.txt", "d", "missing"}); err != nil {
		t.Fatal(err)
	}
	if e.exists("a.txt") || e.exists("d") {
		t.Fatal("still in the server's directory")
	}
	list, err := e.svc.ListTrash(ctx, srvID)
	if err != nil || len(list) != 2 {
		t.Fatalf("trash: %+v, %v", list, err)
	}
	byPath := map[string]TrashEntry{}
	for _, x := range list {
		byPath[x.Path] = x
	}
	if d := byPath["/d"]; !d.IsDir || d.Size != 8 || d.DeletedBy != "u1" || byPath["/a.txt"].Size != 5 {
		t.Errorf("entries: %+v", byPath)
	}
	// It's outside the server's directory, on the same filesystem.
	trash := filepath.Join(filepath.Dir(e.dir), ".trash", srvID, byPath["/a.txt"].ID, "a.txt")
	if b, err := os.ReadFile(trash); err != nil || string(b) != "first" {
		t.Fatalf("in the trash: %q, %v", b, err)
	}

	// Restoring puts it back; something new in the way keeps its place.
	e.write("a.txt", "second")
	got, err := e.svc.Restore(ctx, srvID, []string{byPath["/a.txt"].ID, byPath["/d"].ID})
	if err != nil || len(got) != 2 || got[0].Path != "/a (restored).txt" || got[1].Path != "/d" {
		t.Fatalf("restore: %+v, %v", got, err)
	}
	if e.read("a.txt") != "second" || e.read("a (restored).txt") != "first" || e.read("d/x.txt") != "12345" {
		t.Error("restored files")
	}
	if l, _ := e.svc.ListTrash(ctx, srvID); len(l) != 0 {
		t.Errorf("still listed: %+v", l)
	}
	if _, err := e.svc.Restore(ctx, srvID, []string{byPath["/a.txt"].ID}); !errors.Is(err, ErrTrashNotFound) {
		t.Errorf("restoring twice: %v", err)
	}

	// A link is trashed as a link: what it points at stays.
	if err := os.WriteFile(filepath.Join(e.outside, "secret"), []byte("not yours"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", filepath.Join(e.dir, "evil")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Trash(ctx, srvID, "u1", "/", []string{"evil/secret"}); err == nil {
		t.Error("trashed a file through a link out of the directory")
	}
	if err := e.svc.Trash(ctx, srvID, "u1", "/", []string{"evil"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.outside, "secret")); err != nil {
		t.Errorf("the link's target was moved: %v", err)
	}
	if err := e.svc.Trash(ctx, srvID, "u1", "/", []string{"."}); err == nil {
		t.Error("trashed the server's directory")
	}

	// A directory holding a denied file can't be trashed, like deleting it.
	e.servers.deny = []string{"config/secret.yml"}
	e.write("config/secret.yml", "x")
	if err := e.svc.Trash(ctx, srvID, "u1", "/", []string{"config"}); !errors.Is(err, ErrDenied) {
		t.Errorf("a denied file: %v", err)
	}
	e.servers.deny = nil

	// Deleting for good, emptying, and old entries going by themselves.
	e.write("b.txt", "b")
	e.write("c.txt", "c")
	_ = e.svc.Trash(ctx, srvID, "u1", "/", []string{"b.txt", "c.txt"})
	l, _ := e.svc.ListTrash(ctx, srvID)
	if len(l) != 3 {
		t.Fatalf("trash: %+v", l)
	}
	if err := e.svc.DeleteTrash(ctx, srvID, []string{l[0].ID, "../../etc"}); err != nil {
		t.Fatal(err)
	}
	if l2, _ := e.svc.ListTrash(ctx, srvID); len(l2) != 2 {
		t.Errorf("after deleting one: %+v", l2)
	}
	e.now = e.now.Add(TrashKeep + time.Hour)
	if n, err := e.svc.PruneTrash(ctx); err != nil || n != 2 {
		t.Errorf("prune: %d, %v", n, err)
	}
	e.write("z.txt", "z")
	_ = e.svc.Trash(ctx, srvID, "u1", "/", []string{"z.txt"})
	if err := e.svc.EmptyTrash(ctx, srvID); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(filepath.Dir(e.dir), ".trash", srvID))
	if l, _ := e.svc.ListTrash(ctx, srvID); len(l) != 0 || len(entries) != 0 {
		t.Errorf("after emptying: %+v, %d on disk", l, len(entries))
	}
}
