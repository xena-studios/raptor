package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A backup can be browsed folder by folder, and some of it restored into a
// folder of its own; the egg's denylist applies to both.
func TestBrowseAndExtract(t *testing.T) {
	ctx := context.Background()
	e := newEngine(t)
	src := t.TempDir()
	write(t, filepath.Join(src, "server.properties"), "motd=old", 0o644)
	write(t, filepath.Join(src, "config/a.yml"), "a: 1", 0o644)
	write(t, filepath.Join(src, "config/b.yml"), "b: 2", 0o644)
	write(t, filepath.Join(src, "world/level.dat"), "level", 0o644)
	write(t, filepath.Join(src, "world/region/r.0.0.mca"), "region", 0o644)
	write(t, filepath.Join(src, "world/server.jar"), "jar", 0o644)
	res, err := e.Snapshot(ctx, SnapshotRequest{ServerID: "s1", BackupID: "b1", Dir: src}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deny := []string{"*.jar"}

	list, err := e.Browse(ctx, BrowseRequest{SnapshotID: res.SnapshotID, Path: "world", Deny: deny})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]BrowseEntry{}
	for _, b := range list {
		got[b.Name] = b
	}
	if got["level.dat"].Type != "file" || got["level.dat"].Size != 5 || got["region"].Type != "dir" ||
		!got["server.jar"].Denied || got["level.dat"].Denied || len(got) != 3 {
		t.Errorf("browse world: %+v", list)
	}
	for _, bad := range []string{"../etc", "/../x", "nope"} {
		if _, err := e.Browse(ctx, BrowseRequest{SnapshotID: res.SnapshotID, Path: bad}); !errors.Is(err, ErrBadPath) {
			t.Errorf("browse %q: %v", bad, err)
		}
	}

	// The server's files have moved on since; pull two things back out.
	write(t, filepath.Join(src, "config/a.yml"), "a: changed", 0o644)
	if _, err := e.Extract(ctx, ExtractRequest{
		SnapshotID: res.SnapshotID, Dir: src, Into: ".restore/b1", Paths: []string{"config/a.yml", "world"},
		Deny: deny, UID: -1, GID: -1,
	}, nil); err != nil {
		t.Fatal(err)
	}
	out := tree(t, filepath.Join(src, ".restore/b1"))
	for _, want := range []string{"config/a.yml", "world/level.dat", "world/region/r.0.0.mca"} {
		if out[want] == "" {
			t.Errorf("%s wasn't restored: %v", want, out)
		}
	}
	for _, not := range []string{"config/b.yml", "server.properties", "world/server.jar"} {
		if out[not] != "" {
			t.Errorf("%s was restored: %v", not, out)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(src, ".restore/b1/config/a.yml")); string(b) != "a: 1" {
		t.Errorf("restored a.yml = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "config/a.yml")); string(b) != "a: changed" {
		t.Errorf("the live a.yml was touched: %q", b)
	}

	// Never over an existing folder, never a denied file, never too much.
	for what, req := range map[string]ExtractRequest{
		"existing folder": {Into: ".restore/b1", Paths: []string{"config"}},
		"denied":          {Into: ".restore/b2", Paths: []string{"world/server.jar"}, Deny: deny},
		"escape":          {Into: ".restore/b3", Paths: []string{"../x"}},
		"everything":      {Into: ".restore/b4", Paths: []string{""}},
		"too large":       {Into: ".restore/b5", Paths: []string{"world"}, MaxSize: 3},
	} {
		req.SnapshotID, req.Dir, req.UID, req.GID = res.SnapshotID, src, -1, -1
		if _, err := e.Extract(ctx, req, nil); err == nil {
			t.Errorf("%s: no error", what)
		}
	}
}
