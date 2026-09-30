package files

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpload(t *testing.T) {
	e := newEnv(t)
	e.write("mods/old.jar", "old")
	data := bytes.Repeat([]byte("0123456789"), 1000)

	up, err := e.svc.StartUpload(ctx, srvID, "u", "/mods/old.jar", int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if up.Received != 0 || up.MaxChunk != MaxChunk {
		t.Fatalf("upload = %+v", up)
	}
	// The staging file is in the server's directory (it counts toward the
	// disk limit) but not listed.
	if !e.exists(staging(up.ID)) {
		t.Fatal("no staging file")
	}
	l, _ := e.svc.List(ctx, srvID, "/")
	for _, en := range l.Entries {
		if IsStaging(en.Name) {
			t.Error("staging file listed")
		}
	}

	if up, err = e.svc.WriteChunk(ctx, up.ID, 0, bytes.NewReader(data[:4000])); err != nil || up.Received != 4000 {
		t.Fatalf("chunk 1: %+v, %v", up, err)
	}
	// A chunk at the wrong offset says where to resume.
	up, err = e.svc.WriteChunk(ctx, up.ID, 1000, bytes.NewReader(data[1000:2000]))
	if !errors.Is(err, ErrOffset) || up.Received != 4000 {
		t.Fatalf("wrong offset: %+v, %v", up, err)
	}
	// More than what's left is refused, and nothing of it is kept.
	up, err = e.svc.WriteChunk(ctx, up.ID, 4000, bytes.NewReader(append(bytes.Clone(data[4000:]), 'x')))
	if !errors.Is(err, ErrChunkTooLarge) {
		t.Fatalf("oversized chunk: %v", err)
	}
	if st, _ := e.svc.UploadStatus(ctx, up.ID); st.Received != 4000 {
		t.Fatalf("after a refused chunk, received = %d", st.Received)
	}
	// Until the last chunk arrives, the old file is untouched.
	if e.read("mods/old.jar") != "old" {
		t.Error("the target changed before the upload finished")
	}
	up, err = e.svc.WriteChunk(ctx, up.ID, 4000, bytes.NewReader(data[4000:]))
	if err != nil || !up.Done {
		t.Fatalf("last chunk: %+v, %v", up, err)
	}
	if !bytes.Equal([]byte(e.read("mods/old.jar")), data) || e.exists(staging(up.ID)) {
		t.Error("the upload didn't replace the file")
	}
	if _, err := e.svc.UploadStatus(ctx, up.ID); !errors.Is(err, ErrTransferNotFound) {
		t.Errorf("a finished upload is still there: %v", err)
	}

	// Empty files and new directories.
	if up, err := e.svc.StartUpload(ctx, srvID, "u", "new/dir/empty", 0); err != nil || !up.Done || e.read("new/dir/empty") != "" {
		t.Errorf("empty upload: %+v, %v", up, err)
	}
}

func TestUploadLimits(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.StartUpload(ctx, srvID, "u", "big", MaxTransfer+1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("over the transfer cap: %v", err)
	}
	e.servers.space = 100
	if _, err := e.svc.StartUpload(ctx, srvID, "u", "f", 101); !errors.Is(err, ErrNoSpace) {
		t.Errorf("over the disk limit: %v", err)
	}
	if err := e.svc.Mkdir(ctx, srvID, "dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartUpload(ctx, srvID, "u", "dir", 1); !errors.Is(err, ErrNotRegular) {
		t.Errorf("over a directory: %v", err)
	}
	if _, err := e.svc.StartUpload(ctx, srvID, "u", "/", 1); !errors.Is(err, ErrRoot) {
		t.Errorf("to the directory itself: %v", err)
	}
}

// Uploads survive a Wings restart (a new Service on the same database),
// expire when idle, and can be cancelled.
func TestUploadResumeAndExpiry(t *testing.T) {
	e := newEnv(t)
	up, err := e.svc.StartUpload(ctx, srvID, "u", "f", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.WriteChunk(ctx, up.ID, 0, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	restarted := NewService(Options{Servers: e.servers, Store: e.db, UID: os.Getuid(), GID: os.Getgid(), Now: func() time.Time { return e.now }})
	st, err := restarted.UploadStatus(ctx, up.ID)
	if err != nil || st.Received != 5 {
		t.Fatalf("after a restart: %+v, %v", st, err)
	}
	if up, err = restarted.WriteChunk(ctx, up.ID, 5, strings.NewReader("world")); err != nil || !up.Done || e.read("f") != "helloworld" {
		t.Fatalf("resumed: %+v, %v", up, err)
	}

	idle, err := e.svc.StartUpload(ctx, srvID, "u", "g", 10)
	if err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(uploadIdle + time.Minute)
	if _, err := e.svc.WriteChunk(ctx, idle.ID, 0, strings.NewReader("x")); !errors.Is(err, ErrTransferNotFound) {
		t.Errorf("idle upload: %v", err)
	}
	if n, err := e.svc.Prune(ctx); err != nil || n != 1 || e.exists(staging(idle.ID)) {
		t.Errorf("prune = %d, %v; staging left: %v", n, err, e.exists(staging(idle.ID)))
	}

	c, err := e.svc.StartUpload(ctx, srvID, "u", "h", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelUpload(ctx, "another-server", c.ID); !errors.Is(err, ErrTransferNotFound) {
		t.Errorf("cancelled through another server: %v", err)
	}
	if err := e.svc.CancelUpload(ctx, srvID, c.ID); err != nil || e.exists(staging(c.ID)) {
		t.Errorf("cancel: %v", err)
	}
}

// An upload refuses to land on a denied name, even if the denylist changed
// (an egg update) after it started.
func TestUploadDeniedLater(t *testing.T) {
	e := newEnv(t)
	up, err := e.svc.StartUpload(ctx, srvID, "u", "server.jar", 1)
	if err != nil {
		t.Fatal(err)
	}
	e.servers.deny = []string{"server.jar"}
	if _, err := e.svc.WriteChunk(ctx, up.ID, 0, strings.NewReader("x")); !errors.Is(err, ErrDenied) {
		t.Errorf("finished onto a denied name: %v", err)
	}
	if e.exists("server.jar") {
		t.Error("created a denied file")
	}
}

func TestDownload(t *testing.T) {
	e := newEnv(t)
	data := strings.Repeat("abcdefghij", 100)
	e.write("world.zip", data)
	d, err := e.svc.StartDownload(ctx, srvID, "/world.zip")
	if err != nil || d.Size != int64(len(data)) {
		t.Fatalf("download = %+v, %v", d, err)
	}
	// In chunks, resuming from any offset.
	var got bytes.Buffer
	for off := int64(0); off < d.Size; off += 300 {
		if _, err := e.svc.ReadChunk(ctx, d.ID, off, 300, &got); err != nil {
			t.Fatal(err)
		}
	}
	if got.String() != data {
		t.Fatal("downloaded data differs")
	}
	if _, err := e.svc.ReadChunk(ctx, d.ID, 0, MaxChunk+1, &got); !errors.Is(err, ErrChunkTooLarge) {
		t.Errorf("oversized chunk: %v", err)
	}
	if _, err := e.svc.ReadChunk(ctx, d.ID, d.Size+1, 1, &got); err == nil {
		t.Error("read past the end")
	}
	// A file that changes can't be resumed into a mix of versions.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(e.dir, "world.zip"), later, later); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ReadChunk(ctx, d.ID, 0, 10, &got); !errors.Is(err, ErrChanged) {
		t.Errorf("changed file: %v", err)
	}
	e.now = e.now.Add(downloadIdle + time.Minute)
	if _, err := e.svc.ReadChunk(ctx, d.ID, 0, 10, &got); !errors.Is(err, ErrTransferNotFound) {
		t.Errorf("idle download: %v", err)
	}
	if _, err := e.svc.StartDownload(ctx, srvID, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
}
