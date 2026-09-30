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

func TestBackupCommands(t *testing.T) {
	ctx := context.Background()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(t.TempDir(), "worker")
	var (
		bk    *backup.Manager
		sched *schedule.Scheduler
		eng   *jobs.Engine
	)
	dir := t.TempDir()
	m, db := newManagerWith(t, func(m *server.Manager, db *store.DB, e *jobs.Engine) {
		eng = e
		bk = backup.New(backup.Options{
			Store: db, Jobs: e, Events: events.New(db), Servers: m,
			Runner: backup.Process{
				Command: []string{exe}, Slice: "raptor-backup.slice", MemoryMax: 1 << 30,
				Env: []string{"RAPTOR_E2E_WORKER=1", "RAPTOR_E2E_WORKER_INFO=" + info},
			},
			LocalPath: filepath.Join(dir, "backups"), StateDir: filepath.Join(dir, "kopia"),
		})
		sched = schedule.New(schedule.Options{Store: db, Jobs: e, Events: events.New(db), Servers: m, Backups: bk})
	}, func(o *server.Options) {
		o.Deleted = func(ctx context.Context, id string) { bk.ServerDeleted(ctx, id) }
	})
	if err := bk.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bk.Close)
	sched.Start()
	t.Cleanup(sched.Close)

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	x := &command.Executor{DB: db, NodeID: nodeID, RP: rp, PanelKey: pub}
	Register(x, m, Defaults{BackupSchedule: func(ctx context.Context, q *store.Queries, id string) error {
		_, err := sched.CreateTx(ctx, q, id, schedule.DefaultBackup("Europe/Berlin"))
		return err
	}})
	RegisterSchedules(x, sched)
	RegisterBackups(x, bk)
	p := &panel{t: t, x: x, key: priv}
	owner, err := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.AddKey(ctx, db, command.KeyParams{CredentialID: owner.CredentialID, UserID: "alice", PublicKey: owner.COSE, Role: "owner"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	// A new server gets the daily backup schedule.
	res, err := p.send("alice", ServerCreate, "", CreateParams{ServerConfig: ServerConfig{
		Name: "smp", Egg: []byte(hookEgg), Limits: containers.Limits{MemoryMiB: 128},
		Allocations: []server.Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
	}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ServerID string `json:"server_id"`
	}
	_ = json.Unmarshal(res.Value, &created)
	id := created.ServerID
	t.Cleanup(func() { _ = m.Delete(context.Background(), id) })
	schedules, err := sched.List(ctx, id)
	if err != nil || len(schedules) != 1 || schedules[0].Steps[0].Type != schedule.StepBackup || schedules[0].NextRun.In(mustLoad(t, "Europe/Berlin")).Hour() != 4 {
		t.Fatalf("default schedules: %+v, %v", schedules, err)
	}

	waitState(t, m, id, server.Offline)
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
	waitJob := func(res command.Result) jobs.Job {
		t.Helper()
		var r struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(res.Value, &r)
		wctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		j, err := eng.Wait(wctx, r.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != jobs.Succeeded {
			log, _ := eng.Log(j.ID)
			t.Fatalf("job %s %s: %s\n%s", j.Type, j.Status, j.Error, log)
		}
		return j
	}

	// A manual backup runs the egg's hooks around the snapshot.
	res, err = p.send("alice", BackupCreate, id, nil, nil)
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
