package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const filesNode = "node-files"

const filesServer = "0199a0b1-7c2e-7a41-9f3e-2b1c4d5e6f70"

type fileServers struct{ dir string }

func (s fileServers) FilesDir(id string) (string, error) {
	if id != filesServer {
		return "", errors.New("server not found")
	}
	return s.dir, nil
}
func (fileServers) CheckFiles(context.Context, string, bool) error   { return nil }
func (fileServers) Denylist(string) ([]string, error)                { return []string{"server.jar"}, nil }
func (fileServers) SpaceLeft(context.Context, string) (int64, error) { return -1, nil }

// The file actions through the executor, as the Panel sends them.
func TestFileCommands(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "server")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: filesServer, Name: "smp", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	eng := jobs.New(jobs.Options{Store: db, LogDir: filepath.Join(root, "jobs"), Poll: 10 * time.Millisecond})
	svc := files.NewService(files.Options{Servers: fileServers{dir}, Store: db, Jobs: eng, UID: os.Getuid(), GID: os.Getgid()})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	x := &command.Executor{DB: db, NodeID: filesNode, PanelKey: pub}
	RegisterFiles(x, svc)

	send := func(action string, params any) (json.RawMessage, error) {
		t.Helper()
		id, _ := uuid.NewV7()
		raw, _ := json.Marshal(params)
		e := command.Envelope{CommandID: id.String(), NodeID: filesNode, UserID: "alice", Action: action, ServerID: filesServer, Params: raw, ExpiresAt: time.Now().Add(time.Minute).Unix()}
		e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, ExpiresAt: e.ExpiresAt}
		p, err := e.Grant.Payload()
		if err != nil {
			t.Fatal(err)
		}
		e.Grant.Signature = ed25519.Sign(key, p)
		res, err := x.Execute(ctx, e)
		return res.Value, err
	}

	if _, err := send(FilesWrite, FilesParams{Path: "config/a.yml", Data: []byte("a: 1\n")}); err != nil {
		t.Fatal(err)
	}
	raw, err := send(FilesRead, FilesParams{Path: "config/a.yml"})
	if err != nil {
		t.Fatal(err)
	}
	var c files.Content
	if err := json.Unmarshal(raw, &c); err != nil || string(c.Data) != "a: 1\n" {
		t.Fatalf("read = %s, %v", raw, err)
	}
	raw, err = send(FilesList, FilesParams{Path: "/"})
	if err != nil {
		t.Fatal(err)
	}
	var l files.Listing
	if err := json.Unmarshal(raw, &l); err != nil || len(l.Entries) != 2 || !l.Entries[1].Denied {
		t.Fatalf("list = %s, %v", raw, err)
	}
	if _, err := send(FilesRead, FilesParams{Path: "server.jar"}); !errors.Is(err, files.ErrDenied) {
		t.Errorf("reading a denied file: %v", err)
	}
	if _, err := send(FilesRename, FilesParams{Dir: "config", Moves: []files.Move{{From: "a.yml", To: "b.yml"}}}); err != nil {
		t.Fatal(err)
	}
	raw, err = send(FilesCompress, FilesParams{Dir: "/", Names: []string{"config"}})
	if err != nil {
		t.Fatal(err)
	}
	var job struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(raw, &job)
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if j, err := eng.Wait(wctx, job.JobID); err != nil || j.Status != jobs.Succeeded {
		t.Fatalf("compress job: %+v, %v", j, err)
	}
	raw, err = send(FilesUpload, FilesParams{Path: "up.txt", Size: 3})
	if err != nil {
		t.Fatal(err)
	}
	var up files.Upload
	_ = json.Unmarshal(raw, &up)
	if _, err := svc.WriteChunk(ctx, up.ID, 0, strings.NewReader("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := send(FilesDelete, FilesParams{Dir: "/", Names: []string{"config", "up.txt"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config wasn't deleted: %v", err)
	}
	if _, err := send(FilesList, FilesParams{Path: "config"}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("listing a deleted directory: %v", err)
	}
}
