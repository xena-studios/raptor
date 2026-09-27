package localapi

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// fakeManager is a server manager with fixed servers.
type fakeManager struct {
	Servers
	mu       sync.Mutex
	servers  map[string]*server.Server
	consoles map[string]*server.Console
	power    []string // "id action user"
	commands []string
	logs     []containers.Line
}

func newFakeManager() *fakeManager {
	f := &fakeManager{servers: map[string]*server.Server{}, consoles: map[string]*server.Console{}}
	for id, name := range map[string]string{
		"0190a1b2-0000-7000-8000-00000000aaaa": "survival",
		"0190a1b2-0000-7000-8000-00000000bbbb": "creative",
		"0190a1b2-0000-7000-8000-00000000cccc": "creative",
	} {
		s := &server.Server{ID: id}
		s.Name = name
		s.Limits.MemoryMiB = 1024
		s.Allocations = []server.Allocation{{IP: "0.0.0.0", Port: 25565, Primary: true}}
		f.servers[id] = s
		f.consoles[id] = server.NewConsole()
	}
	return f
}

func (f *fakeManager) List() map[string]server.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]server.State{}
	for id := range f.servers {
		out[id] = server.Running
	}
	return out
}

func (f *fakeManager) Get(_ context.Context, id string) (*server.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return nil, server.ErrNotFound
}

func (f *fakeManager) Usage(_ context.Context, id string) (server.Usage, error) {
	return server.Usage{State: server.Running, RunningSince: time.Now(), CPUPercent: 12.5, MemoryBytes: 512 << 20}, nil
}

func (f *fakeManager) Status(id string) (server.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.consoles[id]
	if !ok {
		return server.Status{}, server.ErrNotFound
	}
	return server.Status{State: server.Running, Console: c}, nil
}

func (f *fakeManager) Power(_ context.Context, id string, a server.PowerAction, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.power = append(f.power, id+" "+string(a)+" "+user)
	return nil
}

func (f *fakeManager) SendCommand(id, user, cmd string) error {
	if cmd == "" {
		return server.ErrConsoleNotReady
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, id+" "+user+" "+cmd)
	return nil
}

func (f *fakeManager) Logs(ctx context.Context, _ string, tail int, _ bool) (<-chan containers.Line, <-chan error, error) {
	lines, errc := make(chan containers.Line), make(chan error, 1)
	go func() {
		defer close(lines)
		ls := f.logs
		if tail >= 0 && tail < len(ls) {
			ls = ls[len(ls)-tail:]
		}
		for _, l := range ls {
			select {
			case lines <- l:
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			}
		}
		errc <- nil
	}()
	return lines, errc, nil
}

func TestResolve(t *testing.T) {
	f := newFakeManager()
	ctx := context.Background()
	for ref, want := range map[string]string{
		"0190a1b2-0000-7000-8000-00000000aaaa": "0190a1b2-0000-7000-8000-00000000aaaa",
		"0000aaaa":                             "0190a1b2-0000-7000-8000-00000000aaaa",
		"survival":                             "0190a1b2-0000-7000-8000-00000000aaaa",
		" survival ":                           "0190a1b2-0000-7000-8000-00000000aaaa",
	} {
		if got, err := resolve(ctx, f, ref); err != nil || got != want {
			t.Errorf("resolve(%q) = %q, %v", ref, got, err)
		}
	}
	for ref, code := range map[string]connect.Code{
		"":         connect.CodeInvalidArgument,
		"creative": connect.CodeInvalidArgument, // two servers have this name
		"aaaa":     connect.CodeNotFound,        // suffixes need 8 characters
		"Survival": connect.CodeNotFound,        // names are exact
		"nope":     connect.CodeNotFound,
	} {
		if _, err := resolve(ctx, f, ref); connect.CodeOf(err) != code {
			t.Errorf("resolve(%q): %v, want %v", ref, err, code)
		}
	}
}

func TestPowerAndCommandsNeedRoot(t *testing.T) {
	f := newFakeManager()
	s := &Service{}
	s.SetServers(f)
	user := context.WithValue(context.Background(), callerKey{}, "local:alice")
	root := context.WithValue(context.WithValue(context.Background(), rootKey{}, true), callerKey{}, "local:root")

	if _, err := s.Power(user, &localv1.PowerRequest{Server: "survival", Action: localv1.PowerAction_POWER_ACTION_STOP}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-root power: %v", err)
	}
	if _, err := s.SendCommand(user, &localv1.SendCommandRequest{Server: "survival", Command: "say hi"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("non-root command: %v", err)
	}
	if _, err := s.Power(root, &localv1.PowerRequest{Server: "survival"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("no action: %v", err)
	}
	res, err := s.Power(root, &localv1.PowerRequest{Server: "0000aaaa", Action: localv1.PowerAction_POWER_ACTION_RESTART})
	if err != nil || res.GetServerId() != "0190a1b2-0000-7000-8000-00000000aaaa" || res.GetState() != "running" {
		t.Fatalf("root power: %v %v", res, err)
	}
	if _, err := s.SendCommand(root, &localv1.SendCommandRequest{Server: "survival", Command: "say hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendCommand(root, &localv1.SendCommandRequest{Server: "survival"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("manager error mapping: %v", err)
	}
	// Both are attributed to the Unix user who asked.
	if !slices.Equal(f.power, []string{"0190a1b2-0000-7000-8000-00000000aaaa restart local:root"}) ||
		!slices.Equal(f.commands, []string{"0190a1b2-0000-7000-8000-00000000aaaa local:root say hi"}) {
		t.Fatalf("power %q commands %q", f.power, f.commands)
	}
}

func TestListServers(t *testing.T) {
	s := &Service{}
	s.SetServers(newFakeManager())
	res, err := s.ListServers(context.Background(), &localv1.ListServersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range res.GetServers() {
		names = append(names, x.GetName())
	}
	if !slices.Equal(names, []string{"creative", "creative", "survival"}) {
		t.Fatalf("names = %q", names)
	}
	x := res.GetServers()[2]
	if x.GetState() != "running" || x.GetCpuPercent() != 12.5 || x.GetMemoryBytes() != 512<<20 ||
		x.GetMemoryLimitBytes() != 1024<<20 || x.GetAddress() != "0.0.0.0:25565" || x.GetRunningSince() == nil {
		t.Fatalf("info = %v", x)
	}
}

// Streams go over the real socket: history, live lines, and an end when
// Wings shuts down (instead of holding the shutdown until its deadline).
func TestStreamsOverSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "wsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "wings.sock")
	f := newFakeManager()
	f.logs = []containers.Line{{Text: "one"}, {Text: "two"}, {Text: "three", Time: time.Unix(1700000000, 0)}}
	svc := &Service{}
	svc.SetServers(f)
	srv, err := Listen(context.Background(), path, "", svc, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	c := Dial(path)
	ctx := context.Background()

	logs, err := c.TailLogs(ctx, &localv1.TailLogsRequest{Server: "survival", Lines: 2})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for logs.Receive() {
		got = append(got, logs.Msg().GetText())
	}
	if logs.Err() != nil || !slices.Equal(got, []string{"two", "three"}) {
		t.Fatalf("logs %q, %v", got, logs.Err())
	}

	con := f.consoles["0190a1b2-0000-7000-8000-00000000aaaa"]
	con.Write("before")
	st, err := c.StreamConsole(ctx, &localv1.StreamConsoleRequest{Server: "survival"})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Receive() || st.Msg().GetText() != "before" {
		t.Fatalf("history: %v", st.Err())
	}
	// The subscription exists once history arrived.
	con.Write("live")
	if !st.Receive() || st.Msg().GetText() != "live" {
		t.Fatalf("live: %v", st.Err())
	}

	done := make(chan error, 1)
	go func() {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		done <- srv.Shutdown(sctx)
	}()
	for st.Receive() {
	}
	if connect.CodeOf(st.Err()) != connect.CodeUnavailable {
		t.Fatalf("stream end: %v", st.Err())
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited for the console stream")
	}
	if errors.Is(st.Err(), context.DeadlineExceeded) {
		t.Fatal("stream timed out")
	}
}
