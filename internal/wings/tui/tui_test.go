package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

type fakeBackend struct {
	mu      sync.Mutex
	servers []*localv1.ServerInfo
	calls   []string
	console chan string
}

func (f *fakeBackend) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeBackend) Servers(context.Context) ([]*localv1.ServerInfo, error) { return f.servers, nil }

func (f *fakeBackend) Metrics(context.Context, string) (*localv1.GetMetricsResponse, error) {
	players := int32(3)
	return &localv1.GetMetricsResponse{
		Latest: &localv1.MetricPoint{PlayersMax: &players},
		Points: []*localv1.MetricPoint{{CpuAvg: 10}, {CpuAvg: 50}, {CpuAvg: 100}},
	}, nil
}

func (f *fakeBackend) Power(_ context.Context, id string, a localv1.PowerAction) error {
	f.record("power " + id + " " + a.String())
	return nil
}

func (f *fakeBackend) Command(_ context.Context, id, cmd string) error {
	f.record("command " + id + " " + cmd)
	return nil
}

func (f *fakeBackend) Backup(_ context.Context, id string) (string, error) {
	f.record("backup " + id)
	return "backup queued", nil
}

func (f *fakeBackend) Console(context.Context, string) (<-chan string, error) { return f.console, nil }

// drive runs a command and feeds its message back, the way Bubble Tea
// would, a few levels deep (batches included).
func drive(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for n := 0; len(queue) > 0 && n < 50; n++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		// Commands that wait (timers, a console with no more lines) are
		// skipped.
		got := make(chan tea.Msg, 1)
		go func() { got <- c() }()
		var msg tea.Msg
		select {
		case msg = <-got:
		case <-time.After(100 * time.Millisecond):
			continue
		}
		switch msg := msg.(type) {
		case tea.BatchMsg:
			for _, c := range msg {
				queue = append(queue, c)
			}
			continue
		case tickMsg, metricTickMsg, nil:
			continue // timers: not in tests
		case lineMsg:
			if !msg.ok {
				continue
			}
		}
		var next tea.Model
		next, c = m.Update(msg)
		m = next.(Model)
		queue = append(queue, c)
	}
	return m
}

func key(m Model, k string) (Model, tea.Cmd) {
	var msg tea.KeyMsg
	switch k {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

func newTest(t *testing.T, root bool) (Model, *fakeBackend) {
	f := &fakeBackend{
		servers: []*localv1.ServerInfo{
			{Id: "0190-aaaaaaaa", Name: "survival", State: "running", CpuPercent: 42, MemoryBytes: 1 << 30, MemoryLimitBytes: 2 << 30, Address: "0.0.0.0:25565"},
			{Id: "0190-bbbbbbbb", Name: "creative", State: "offline"},
		},
		console: make(chan string, 10),
	}
	m := New(context.Background(), f, root)
	return drive(t, m, m.Init()), f
}

func TestList(t *testing.T) {
	m, f := newTest(t, true)
	v := m.View()
	for _, want := range []string{"survival", "aaaaaaaa", "42%", "1.0 GiB", "0.0.0.0:25565", "creative"} {
		if !strings.Contains(v, want) {
			t.Errorf("list doesn't show %q:\n%s", want, v)
		}
	}
	m, _ = key(m, "down")
	m, cmd := key(m, "s")
	drive(t, m, cmd)
	if len(f.calls) != 1 || f.calls[0] != "power 0190-bbbbbbbb POWER_ACTION_START" {
		t.Errorf("start: %v", f.calls)
	}
}

func TestDetail(t *testing.T) {
	m, f := newTest(t, true)
	f.console <- "[Server thread/INFO]: Done (3.2s)!"
	m, cmd := key(m, "enter")
	m = drive(t, m, cmd)
	v := m.View()
	for _, want := range []string{"survival", "Done (3.2s)!", "Players 3", "1.0 GiB / 2.0 GiB", "█"} {
		if !strings.Contains(v, want) {
			t.Errorf("detail doesn't show %q:\n%s", want, v)
		}
	}

	// A console command.
	m, cmd = key(m, ":")
	m = drive(t, m, cmd)
	for _, r := range "say hi" {
		m, _ = key(m, string(r))
	}
	m, cmd = key(m, "enter")
	m = drive(t, m, cmd)
	m, _ = key(m, "esc") // leave the command line
	// Kill asks first.
	m, _ = key(m, "K")
	if !strings.Contains(m.View(), "kill survival?") {
		t.Fatalf("no kill prompt:\n%s", m.View())
	}
	m, _ = key(m, "n")
	m, _ = key(m, "K")
	m, cmd = key(m, "y")
	drive(t, m, cmd)
	m, cmd = key(m, "b")
	drive(t, m, cmd)
	want := []string{"command 0190-aaaaaaaa say hi", "power 0190-aaaaaaaa POWER_ACTION_KILL", "backup 0190-aaaaaaaa"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls: %v", f.calls)
	}
	m, _ = key(m, "esc")
	if m.view != listView {
		t.Error("esc didn't go back")
	}
}

// Without root the TUI looks but doesn't act.
func TestNotRoot(t *testing.T) {
	m, f := newTest(t, false)
	for _, k := range []string{"s", "b", "K"} {
		var cmd tea.Cmd
		m, cmd = key(m, k)
		m = drive(t, m, cmd)
		if !strings.Contains(m.View(), "needs root") {
			t.Errorf("%s as non-root:\n%s", k, m.View())
		}
	}
	m, cmd := key(m, "enter")
	m = drive(t, m, cmd)
	m, _ = key(m, ":")
	if m.typing || !strings.Contains(m.View(), "needs root") {
		t.Error("typed a command as non-root")
	}
	if len(f.calls) != 0 {
		t.Errorf("acted as non-root: %v", f.calls)
	}
}

func TestSparkline(t *testing.T) {
	got := Sparkline([]float64{0, 1, 2, 4}, 10)
	if !strings.Contains(got, "▁▂▄█") {
		t.Errorf("sparkline %q", got)
	}
	if got := Sparkline(make([]float64, 100), 5); strings.Count(got, "▁") != 5 {
		t.Errorf("width: %q", got)
	}
}
