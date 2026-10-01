// Package tui is `raptor tui`: a terminal view of the node's servers, their
// stats and consoles, with power and backup keys (docs/WINGS.md#cli-scope).
// It only talks to Wings' local API, like the rest of the CLI.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/host"
)

// Backend is what the TUI asks Wings (the local API in the CLI; a fake in
// tests).
type Backend interface {
	Servers(ctx context.Context) ([]*localv1.ServerInfo, error)
	Metrics(ctx context.Context, id string) (*localv1.GetMetricsResponse, error)
	Power(ctx context.Context, id string, a localv1.PowerAction) error
	Command(ctx context.Context, id, cmd string) error
	// Backup starts a backup and says what happened.
	Backup(ctx context.Context, id string) (string, error)
	// Console streams a server's console: history, then live lines. The
	// channel closes when the stream ends or ctx does.
	Console(ctx context.Context, id string) (<-chan string, error)
}

// Refresh intervals.
const (
	refreshServers = 2 * time.Second
	refreshMetrics = 10 * time.Second
	consoleLines   = 500
)

type view int

const (
	listView view = iota
	detailView
)

// Model is the TUI's state.
type Model struct {
	ctx     context.Context
	backend Backend
	root    bool

	view     view
	servers  []*localv1.ServerInfo
	cursor   int
	selected string // the server in the detail view
	metrics  *localv1.GetMetricsResponse
	console  []string
	lines    <-chan string
	stop     context.CancelFunc
	input    textinput.Model
	typing   bool
	confirm  string // a pending kill: the server's ID
	status   string
	err      string // the last action's error
	listErr  string // the server list couldn't be loaded
	width    int
	height   int
}

// New returns the TUI's model. root says whether actions are allowed.
func New(ctx context.Context, b Backend, root bool) Model {
	in := textinput.New()
	in.Placeholder = "console command"
	in.CharLimit = 2000
	return Model{ctx: ctx, backend: b, root: root, input: in, width: 100, height: 30}
}

// Messages.
type (
	serversMsg struct {
		list []*localv1.ServerInfo
		err  error
	}
	metricsMsg struct {
		id string
		m  *localv1.GetMetricsResponse
	}
	consoleMsg struct {
		id  string
		ch  <-chan string
		err error
	}
	lineMsg struct {
		id   string
		line string
		ok   bool
	}
	doneMsg struct {
		status string
		err    error
	}
	tickMsg       struct{}
	metricTickMsg struct{}
)

// Init loads the server list.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadServers(), tick(refreshServers, tickMsg{}))
}

func tick(d time.Duration, msg tea.Msg) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return msg })
}

func (m Model) loadServers() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		l, err := m.backend.Servers(ctx)
		return serversMsg{l, err}
	}
}

func (m Model) loadMetrics(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
		defer cancel()
		r, err := m.backend.Metrics(ctx, id)
		if err != nil {
			return metricsMsg{id: id}
		}
		return metricsMsg{id, r}
	}
}

func nextLine(id string, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		l, ok := <-ch
		return lineMsg{id, l, ok}
	}
}

// run does an action in the background and reports how it went.
func (m Model) run(what string, fn func(ctx context.Context) (string, error)) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 10*time.Minute)
		defer cancel()
		s, err := fn(ctx)
		if err != nil {
			return doneMsg{err: fmt.Errorf("%s: %w", what, err)}
		}
		return doneMsg{status: s}
	}
}

func (m Model) current() *localv1.ServerInfo {
	id := m.selected
	if m.view == listView && m.cursor < len(m.servers) {
		id = m.servers[m.cursor].GetId()
	}
	for _, s := range m.servers {
		if s.GetId() == id {
			return s
		}
	}
	return nil
}

// Update handles a message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case serversMsg:
		if msg.err != nil {
			m.listErr = msg.err.Error()
			return m, nil
		}
		m.listErr, m.servers = "", msg.list
		m.cursor = min(m.cursor, max(len(m.servers)-1, 0))
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadServers(), tick(refreshServers, tickMsg{}))
	case metricTickMsg:
		if m.view != detailView {
			return m, nil
		}
		return m, tea.Batch(m.loadMetrics(m.selected), tick(refreshMetrics, metricTickMsg{}))
	case metricsMsg:
		if msg.id == m.selected {
			m.metrics = msg.m
		}
		return m, nil
	case consoleMsg:
		if msg.id != m.selected {
			return m, nil
		}
		if msg.err != nil {
			m.err = "console: " + msg.err.Error()
			return m, nil
		}
		m.lines = msg.ch
		return m, nextLine(msg.id, msg.ch)
	case lineMsg:
		if msg.id != m.selected || !msg.ok {
			return m, nil
		}
		m.console = append(m.console, msg.line)
		if len(m.console) > consoleLines {
			m.console = m.console[len(m.console)-consoleLines:]
		}
		return m, nextLine(msg.id, m.lines)
	case doneMsg:
		if msg.err != nil {
			m.err, m.status = msg.err.Error(), ""
		} else {
			m.err, m.status = "", msg.status
		}
		return m, m.loadServers()
	case tea.KeyMsg:
		return m.key(msg)
	}
	if m.typing {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) open(id string) (Model, tea.Cmd) {
	if m.stop != nil {
		m.stop()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.view, m.selected, m.stop = detailView, id, cancel
	m.console, m.metrics, m.lines, m.status, m.err = nil, nil, nil, "", ""
	b := m.backend
	return m, tea.Batch(m.loadMetrics(id), tick(refreshMetrics, metricTickMsg{}), func() tea.Msg {
		ch, err := b.Console(ctx, id)
		return consoleMsg{id, ch, err}
	})
}

func (m Model) back() Model {
	if m.stop != nil {
		m.stop()
		m.stop = nil
	}
	m.view, m.selected, m.typing, m.confirm = listView, "", false, ""
	m.input.Blur()
	return m
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.err = ""
	if m.typing {
		switch k.Type {
		case tea.KeyEsc:
			m.typing = false
			m.input.Blur()
			return m, nil
		case tea.KeyEnter:
			cmd := strings.TrimSpace(m.input.Value())
			m.input.SetValue("")
			if cmd == "" {
				return m, nil
			}
			id, b := m.selected, m.backend
			return m, m.run("command", func(ctx context.Context) (string, error) { return "", b.Command(ctx, id, cmd) })
		}
		var c tea.Cmd
		m.input, c = m.input.Update(k)
		return m, c
	}
	if m.confirm != "" {
		id := m.confirm
		m.confirm = ""
		if k.String() == "y" {
			return m, m.power(id, localv1.PowerAction_POWER_ACTION_KILL, "killed")
		}
		m.status = "not killed"
		return m, nil
	}
	switch k.String() {
	case "ctrl+c", "q":
		if m.stop != nil {
			m.stop()
		}
		return m, tea.Quit
	case "esc", "backspace":
		if m.view == detailView {
			return m.back(), nil
		}
	case "up", "k":
		if m.view == listView && m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.view == listView && m.cursor < len(m.servers)-1 {
			m.cursor++
		}
	case "enter":
		if m.view == listView && m.cursor < len(m.servers) {
			return m.open(m.servers[m.cursor].GetId())
		}
	}
	s := m.current()
	if s == nil {
		return m, nil
	}
	switch k.String() {
	case "s":
		return m, m.power(s.GetId(), localv1.PowerAction_POWER_ACTION_START, "started")
	case "x":
		return m, m.power(s.GetId(), localv1.PowerAction_POWER_ACTION_STOP, "stopped")
	case "r":
		return m, m.power(s.GetId(), localv1.PowerAction_POWER_ACTION_RESTART, "restarted")
	case "K":
		if !m.root {
			m.err = "needs root: run sudo raptor tui"
			return m, nil
		}
		m.confirm = s.GetId()
		m.status = fmt.Sprintf("kill %s? unsaved data is lost (y/N)", s.GetName())
	case "b":
		if !m.root {
			m.err = "needs root: run sudo raptor tui"
			return m, nil
		}
		id, b := s.GetId(), m.backend
		m.status = "backing up " + s.GetName() + "…"
		return m, m.run("backup", func(ctx context.Context) (string, error) { return b.Backup(ctx, id) })
	case ":", "/":
		if m.view != detailView {
			return m, nil
		}
		if !m.root {
			m.err = "sending commands needs root: run sudo raptor tui"
			return m, nil
		}
		m.typing = true
		return m, m.input.Focus()
	}
	return m, nil
}

func (m Model) power(id string, a localv1.PowerAction, done string) tea.Cmd {
	if !m.root {
		return func() tea.Msg { return doneMsg{err: fmt.Errorf("needs root: run sudo raptor tui")} }
	}
	name := id
	for _, s := range m.servers {
		if s.GetId() == id {
			name = s.GetName()
		}
	}
	b := m.backend
	return m.run(strings.ToLower(strings.TrimPrefix(a.String(), "POWER_ACTION_")), func(ctx context.Context) (string, error) {
		return name + " " + done, b.Power(ctx, id, a)
	})
}

// Styles.
var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	selStyle   = lipgloss.NewStyle().Reverse(true)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	stateStyle = map[string]lipgloss.Style{
		"running":        lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		"starting":       lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"stopping":       lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		"crashed":        lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		"install_failed": lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
	}
)

func state(s string) string {
	if st, ok := stateStyle[s]; ok {
		return st.Render(s)
	}
	return s
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

// View renders the screen.
func (m Model) View() string {
	var b strings.Builder
	if m.view == detailView {
		m.detail(&b)
	} else {
		m.list(&b)
	}
	b.WriteString("\n")
	switch {
	case m.err != "":
		b.WriteString(errStyle.Render(m.err))
	case m.listErr != "":
		b.WriteString(errStyle.Render("can't reach Wings: " + m.listErr))
	case m.status != "":
		b.WriteString(m.status)
	}
	return b.String()
}

func (m Model) list(b *strings.Builder) {
	b.WriteString(titleStyle.Render("Raptor servers") + dimStyle.Render("  ↑/↓ select · enter open · s start · x stop · r restart · K kill · b backup · q quit") + "\n\n")
	if len(m.servers) == 0 {
		b.WriteString(dimStyle.Render("No servers on this node.") + "\n")
		return
	}
	fmt.Fprintf(b, "%-24s %-8s %-15s %6s %10s %10s  %s\n", "NAME", "ID", "STATE", "CPU", "MEMORY", "DISK", "ADDRESS")
	for i, s := range m.servers {
		row := fmt.Sprintf("%-24s %-8s %-15s %5.0f%% %10s %10s  %s", trunc(s.GetName(), 24), shortID(s.GetId()), s.GetState(),
			s.GetCpuPercent(), host.Bytes(s.GetMemoryBytes()), host.Bytes(s.GetDiskBytes()), s.GetAddress())
		if i == m.cursor {
			row = selStyle.Render(row)
		} else {
			row = strings.Replace(row, s.GetState(), state(s.GetState()), 1)
		}
		b.WriteString(row + "\n")
	}
}

func trunc(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func (m Model) detail(b *strings.Builder) {
	s := m.current()
	if s == nil {
		b.WriteString("This server is gone. esc goes back.\n")
		return
	}
	fmt.Fprintf(b, "%s %s  %s  %s\n", titleStyle.Render(s.GetName()), dimStyle.Render(shortID(s.GetId())), state(s.GetState()), s.GetAddress())
	b.WriteString(dimStyle.Render("esc back · s start · x stop · r restart · K kill · b backup · : command · q quit") + "\n\n")
	mem := host.Bytes(s.GetMemoryBytes())
	if s.GetMemoryLimitBytes() > 0 {
		mem += " / " + host.Bytes(s.GetMemoryLimitBytes())
	}
	disk := host.Bytes(s.GetDiskBytes())
	if s.GetDiskLimitBytes() > 0 {
		disk += " / " + host.Bytes(s.GetDiskLimitBytes())
	}
	players := "-"
	var cpu, memHist []float64
	if mt := m.metrics; mt != nil {
		if l := mt.GetLatest(); l != nil && l.PlayersMax != nil {
			players = fmt.Sprintf("%d", l.GetPlayersMax())
		}
		for _, p := range mt.GetPoints() {
			cpu = append(cpu, p.GetCpuAvg())
			memHist = append(memHist, float64(p.GetMemoryAvg()))
		}
	}
	fmt.Fprintf(b, "CPU     %5.0f%%  %s\n", s.GetCpuPercent(), Sparkline(cpu, 60))
	fmt.Fprintf(b, "Memory  %s  %s\n", mem, Sparkline(memHist, 60))
	fmt.Fprintf(b, "Disk    %s    Players %s\n\n", disk, players)

	rows := max(m.height-12, 5)
	lines := m.console
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	for _, l := range lines {
		b.WriteString(trunc(l, max(m.width, 20)) + "\n")
	}
	if m.typing {
		b.WriteString(m.input.View() + "\n")
	}
}

var blocks = []rune("▁▂▃▄▅▆▇█")

// Sparkline draws the last width values as block characters, scaled to
// their maximum.
func Sparkline(v []float64, width int) string {
	if len(v) > width {
		v = v[len(v)-width:]
	}
	hi := 0.0
	for _, x := range v {
		hi = max(hi, x)
	}
	var b strings.Builder
	for _, x := range v {
		i := 0
		if hi > 0 {
			i = int(x / hi * float64(len(blocks)-1))
		}
		b.WriteRune(blocks[min(max(i, 0), len(blocks)-1)])
	}
	return dimStyle.Render(b.String())
}

// Run shows the TUI until the user quits.
func Run(ctx context.Context, b Backend, root bool) error {
	_, err := tea.NewProgram(New(ctx, b, root), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}
