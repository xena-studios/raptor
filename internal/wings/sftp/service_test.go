package sftp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/events"
)

// fakeListen pretends ports in busy are taken by other programs and records
// what was bound.
type fakeListen struct {
	mu    sync.Mutex
	busy  map[int]bool
	bound []int
}

func (f *fakeListen) listen(_, addr string) (net.Listener, error) {
	_, p, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy[port] {
		return nil, errors.New("address already in use")
	}
	f.bound = append(f.bound, port)
	return net.Listen("tcp", "127.0.0.1:0")
}

func TestService(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	fl := &fakeListen{busy: map[int]bool{2022: true}}
	o := ServiceOptions{
		Options:   Options{HostKey: e.hostKey, Auth: e.panel, Servers: e.servers, Events: events.New(e.db)},
		Store:     e.db,
		Port:      2022,
		Allocated: func(context.Context) ([]int, error) { return []int{2023}, nil },
		Listen:    fl.listen,
	}

	// Off by default.
	s := NewService(o)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Enabled || st.Port != 0 {
		t.Fatalf("on by default: %+v", st)
	}

	// 2022 is taken and 2023 belongs to a server, so 2024.
	st, err := s.SetEnabled(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || st.Port != 2024 || st.Fingerprint != Fingerprint(e.hostKey) {
		t.Fatalf("enabled: %+v", st)
	}
	if _, err := s.SetEnabled(ctx, true); err != nil { // idempotent
		t.Fatal(err)
	}
	s.Close() // Wings stops; the stored state stays on

	// After a restart it comes back on the same port, even though 2022 is
	// free again: users' saved connections keep working.
	fl.busy = nil
	s = NewService(o)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); !st.Enabled || st.Port != 2024 {
		t.Fatalf("after restart: %+v", st)
	}
	if st, err := s.SetEnabled(ctx, false); err != nil || st.Enabled || st.Port != 0 {
		t.Fatalf("disable: %+v %v", st, err)
	}
	s = NewService(o)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Status().Enabled {
		t.Fatal("came back on after being turned off")
	}

	// The Panel hears about every change, and the port after every restart
	// (it moves if the old one was taken meanwhile).
	evs, err := events.New(e.db).Since(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	for _, ev := range evs {
		if ev.Type == EventNode {
			got = append(got, ev.Data["enabled"], ev.Data["port"])
		}
	}
	want := []any{true, float64(2024), true, float64(2024), false, float64(0)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("node.sftp events: %v", got)
	}
}
