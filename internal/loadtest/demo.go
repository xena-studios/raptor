package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// Demo servers make a fleet look like people's own, for screenshots of the
// web and phone apps (docs/RELIABILITY.md#load-tests): real games' names,
// states that stay put, believable use and players (server.metrics and
// node.metrics), and a console that says what that game would. One crashes
// soon after linking, so an alert rule has something to show.

// DemoServer is one of a demo node's servers.
type DemoServer struct {
	Name  string
	Egg   string
	State string
	// Typical use: CPU as percent of one core, memory in GiB, players
	// online (0: the game doesn't answer player queries) and its cap.
	CPU, MemoryGiB    float64
	Players, MaxSlots int
	// Crash: it crashes about half a minute after the node links.
	// Installed: its install finishes then.
	Crash, Installed bool
}

// DemoFleet is what each demo node runs, by its place in the fleet.
func DemoFleet(node int) []DemoServer {
	fleets := [][]DemoServer{
		{
			{Name: "Survival SMP", Egg: "Paper", State: "running", CPU: 86, MemoryGiB: 5.6, Players: 14, MaxSlots: 40},
			{Name: "Rust Weekly", Egg: "Rust", State: "running", CPU: 142, MemoryGiB: 9.2, Players: 41, MaxSlots: 100},
			{Name: "Creative", Egg: "Paper", State: "running", CPU: 21, MemoryGiB: 2.4, Players: 3, MaxSlots: 20},
			{Name: "Modded ATM10", Egg: "NeoForge", State: "starting", CPU: 180, MemoryGiB: 7.1, MaxSlots: 12, Installed: true},
		},
		{
			{Name: "Valheim", Egg: "Valheim", State: "running", CPU: 64, MemoryGiB: 3.8, Players: 6, MaxSlots: 10},
			{Name: "Factorio", Egg: "Factorio", State: "running", CPU: 38, MemoryGiB: 1.9, Players: 2, MaxSlots: 16, Crash: true},
			{Name: "Palworld", Egg: "Palworld", State: "offline", CPU: 0, MemoryGiB: 0, MaxSlots: 32},
		},
	}
	return fleets[node%len(fleets)]
}

// demo is a node's demo state, by server ID.
type demo struct {
	byID    map[string]DemoServer
	crashed bool
}

func newDemo(servers []*nodev1.Server, list []DemoServer) *demo {
	d := &demo{byID: map[string]DemoServer{}}
	for i, s := range servers {
		d.byID[s.GetId()] = list[i]
	}
	return d
}

// wave is a value that drifts around typical over the hour, the same for
// the same server and minute, so refreshes agree.
func wave(seed string, minute int64, typical float64) float64 {
	var h float64
	for _, c := range seed {
		h += float64(c)
	}
	v := typical * (1 + 0.18*math.Sin(float64(minute)/7+h) + 0.07*math.Sin(float64(minute)/2.3+h*3))
	return math.Max(v, 0)
}

func (n *Node) demoMetrics(serverID string) (any, error) {
	d, ok := n.demo.byID[serverID]
	if !ok {
		return nil, errors.New("no such server")
	}
	running := n.demoState(serverID) == "running" || n.demoState(serverID) == "starting"
	now := time.Now().Truncate(time.Minute)
	point := func(at time.Time, res int) map[string]any {
		m := at.Unix() / 60
		p := map[string]any{"at": at.UnixMilli(), "resolution": res, "running": running}
		if !running {
			return p
		}
		cpu := wave(d.Name, m, d.CPU)
		mem := int64(wave(d.Name+"m", m, d.MemoryGiB) * (1 << 30))
		p["cpu"], p["cpu_max"] = cpu, cpu*1.3
		p["memory"], p["memory_max"] = mem, int64(float64(mem)*1.05)
		p["rx"], p["tx"] = int64(wave(d.Name+"r", m, 90_000)), int64(wave(d.Name+"t", m, 240_000))
		p["disk"] = int64(d.MemoryGiB * 2.6 * (1 << 30))
		if d.MaxSlots > 0 && d.State == "running" {
			players := math.Round(wave(d.Name+"p", m, float64(d.Players)))
			p["players"], p["players_max"] = players, int(players)
		}
		return p
	}
	points := make([]map[string]any, 0, 60)
	for i := 59; i >= 0; i-- {
		points = append(points, point(now.Add(-time.Duration(i)*time.Minute), 60))
	}
	return map[string]any{
		"range": "1h", "latest": point(time.Now(), 0), "points": points,
		"limits": map[string]int64{"memory_bytes": int64(math.Ceil(d.MemoryGiB*1.5+1)) << 30, "disk_bytes": 40 << 30, "cpu_percent": 400},
	}, nil
}

func (n *Node) demoNodeMetrics() any {
	uses := []map[string]any{}
	m := time.Now().Unix() / 60
	for id, d := range n.demo.byID {
		st := n.demoState(id)
		u := map[string]any{"id": id, "state": st}
		if st == "running" || st == "starting" {
			u["cpu"] = wave(d.Name, m, d.CPU)
			u["memory"] = int64(wave(d.Name+"m", m, d.MemoryGiB) * (1 << 30))
			if d.MaxSlots > 0 && st == "running" {
				u["players"] = int(math.Round(wave(d.Name+"p", m, float64(d.Players))))
			}
		}
		uses = append(uses, u)
	}
	return map[string]any{"range": "1h", "servers": uses, "totals": map[string]int64{"cpus": 8, "memory_bytes": 32 << 30, "disk_bytes": 240 << 30}}
}

func (n *Node) demoState(id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, s := range n.servers {
		if s.GetId() == id {
			return s.GetState()
		}
	}
	return ""
}

// demoEvents makes the demo's one-time events, once: the crash of the
// server meant to crash, with the console's last lines as Wings sends them,
// and the install of the one meant to finish installing. ok is whether
// there were any.
func (n *Node) demoEvents() (int64, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.demo.crashed {
		return n.seq, false
	}
	n.demo.crashed = true
	add := func(s *nodev1.Server, typ string, data any) {
		n.seq++
		e := &nodev1.Event{Seq: n.seq, ServerId: s.GetId(), At: time.Now().UnixMilli(), Type: typ, Version: s.GetVersion()}
		e.Data, _ = json.Marshal(data)
		n.events = append(n.events, e)
	}
	before := n.seq
	for _, s := range n.servers {
		d := n.demo.byID[s.GetId()]
		if d.Crash {
			s.State = "crashed"
			add(s, "server.state", map[string]any{"state": "crashed"})
			add(s, "server.crashed", map[string]any{"exit_code": 139, "reason": "error", "console": []string{
				"Saving map to file autosave3.zip",
				"Segmentation fault (core dumped)",
			}})
		}
		if d.Installed {
			add(s, "server.install.finished", map[string]any{})
		}
	}
	return n.seq, n.seq > before
}

func (s *service) demoExecute(req *nodev1.ExecuteRequest) (*nodev1.ExecuteResponse, error) {
	var e nodecmd.Envelope
	if err := json.Unmarshal(req.GetEnvelope(), &e); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var out any
	switch e.Action {
	case "server.metrics":
		var err error
		if out, err = s.n.demoMetrics(e.ServerID); err != nil {
			return &nodev1.ExecuteResponse{Error: err.Error()}, nil
		}
	case "node.metrics":
		out = s.n.demoNodeMetrics()
	default:
		return &nodev1.ExecuteResponse{Error: "this is a demo node: it doesn't run " + e.Action}, nil
	}
	b, _ := json.Marshal(out)
	return &nodev1.ExecuteResponse{Result: b}, nil
}

// demoConsole is a game's console: its history, then a line now and then.
func (s *service) demoConsole(ctx context.Context, req *nodev1.ConsoleRequest, stream *connect.ServerStream[nodev1.ConsoleResponse]) error {
	var e nodecmd.Envelope
	if err := json.Unmarshal(req.GetEnvelope(), &e); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	d, ok := s.n.demo.byID[e.ServerID]
	if !ok {
		return connect.NewError(connect.CodeNotFound, errors.New("no such server"))
	}
	history, later := consoleLines(d)
	if err := stream.Send(&nodev1.ConsoleResponse{Lines: history, History: true}); err != nil {
		return err
	}
	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(4+rand.IntN(5)) * time.Second): //nolint:gosec // a demo's pacing
		}
		line := later[i%len(later)]
		if d.Egg != "Rust" {
			// Minecraft stamps each line as it's printed.
			line = fmt.Sprintf("[%s INFO]: %s", time.Now().Format("15:04:05"), line)
		}
		if err := stream.Send(&nodev1.ConsoleResponse{Lines: []string{line}}); err != nil {
			return err
		}
	}
}

func consoleLines(d DemoServer) (history, later []string) {
	// Minutes and seconds ago, so the stamps look printed, not generated.
	stamp := func(m, sec int) string {
		return time.Now().Add(-time.Duration(m)*time.Minute - time.Duration(sec)*time.Second).Format("15:04:05")
	}
	switch d.Egg {
	case "Paper", "NeoForge":
		lines := []struct {
			m, s int
			text string
		}{
			{52, 41, "Starting minecraft server version 1.21.4"},
			{52, 38, "Preparing level \"world\""},
			{52, 30, "Done (8.412s)! For help, type \"help\""},
			{34, 12, "Maya_Builds joined the game"},
			{33, 47, "<Maya_Builds> anyone up for the end raid tonight?"},
			{32, 5, "<kestrel77> yes! after dinner"},
			{12, 19, "kestrel77 has made the advancement [Diamonds!]"},
			{5, 2, "Saved the game"},
		}
		for _, l := range lines {
			history = append(history, fmt.Sprintf("[%s INFO]: %s", stamp(l.m, l.s), l.text))
		}
		later = []string{
			"<kestrel77> who has spare iron?",
			"Tobi_R joined the game",
			"<Maya_Builds> chests by spawn",
			"Tobi_R has made the advancement [Stone Age]",
			"Saved the game",
		}
	case "Rust":
		history = []string{
			"Loading Prefab Bundle Content",
			"Server startup complete",
			"SteamServer Connected",
			"Saved 412,385 ents, cache(0.12), write(0.03), disk(0.01).",
			"Kicked: Raider[7656119] (Connection lost)",
		}
		later = []string{"Saving complete", "Raider[7656119] has entered the game", "Heli event started", "Saved 412,950 ents, cache(0.11), write(0.03), disk(0.01)."}
	default:
		history = []string{"Server started", "World loaded", "Listening for players"}
		later = []string{"Player connected", "World saved", "Player disconnected"}
	}
	return history, later
}

// change0 is the newest sequence number, changing nothing.
func (n *Node) change0() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.seq
}
