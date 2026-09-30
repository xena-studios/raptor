//go:build e2e

// Backups through the command path against a real server: the default
// schedule, the egg's hooks through the real console, a signed restore,
// the worker process in its own low-priority scope, and an S3 destination
// (MinIO in Docker).
//
//	task e2e:runtime RUN=TestBackup
package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/backup/engine"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/schedule"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// The test binary doubles as the backup worker, like `raptor wings
// backup-worker`, and records where it ran.
func TestMain(m *testing.M) {
	if os.Getenv("RAPTOR_E2E_WORKER") == "1" {
		backup.PrepareWorker()
		if f := os.Getenv("RAPTOR_E2E_WORKER_INFO"); f != "" {
			cg, _ := os.ReadFile("/proc/self/cgroup")
			oom, _ := os.ReadFile("/proc/self/oom_score_adj")
			_ = os.WriteFile(f, []byte(strings.TrimSpace(string(cg))+" oom="+strings.TrimSpace(string(oom))), 0o600)
		}
		if err := backup.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

const hookEgg = `{
	"meta": {"version": "PTDL_v2"}, "name": "Shell",
	"docker_images": {"BusyBox": "busybox:1"}, "startup": "sh",
	"config": {"files": "{}", "startup": "{\"done\": \"READY\"}", "stop": "exit"},
	"scripts": {"installation": {"script": "echo installed", "container": "busybox:1", "entrypoint": "sh"}},
	"variables": [],
	"x-raptor": {"backup": {"pre": ["echo SAVE-OFF", "printf 'Saved%sthe game\\n' ' '"], "post": ["echo SAVE-ON"], "wait_for": "Saved the game"}}
}`

// backupEnv is a server manager with backups, schedules, and the command
// path, as Wings wires them.
type backupEnv struct {
	m     *server.Manager
	db    *store.DB
	bk    *backup.Manager
	sched *schedule.Scheduler
	eng   *jobs.Engine
	p     *panel
	owner *commandtest.Authenticator
	info  string // where the worker records its cgroup and OOM score
}

func newBackupEnv(t *testing.T) *backupEnv {
	t.Helper()
	ctx := context.Background()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	v := &backupEnv{info: filepath.Join(t.TempDir(), "worker")}
	dir := t.TempDir()
	v.m, v.db = newManagerWith(t, func(m *server.Manager, db *store.DB, e *jobs.Engine) {
		v.eng = e
		v.bk = backup.New(backup.Options{
			Store: db, Jobs: e, Events: events.New(db), Servers: m,
			Runner: backup.Process{
				Command: []string{exe}, Slice: "raptor-backup.slice", MemoryMax: 1 << 30,
				Env: []string{"RAPTOR_E2E_WORKER=1", "RAPTOR_E2E_WORKER_INFO=" + v.info},
			},
			LocalPath: filepath.Join(dir, "backups"), StateDir: filepath.Join(dir, "kopia"),
		})
		v.sched = schedule.New(schedule.Options{Store: db, Jobs: e, Events: events.New(db), Servers: m, Backups: v.bk})
	}, func(o *server.Options) {
		o.Deleted = func(ctx context.Context, id string) { v.bk.ServerDeleted(ctx, id) }
		o.JobBackup = func(ctx context.Context, id, jobID, kind string, log io.Writer) (string, error) {
			return v.bk.JobBackup(ctx, id, jobID, kind, log)
		}
	})
	if err := v.bk.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.bk.Close)
	v.sched.Start()
	t.Cleanup(v.sched.Close)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	x := &command.Executor{DB: v.db, NodeID: nodeID, RP: rp, PanelKey: pub}
	Register(x, v.m, Defaults{BackupSchedule: func(ctx context.Context, q *store.Queries, id string) error {
		_, err := v.sched.CreateTx(ctx, q, id, schedule.DefaultBackup("Europe/Berlin"))
		return err
	}})
	RegisterSchedules(x, v.sched)
	RegisterBackups(x, v.bk)
	v.p = &panel{t: t, x: x, key: priv}
	if v.owner, err = commandtest.New("ES256", rp.Origin, rp.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := command.AddKey(ctx, v.db, command.KeyParams{CredentialID: v.owner.CredentialID, UserID: "alice", PublicKey: v.owner.COSE, Role: "owner"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	return v
}

// waitJob waits for the job a command queued, which must succeed.
func (v *backupEnv) waitJob(t *testing.T, res command.Result) jobs.Job {
	t.Helper()
	j := v.job(t, res)
	if j.Status != jobs.Succeeded {
		log, _ := v.eng.Log(j.ID)
		t.Fatalf("job %s %s: %s\n%s", j.Type, j.Status, j.Error, log)
	}
	return j
}

// job waits for the job a command queued, however it ends.
func (v *backupEnv) job(t *testing.T, res command.Result) jobs.Job {
	t.Helper()
	var r struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res.Value, &r)
	wctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	j, err := v.eng.Wait(wctx, r.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// create makes a server with the owner's signature and waits for its
// install.
func (v *backupEnv) create(t *testing.T, name string) string {
	t.Helper()
	res, err := v.p.send("alice", ServerCreate, "", CreateParams{ServerConfig: ServerConfig{
		Name: name, Egg: []byte(hookEgg), Limits: containers.Limits{MemoryMiB: 128},
		Allocations: []server.Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
	}}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ServerID string `json:"server_id"`
	}
	_ = json.Unmarshal(res.Value, &created)
	id := created.ServerID
	t.Cleanup(func() { _ = v.m.Delete(context.Background(), id) })
	waitState(t, v.m, id, server.Offline)
	return id
}

func TestBackupCommands(t *testing.T) {
	ctx := context.Background()
	v := newBackupEnv(t)
	m, bk, sched, p, owner, info := v.m, v.bk, v.sched, v.p, v.owner, v.info
	waitJob := func(res command.Result) jobs.Job {
		t.Helper()
		return v.waitJob(t, res)
	}

	// A new server gets the daily backup schedule.
	id := v.create(t, "smp")
	schedules, err := sched.List(ctx, id)
	if err != nil || len(schedules) != 1 || schedules[0].Steps[0].Type != schedule.StepBackup || schedules[0].NextRun.In(mustLoad(t, "Europe/Berlin")).Hour() != 4 {
		t.Fatalf("default schedules: %+v, %v", schedules, err)
	}

	start := func() {
		t.Helper()
		if err := m.Start(ctx, id); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, id, server.Starting)
		if err := m.SendCommand(id, "alice", "echo READY"); err != nil {
			t.Fatal(err)
		}
		waitState(t, m, id, server.Running)
	}
	start()
	console := func(cmd string) {
		t.Helper()
		if err := m.SendCommand(id, "alice", cmd); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	}
	console("echo day1 > /home/container/level.dat")
	src, err := m.BackupSource(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// A manual backup runs the egg's hooks around the snapshot.
	res, err := p.send("alice", BackupCreate, id, nil, nil)
	if err != nil {
		t.Fatalf("backup.create (unsigned): %v", err)
	}
	waitJob(res)
	var made struct {
		BackupID string `json:"backup_id"`
	}
	_ = json.Unmarshal(res.Value, &made)
	b, err := bk.Get(ctx, id, made.BackupID)
	if err != nil || b.Status != backup.StatusOK || b.Warning != "" || b.Files != 1 {
		t.Fatalf("backup: %+v, %v", b, err)
	}
	// (The shell echoes commands, so the wait_for text isn't in one.)
	var hist string
	for range 50 {
		st, _ := m.Status(id)
		hist = strings.Join(st.Console.History(), "\n")
		if strings.Contains(hist, "\nSAVE-ON") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if i, j, k := strings.Index(hist, "\nSAVE-OFF"), strings.Index(hist, "\nSaved the game"), strings.Index(hist, "\nSAVE-ON"); i < 0 || j < i || k < j {
		t.Fatalf("hooks didn't run in order:\n%s", hist)
	}

	// The worker ran in the backup slice, first in line for the OOM killer.
	w, _ := os.ReadFile(info)
	if !strings.Contains(string(w), "/raptor.slice/raptor-backup.slice/") || !strings.HasSuffix(string(w), "oom=1000") {
		t.Fatalf("worker ran in %q", w)
	}

	// Restoring over the files needs the owner's passkey. The server was
	// running, so it starts again afterwards.
	console("echo griefed > /home/container/level.dat")
	console("touch /home/container/griefer")
	if _, err := p.send("alice", BackupRestore, id, BackupParams{BackupID: b.ID}, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned restore: %v", err)
	}
	res, err = p.send("alice", BackupRestore, id, BackupParams{BackupID: b.ID}, owner)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(res)
	if got, _ := os.ReadFile(filepath.Join(src.Dir, "level.dat")); string(got) != "day1\n" {
		t.Fatalf("level.dat = %q after restore", got)
	}
	if _, err := os.Stat(filepath.Join(src.Dir, "griefer")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("griefer survived the restore")
	}
	fi, err := os.Stat(filepath.Join(src.Dir, "level.dat"))
	if err != nil || fi.Sys().(*syscall.Stat_t).Uid != 988 {
		t.Fatalf("restored file owner: %+v, %v", fi.Sys(), err)
	}
	waitState(t, m, id, server.Starting)
	if err := m.SendCommand(id, "alice", "echo READY"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, id, server.Running)
	list, _ := bk.List(ctx, id)
	if len(list) != 2 || list[0].Kind != backup.KindSafety {
		t.Fatalf("no safety backup: %+v", list)
	}

	// The default schedule's backup step makes a scheduled backup.
	res, err = p.send("alice", ScheduleRun, id, ScheduleParams{ScheduleID: schedules[0].ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(res)
	if list, _ = bk.List(ctx, id); list[0].Kind != backup.KindScheduled || list[0].CreatedBy != "schedule:"+schedules[0].ID {
		t.Fatalf("scheduled backup: %+v", list[0])
	}

	// Locking is unsigned; unlocking lets retention delete the backup, so
	// it's signed.
	if _, err := p.send("alice", BackupLock, id, BackupParams{BackupID: b.ID, Locked: true}, nil); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := p.send("alice", BackupLock, id, BackupParams{BackupID: b.ID}, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned unlock: %v", err)
	}

	// Deleting a backup needs the passkey.
	if _, err := p.send("alice", BackupDelete, id, BackupParams{BackupID: b.ID}, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned delete: %v", err)
	}
	res, err = p.send("alice", BackupDelete, id, BackupParams{BackupID: b.ID}, owner)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(res)
	if _, err := bk.Get(ctx, id, b.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("deleted backup: %v", err)
	}

	// An S3 destination.
	endpoint := startMinIO(t)
	res, err = p.send("alice", BackupDestinationSave, "", DestinationParams{backup.Destination{
		Name: "MinIO", Type: engine.S3, S3: engine.S3Config{Endpoint: "http://" + endpoint, Bucket: "raptor", Prefix: "node/", AccessKey: "raptor", SecretKey: "raptor-secret"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var dest struct {
		DestinationID string `json:"destination_id"`
	}
	_ = json.Unmarshal(res.Value, &dest)
	// Lowering retention needs the passkey; moving to S3 with the same keep
	// values doesn't.
	lower := backup.Policy{DestinationID: dest.DestinationID, Retention: backup.Retention{KeepLast: 1}}
	if _, err := p.send("alice", BackupPolicy, id, lower, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned retention cut: %v", err)
	}
	if _, err := p.send("alice", BackupPolicy, id, backup.Policy{DestinationID: dest.DestinationID, Retention: backup.DefaultRetention}, nil); err != nil {
		t.Fatal(err)
	}
	console("echo offsite > /home/container/level.dat")
	res, err = p.send("alice", BackupCreate, id, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(res)
	_ = json.Unmarshal(res.Value, &made)
	console("echo later > /home/container/level.dat")
	res, err = p.send("alice", BackupRestore, id, BackupParams{BackupID: made.BackupID}, owner)
	if err != nil {
		t.Fatal(err)
	}
	waitJob(res)
	if got, _ := os.ReadFile(filepath.Join(src.Dir, "level.dat")); string(got) != "offsite\n" {
		t.Fatalf("level.dat = %q after restoring from S3", got)
	}

	// Deleting the server deletes its local backups and keeps the S3 ones.
	if err := m.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		list, _ = bk.List(ctx, id)
		local := slices.IndexFunc(list, func(b *backup.Backup) bool { return b.DestinationID == backup.LocalDestination })
		if local < 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(list) == 0 || slices.ContainsFunc(list, func(b *backup.Backup) bool { return b.DestinationID != dest.DestinationID }) {
		t.Fatalf("backups after deleting the server: %+v", list)
	}
}

// Wipe and reinstall, and deleting a server with a final backup, through
// the command path: both are signed, both back up before removing files,
// and a deleted server's final backup restores onto another server with
// the owner's passkey (not a delegate's).
func TestWipeAndFinalBackup(t *testing.T) {
	ctx := context.Background()
	v := newBackupEnv(t)
	id := v.create(t, "smp")
	src, err := v.m.BackupSource(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	plant := func(dir string) {
		t.Helper()
		for name, data := range map[string]string{"world/level.dat": "level", "mods/a.jar": "jar"} {
			p := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // test directory
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(data), 0o644); err != nil { //nolint:gosec // test file
				t.Fatal(err)
			}
		}
		// A link out of the directory: wiping removes the link, not its target.
		if err := os.Symlink("/etc", filepath.Join(dir, "etc-link")); err != nil {
			t.Fatal(err)
		}
	}
	plant(src.Dir)

	// A plain reinstall is unsigned and keeps the files; a wipe is signed.
	res, err := v.p.send("alice", ServerReinstall, id, nil, nil)
	if err != nil {
		t.Fatalf("plain reinstall: %v", err)
	}
	v.waitJob(t, res)
	if _, err := os.Stat(filepath.Join(src.Dir, "world/level.dat")); err != nil {
		t.Fatalf("a plain reinstall removed files: %v", err)
	}
	if _, err := v.p.send("alice", ServerReinstall, id, ReinstallParams{Wipe: true}, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned wipe: %v", err)
	}

	// A destination that can't be reached: a wipe whose safety backup
	// fails removes nothing, and the server stays installed.
	res, err = v.p.send("alice", BackupDestinationSave, "", DestinationParams{backup.Destination{
		Name: "Down", Type: engine.S3, S3: engine.S3Config{Endpoint: "http://127.0.0.1:1", Bucket: "raptor", AccessKey: "a", SecretKey: "b"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var down struct {
		DestinationID string `json:"destination_id"`
	}
	_ = json.Unmarshal(res.Value, &down)
	setDest := func(dest string) {
		t.Helper()
		if _, err := v.p.send("alice", BackupPolicy, id, backup.Policy{DestinationID: dest, Retention: backup.DefaultRetention}, nil); err != nil {
			t.Fatal(err)
		}
	}
	setDest(down.DestinationID)
	res, err = v.p.send("alice", ServerReinstall, id, ReinstallParams{Wipe: true}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	if j := v.job(t, res); j.Status != jobs.Failed || !strings.Contains(j.Error, "no files were removed") {
		t.Fatalf("wipe with a failing backup: %s %s", j.Status, j.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(src.Dir, "world/level.dat")); string(got) != "level" {
		t.Fatal("a wipe whose backup failed removed files")
	}
	if err := v.m.Start(ctx, id); err != nil {
		t.Fatalf("start after the failed wipe: %v", err)
	}
	if err := v.m.Kill(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitState(t, v.m, id, server.Offline)
	setDest(backup.LocalDestination)

	res, err = v.p.send("alice", ServerReinstall, id, ReinstallParams{Wipe: true}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	v.waitJob(t, res)
	if entries, _ := os.ReadDir(src.Dir); len(entries) != 0 {
		t.Fatalf("files left after the wipe: %v", entries)
	}
	if _, err := os.Stat("/etc/hostname"); err != nil {
		t.Fatalf("the wipe followed a link out: %v", err)
	}
	list, _ := v.bk.List(ctx, id)
	list = slices.DeleteFunc(list, func(b *backup.Backup) bool { return b.Status == backup.StatusFailed })
	if len(list) != 1 || list[0].Kind != backup.KindSafety || list[0].Files < 2 {
		t.Fatalf("safety backup: %+v", list)
	}
	// The safety backup brings the files back.
	res, err = v.p.send("alice", BackupRestore, id, BackupParams{BackupID: list[0].ID}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	v.waitJob(t, res)
	if got, _ := os.ReadFile(filepath.Join(src.Dir, "world/level.dat")); string(got) != "level" {
		t.Fatalf("level.dat = %q after restoring the safety backup", got)
	}

	// A final backup that fails keeps the server.
	setDest(down.DestinationID)
	if _, err := v.p.send("alice", ServerDelete, id, DeleteParams{FinalBackup: true}, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned delete: %v", err)
	}
	res, err = v.p.send("alice", ServerDelete, id, DeleteParams{FinalBackup: true}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	if j := v.job(t, res); j.Status != jobs.Failed || !strings.Contains(j.Error, "wasn't deleted") {
		t.Fatalf("delete with a failing backup: %s %s", j.Status, j.Error)
	}
	if st, err := v.m.Status(id); err != nil || st.State != server.Offline {
		t.Fatalf("server after the failed deletion: %+v, %v", st, err)
	}
	if got, _ := os.ReadFile(filepath.Join(src.Dir, "world/level.dat")); string(got) != "level" {
		t.Fatal("files changed after the failed deletion")
	}

	// With a working destination, the final backup is taken and kept.
	setDest(backup.LocalDestination)
	res, err = v.p.send("alice", ServerDelete, id, DeleteParams{FinalBackup: true}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	var done struct {
		FinalBackupID string `json:"final_backup_id"`
	}
	_ = json.Unmarshal(v.waitJob(t, res).Result, &done)
	if _, err := v.m.Status(id); !errors.Is(err, server.ErrNotFound) {
		t.Fatalf("server after deletion: %v", err)
	}
	if _, err := os.Stat(src.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("files after deletion: %v", err)
	}
	// Its other local backups (the safety backup) are deleted; the failed
	// final backup expires like any failed backup.
	time.Sleep(time.Second) // the deletion is queued
	list, _ = v.bk.List(ctx, id)
	list = slices.DeleteFunc(list, func(b *backup.Backup) bool { return b.Status == backup.StatusFailed })
	if len(list) != 1 || list[0].ID != done.FinalBackupID || list[0].Kind != backup.KindFinal || list[0].ExpiresAt.IsZero() {
		for _, b := range list {
			t.Logf("left: %s %s %s %s", b.ID, b.Kind, b.Status, b.DestinationID)
		}
		t.Fatalf("backups after deleting with a final backup (final %s)", done.FinalBackupID)
	}

	// It restores onto another server: the owner may, a delegate for that
	// server may not.
	other := v.create(t, "new-home")
	helper, err := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.AddKey(ctx, v.db, command.KeyParams{
		CredentialID: helper.CredentialID, UserID: "bob", PublicKey: helper.COSE, Role: "delegate",
		ServerID: other, Actions: []string{BackupRestore}, ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}, v.owner.CredentialID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := v.p.send("bob", BackupRestore, other, BackupParams{BackupID: done.FinalBackupID}, helper); !errors.Is(err, command.ErrUntrustedKey) {
		t.Fatalf("delegate restoring a deleted server's backup: %v", err)
	}
	res, err = v.p.send("alice", BackupRestore, other, BackupParams{BackupID: done.FinalBackupID}, v.owner)
	if err != nil {
		t.Fatal(err)
	}
	v.waitJob(t, res)
	otherSrc, err := v.m.BackupSource(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(otherSrc.Dir, "mods/a.jar")); string(got) != "jar" {
		t.Fatalf("a.jar = %q on the new server", got)
	}
}

// startMinIO runs MinIO (Chainguard's image; MinIO no longer publishes one) on a local port and creates the "raptor" bucket. It
// returns the endpoint.
func startMinIO(t *testing.T) string {
	t.Helper()
	port := freePort(t)
	name := fmt.Sprintf("raptor-e2e-minio-%d", port)
	out, err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-p", fmt.Sprintf("127.0.0.1:%d:9000", port),
		"-e", "MINIO_ROOT_USER=raptor", "-e", "MINIO_ROOT_PASSWORD=raptor-secret",
		"cgr.dev/chainguard/minio", "server", "/tmp/data").CombinedOutput()
	if err != nil {
		t.Fatalf("start minio: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	endpoint := fmt.Sprintf("127.0.0.1:%d", port)
	c, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4("raptor", "raptor-secret", "")})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		err = c.MakeBucket(context.Background(), "raptor", minio.MakeBucketOptions{})
		if err == nil {
			return endpoint
		}
		if time.Now().After(deadline) {
			t.Fatalf("create bucket: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
