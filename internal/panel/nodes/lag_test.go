package nodes

import (
	"maps"
	"slices"
	"testing"
	"time"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/doctor"
)

// The mirror's backlog: a node is behind from when it says it has events
// until they're applied, and forgotten when it isn't connected here.
func TestBacklog(t *testing.T) {
	m := &Mirror{Hub: &Hub{}}
	m.init()
	m.mu.Lock()
	m.behind["a"] = backlog{head: 10, since: time.Now().Add(-90 * time.Second)}
	m.behind["b"] = backlog{head: 5, since: time.Now().Add(-5 * time.Second)}
	m.mu.Unlock()
	if n, oldest := m.backlog(time.Now()); n != 2 || oldest < 89*time.Second {
		t.Fatalf("backlog: %d nodes, %s", n, oldest)
	}
	m.applied("a", 9) // not all of it
	if n, _ := m.backlog(time.Now()); n != 2 {
		t.Errorf("partly applied: %d behind", n)
	}
	m.applied("a", 10)
	m.forget("b")
	if n, oldest := m.backlog(time.Now()); n != 0 || oldest != 0 {
		t.Errorf("caught up: %d behind, %s", n, oldest)
	}
}

func TestVersions(t *testing.T) {
	h := &Hub{}
	h.init()
	for id, v := range map[string]string{"1": "1.4.0", "2": "1.4.0", "3": "v1.5.0-rc.1", "4": "dev", "5": "../../evil", "6": ""} {
		h.conns[id] = &Conn{NodeID: id, Hello: nodelink.Hello{Software: v}}
	}
	got := h.versions()
	if got["1.4.0"] != 2 || got["v1.5.0-rc.1"] != 1 || got["dev"] != 1 || got["other"] != 2 || len(got) != 4 {
		t.Errorf("versions: %v", got)
	}
}

func TestNodeHealth(t *testing.T) {
	h := &Hub{}
	h.init()
	for id, r := range map[string]*nodev1.ReportHealthRequest{
		"1": {HostDiskLow: true, Failing: []string{"firewall", "firewall"}, Warning: []string{"ssh"}},
		"2": {Failing: []string{"firewall", "../../evil"}},
		"3": {},
		"4": nil, // an older Wings
	} {
		c := &Conn{NodeID: id}
		if r != nil {
			c.health.Store(r)
		}
		h.conns[id] = c
	}
	got := h.health()
	if got.reporting != 3 || got.diskLow != 1 {
		t.Errorf("reporting %d, disk low %d", got.reporting, got.diskLow)
	}
	want := map[[2]string]int64{{"firewall", "fail"}: 2, {"other", "fail"}: 1, {"ssh", "warn"}: 1}
	if !maps.Equal(got.checks, want) {
		t.Errorf("checks: %v", got.checks)
	}
}

// The checks with their own series are doctor's.
func TestDoctorChecks(t *testing.T) {
	var ids []string
	for _, c := range doctor.Checks() {
		ids = append(ids, c.ID)
	}
	if !slices.Equal(ids, doctorChecks) {
		t.Errorf("doctor's checks are %v", ids)
	}
}
