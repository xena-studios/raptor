//go:build e2e

package databases

import (
	"context"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/events"
)

// testHealth checks a running instance on one network of the owner's: its
// usage is sampled (its connections counted by the engine), it answers and
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
