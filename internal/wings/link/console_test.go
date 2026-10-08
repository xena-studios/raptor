package link

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/server"
)

type fakeServers struct{ consoles map[string]*server.Console }

func (s *fakeServers) List() map[string]server.State { return nil }

func (s *fakeServers) Get(context.Context, string) (*server.Server, error) {
	return nil, errors.New("no")
}

func (s *fakeServers) Status(id string) (server.Status, error) {
	c, ok := s.consoles[id]
	if !ok {
		return server.Status{}, errors.New("no such server")
	}
	return server.Status{State: server.Running, Console: c}, nil
}

// Watching a console through the real hub and link: the history first, then
// new lines as they're written, and only with the Panel's grant for exactly
// that server.
func TestConsole(t *testing.T) {
	f := newFixture(t)
	c := server.NewConsole()
	for i := range 450 {
		c.Backfill(fmt.Sprintf("old %d", i))
	}
	f.servers.consoles["s1"] = c
	waitConnected(t, f)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan nodes.ConsoleBatch, 100)
	done := make(chan error, 1)
	go func() {
		done <- f.hub.Console(ctx, nodeID, f.envelopeFor(ConsoleAction, "s1"), func(b nodes.ConsoleBatch) error {
			got <- b
			return nil
		})
	}()
	var history []string
	for len(history) < 450 {
		b := <-got
		if !b.History {
			t.Fatalf("live lines before the history ended: %v", b.Lines)
		}
		history = append(history, b.Lines...)
	}
	if history[0] != "old 0" || history[449] != "old 449" {
		t.Errorf("history %q … %q", history[0], history[449])
	}
	c.Write("new 1")
	c.Write("new 2")
	var live []string
	for len(live) < 2 {
		b := <-got
		if b.History {
			t.Fatalf("history after live lines: %v", b.Lines)
		}
		live = append(live, b.Lines...)
	}
	if live[0] != "new 1" || live[1] != "new 2" {
		t.Errorf("live %v", live)
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("stream ended with %v", err)
	}

	// Refused: another action's grant, a forged grant, another server's
	// grant used for this one, an unknown server.
	try := func(env []byte) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return f.hub.Console(ctx, nodeID, env, func(nodes.ConsoleBatch) error { return nil })
	}
	if err := try(f.envelopeFor("server.start", "s1")); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("wrong action: %v", err)
	}
	var e command.Envelope
	_ = json.Unmarshal(f.envelopeFor(ConsoleAction, "s1"), &e)
	e.Grant.Signature[0] ^= 1
	forged, _ := json.Marshal(e)
	if err := try(forged); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("forged grant: %v", err)
	}
	_ = json.Unmarshal(f.envelopeFor(ConsoleAction, "s2"), &e)
	e.ServerID = "s1"
	swapped, _ := json.Marshal(e)
	if err := try(swapped); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("another server's grant: %v", err)
	}
	if err := try(f.envelopeFor(ConsoleAction, "s9")); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown server: %v", err)
	}
}

func waitConnected(t *testing.T, f *fixture) {
	t.Helper()
	for range 500 {
		if _, ok := f.hub.Conn(nodeID); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the node never connected")
}
