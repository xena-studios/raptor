//go:build e2e

package databases

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/xena-studios/raptor/internal/wings/events"
)

// testHealth checks a running instance on one network of the owner's: its
// usage is sampled (its connections, uptime, and queries a second from the
// engine, an idle one's at 0 and a busy one's counted), it answers and
// is on its network at its address, and one that stops on its own is down
// once, started again, and back up once.
func testHealth(t *testing.T, svc *Service, ev *events.Outbox, id string) {
	t.Helper()
	ctx := context.Background()
	svc.checkAll(ctx)
	u, ok := svc.Usage(id)
	if !ok || u.Connections < 1 || u.MaxConnections != defaultMaxConnections || u.MemoryBytes <= 0 || u.DiskBytes <= 0 {
		t.Errorf("usage: %+v %v", u, ok)
	}
	testQueryRate(t, svc, id)
	// Seconds later, it's been up a while (a just-started one can read 0).
	if u, _ := svc.Usage(id); u.UptimeSeconds <= 0 || u.UptimeSeconds > 3600 {
		t.Errorf("uptime: %d seconds", u.UptimeSeconds)
	}
	c, err := svc.Check(ctx, id)
	if err != nil || !c.Answering || len(c.Networks) != 1 || !c.Networks[0].Attached || c.Networks[0].Address == "" ||
		c.Networks[0].Actual != c.Networks[0].Address {
		t.Errorf("check: %+v %v", c, err)
	}
	last, err := ev.Last(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// It stops behind Wings' back, as a crash would.
	if err := svc.o.Runtime.StopService(ctx, ContainerName(id), time.Minute); err != nil {
		t.Fatal(err)
	}
	svc.checkAll(ctx)
	waitRunning(t, svc, id)
	svc.checkAll(ctx)
	evs, err := ev.Since(ctx, last, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Type != EventDown || evs[1].Type != EventUp || evs[0].Data["instance_id"] != id {
		t.Errorf("events: %+v", evs)
	}
}

// testQueryRate checks an instance's queries a second: 0 while only Wings
// asks (what sampling adds itself, ownOps, is right for the engine), and a
// burst of queries counted once.
func testQueryRate(t *testing.T, svc *Service, id string) {
	t.Helper()
	ctx := context.Background()
	for i := range 3 {
		before, _ := svc.Usage(id)
		time.Sleep(time.Second)
		svc.checkAll(ctx)
		u, _ := svc.Usage(id)
		t.Logf("idle sample %d: the counter went up %d", i, u.ops-before.ops)
		if u.QueriesPerSecond == nil {
			t.Error("idle: no queries a second")
		} else if *u.QueriesPerSecond >= 0.5 {
			t.Errorf("idle: %.2f queries a second", *u.QueriesPerSecond)
		}
	}
	const burst = 200
	busy(t, svc, id, burst)
	start := len(svc.Recent(id))
	counted := 0.0
	for range 15 {
		time.Sleep(time.Second)
		svc.checkAll(ctx)
		r := svc.Recent(id)
		counted = 0
		for i := max(start, 1); i < len(r); i++ {
			if q := r[i].QueriesPerSecond; q != nil {
				counted += *q * r[i].At.Sub(r[i-1].At).Seconds()
			}
		}
		if counted >= burst*0.95 {
			break
		}
	}
	if counted < burst*0.95 || counted > burst*1.2 {
		t.Errorf("a burst of %d queries counted as %.1f", burst, counted)
	}
}

// busy runs n trivial queries on an instance as its administrator.
func busy(t *testing.T, svc *Service, id string, n int) {
	t.Helper()
	ctx := context.Background()
	in, r, err := svc.instance(ctx, svc.o.Store.Read, id)
	if err != nil {
		t.Fatal(err)
	}
	root, err := svc.sealer.open(r.RootPassword)
	if err != nil {
		t.Fatal(err)
	}
	dir := svc.dir(id)
	switch in.Engine {
	case Redis, Valkey:
		c := redisClient(dir, root)
		defer func() { _ = c.Close() }()
		for range n {
			if err := c.Ping(ctx).Err(); err != nil {
				t.Fatal(err)
			}
		}
	case MongoDB:
		c, err := mongoConnect(ctx, dir, root)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Disconnect(context.Background()) }()
		for range n {
			if err := c.Database("admin").RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
				t.Fatal(err)
			}
		}
	default:
		db, err := connect(in, dir, root)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		db.SetMaxOpenConns(1)
		for range n {
			if _, err := db.ExecContext(ctx, "SELECT 1"); err != nil {
				t.Fatal(err)
			}
		}
	}
}
