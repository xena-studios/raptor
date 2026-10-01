//go:build e2e

package actions

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/schedule"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// soakEgg starts by itself (the yolk runs its startup, which prints the done
// line), so a scheduled restart needs no one at the console, like a real
// game.
const soakEgg = `{
	"meta": {"version": "PTDL_v2"}, "name": "Soak",
	"docker_images": {"Debian": "ghcr.io/pterodactyl/yolks:debian"}, "startup": "tail -n +1 -f ready.txt",
	"config": {"files": "{}", "startup": "{\"done\": \"READY\"}", "stop": "^C"},
	"scripts": {"installation": {"script": "echo READY > /mnt/server/ready.txt", "container": "busybox:1", "entrypoint": "sh"}},
	"variables": []
}`

// TestSeedSoak adds servers with scheduled restarts and backups to the real
// daemon's database, for the soak test (scripts/soak.sh). Wings must be
// stopped; it adopts them when it starts. It prints "SOAK <id>" per server.
//
//	RAPTOR_SOAK_DB=/var/lib/raptor/state.db RAPTOR_SOAK_SERVERS=3 \
//	  RAPTOR_SOAK_RESTART='0 */6 * * *' RAPTOR_SOAK_BACKUP='0 */2 * * *' actions-e2e -test.run TestSeedSoak
func TestSeedSoak(t *testing.T) {
	path := os.Getenv("RAPTOR_SOAK_DB")
	if path == "" {
		t.Skip("RAPTOR_SOAK_DB not set")
	}
	n, _ := strconv.Atoi(cmp.Or(os.Getenv("RAPTOR_SOAK_SERVERS"), "3"))
	restart := cmp.Or(os.Getenv("RAPTOR_SOAK_RESTART"), "0 */6 * * *")
	backup := cmp.Or(os.Getenv("RAPTOR_SOAK_BACKUP"), "0 */2 * * *")
	ctx := context.Background()
	rt, err := docker.New(docker.Config{Network: "raptor_nw", InstallNetwork: "raptor_install"})
	if err != nil {
		t.Fatal(err)
	}
	nets, err := rt.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	u, err := user.Lookup("raptor")
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	eng := jobs.New(jobs.Options{Store: db, LogDir: "/var/log/raptor/jobs", Limits: map[string]int{"install": 2}})
	m := server.New(server.Options{
		Runtime: rt, Store: db, Storage: &storage.Volume{Path: "/var/lib/raptor/volumes", Soft: true}, VolumesDir: "/var/lib/raptor/volumes",
		TmpDir: "/var/lib/raptor/tmp", LogDir: "/var/log/raptor", UID: uid, GID: gid, Timezone: "UTC",
		DockerInterface: nets.Server.Gateway.String(), Jobs: eng, Events: events.New(db),
	})
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	sched := schedule.New(schedule.Options{Store: db, Jobs: eng, Events: events.New(db), Servers: m})
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for i := range n {
		id, err := m.Create(ctx, server.Config{
			Name: fmt.Sprintf("soak-%d", i+1), Egg: []byte(soakEgg),
			Limits: containers.Limits{MemoryMiB: 64}, Settings: server.DefaultSettings(),
			Allocations: []server.Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
		}, server.CreateOptions{StartAfterInstall: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range []schedule.Definition{
			{Name: "Restart", Cron: restart, Enabled: true, Steps: []schedule.Step{
				{Type: schedule.StepCommand, Command: "echo restarting soon", ContinueOnFailure: true},
				{Type: schedule.StepPower, Action: server.PowerRestart},
			}},
			{Name: "Backup", Cron: backup, Enabled: true, Steps: []schedule.Step{{Type: schedule.StepBackup}}},
		} {
			if _, err := sched.Create(ctx, id, d); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(3 * time.Minute)
		for {
			st, err := m.Status(id)
			if err == nil && st.State == server.Running {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s didn't start", id)
			}
			time.Sleep(time.Second)
		}
		fmt.Println("SOAK", id)
	}
}
