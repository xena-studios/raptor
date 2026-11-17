package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/rcon"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Player actions (#422): a Minecraft Java server's whitelist, operators,
// and bans, under their own permission, so a moderator can manage players
// without the console or the files. A running server is told by console
// command (it would overwrite an edited file); a stopped one has its
// list files edited as the game writes them. Nothing a user sends reaches
// the console except a checked name, IP, level, or one-line reason.
//
// Games whose egg declares RCON player commands (#423, x-raptor.rcon) get
// players.online and players.manage's kick, ban, unban, and say over RCON
// instead (players_rcon.go), under the same rule.
const (
	PlayersLists  = "players.lists"  // a read: the list files
	PlayersManage = "players.manage" // whitelist, op, ban, kick, and their undoing
	PlayersOnline = "players.online" // a read: who's online, from the game's ping
)

// playerFiles are the files players.lists reads, by key.
var playerFiles = map[string]string{
	"whitelist": "whitelist.json",
	"ops":       "ops.json",
	"bans":      "banned-players.json",
	"ip_bans":   "banned-ips.json",
	"seen":      "usercache.json",
}

// Online is players.online's answer. Names can be fewer than Online:
// Minecraft's ping lists at most 12, and servers can hide them.
type Online struct {
	Running bool     `json:"running"`
	Online  int      `json:"online"`
	Max     int      `json:"max"`
	Names   []string `json:"names"`
}

// PlayersParams are players.manage's params.
type PlayersParams struct {
	// Op is whitelist.add, whitelist.remove, op, deop, ban, pardon,
	// ban-ip, pardon-ip, or kick.
	Op   string `json:"op"`
	Name string `json:"name,omitempty"`
	// UUID is the player's, for a stopped server's files (the Panel looks
	// it up).
	UUID   string `json:"uuid,omitempty"`
	IP     string `json:"ip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

var (
	playerName = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
	playerUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	playerIPv4 = regexp.MustCompile(`^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$`)
	playerIPv6 = regexp.MustCompile(`^[0-9a-fA-F:]{2,39}$`)
)

// errNotMinecraft is players.* on a server that isn't Minecraft Java.
var errNotMinecraft = errors.New("players are managed on Minecraft Java servers only")

// reasonLine is a reason as one plain line of at most 100 characters.
func reasonLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return s
}

// playersCommand is the console command for an op, its arguments checked.
func playersCommand(p PlayersParams) (string, error) {
	switch p.Op {
	case "ban-ip", "pardon-ip":
		if !playerIPv4.MatchString(p.IP) && (!playerIPv6.MatchString(p.IP) || !strings.Contains(p.IP, ":")) {
			return "", errors.New("that isn't an IP address")
		}
	default:
		if !playerName.MatchString(p.Name) {
			return "", errors.New("a Minecraft name is 1 to 16 letters, digits, and underscores")
		}
	}
	withReason := func(cmd string) string {
		if r := reasonLine(p.Reason); r != "" {
			return cmd + " " + r
		}
		return cmd
	}
	switch p.Op {
	case "whitelist.add":
		return "whitelist add " + p.Name, nil
	case "whitelist.remove":
		return "whitelist remove " + p.Name, nil
	case "op", "deop", "pardon", "kick":
		return p.Op + " " + p.Name, nil
	case "ban":
		return withReason("ban " + p.Name), nil
	case "ban-ip":
		return withReason("ban-ip " + p.IP), nil
	case "pardon-ip":
		return "pardon-ip " + p.IP, nil
	}
	return "", fmt.Errorf("unknown op %q", p.Op)
}

// mcTime is a time as Minecraft writes it in ban lists.
func mcTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 -0700") }

// editPlayers changes a stopped server's list file for an op.
func editPlayers(ctx context.Context, fsvc *files.Service, id, user string, p PlayersParams) error {
	if p.Op == "kick" {
		return errors.New("the server isn't running, so nobody is online to kick")
	}
	if strings.HasSuffix(p.Op, "-ip") {
		return editList(ctx, fsvc, id, "banned-ips.json", "ip", p.IP, func() map[string]any {
			if p.Op == "pardon-ip" {
				return nil
			}
			return map[string]any{"ip": p.IP, "created": mcTime(time.Now()), "source": "Raptor (" + user + ")", "expires": "forever", "reason": orDefault(reasonLine(p.Reason), "Banned by an operator.")}
		})
	}
	adding := p.Op == "whitelist.add" || p.Op == "op" || p.Op == "ban"
	if adding && !playerUUID.MatchString(p.UUID) {
		return errors.New("a stopped server needs the player's UUID")
	}
	file := map[string]string{
		"whitelist.add": "whitelist.json", "whitelist.remove": "whitelist.json",
		"op": "ops.json", "deop": "ops.json", "ban": "banned-players.json", "pardon": "banned-players.json",
	}[p.Op]
	return editList(ctx, fsvc, id, file, "name", p.Name, func() map[string]any {
		switch p.Op {
		case "whitelist.add":
			return map[string]any{"uuid": p.UUID, "name": p.Name}
		case "op":
			return map[string]any{"uuid": p.UUID, "name": p.Name, "level": 4, "bypassesPlayerLimit": false}
		case "ban":
			return map[string]any{"uuid": p.UUID, "name": p.Name, "created": mcTime(time.Now()), "source": "Raptor (" + user + ")", "expires": "forever", "reason": orDefault(reasonLine(p.Reason), "Banned by an operator.")}
		}
		return nil
	})
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// editList rewrites a list file without the entries whose key matches
// value (case-insensitively), then with add's entry if it gives one.
func editList(ctx context.Context, fsvc *files.Service, id, file, key, value string, add func() map[string]any) error {
	var list []map[string]any
	c, err := fsvc.Read(ctx, id, file)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case strings.TrimSpace(string(c.Data)) != "":
		if err := json.Unmarshal(c.Data, &list); err != nil {
			return fmt.Errorf("%s isn't a list Minecraft wrote: %w", file, err)
		}
	}
	list = slices.DeleteFunc(list, func(e map[string]any) bool {
		v, _ := e[key].(string)
		return strings.EqualFold(v, value)
	})
	if e := add(); e != nil {
		list = append(list, e)
	}
	if list == nil {
		list = []map[string]any{}
	}
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return fsvc.Write(ctx, id, file, append(out, '\n'))
}

// RegisterPlayers adds the player actions. None is signed: they do only
// what the console could, on a Minecraft Java server or over a game's RCON
// (players.online, and players.manage's kick, ban, unban, and say).
func RegisterPlayers(x *command.Executor, m *server.Manager, fsvc *files.Service) {
	minecraft := func(ctx context.Context, e command.Envelope) error {
		if e.ServerID == "" {
			return errors.New("command needs a server_id")
		}
		egg, err := m.Egg(ctx, e.ServerID)
		if err != nil {
			return err
		}
		if egg.Raptor.Players.Query != "minecraft" {
			return errNotMinecraft
		}
		for _, a := range egg.Raptor.Addons {
			if len(a.Loaders) > 0 && slices.Contains([]string{"velocity", "bungeecord", "waterfall"}, a.Loaders[0]) {
				return errNotMinecraft
			}
		}
		return nil
	}
	x.Register(PlayersLists, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if err := minecraft(ctx, e); err != nil {
			return nil, err
		}
		out := map[string]json.RawMessage{}
		for k, file := range playerFiles {
			c, err := fsvc.Read(ctx, e.ServerID, file)
			if err != nil || !json.Valid(c.Data) || !strings.HasPrefix(strings.TrimSpace(string(c.Data)), "[") {
				out[k] = json.RawMessage("[]")
				continue
			}
			out[k] = c.Data
		}
		// The two settings the lists depend on: offline-mode servers take
		// offline UUIDs, and the whitelist only counts when it's on.
		props := map[string]string{}
		if c, err := fsvc.Read(ctx, e.ServerID, "server.properties"); err == nil {
			for _, l := range strings.Split(string(c.Data), "\n") {
				if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok && (k == "online-mode" || k == "white-list") {
					props[k] = strings.TrimSpace(v)
				}
			}
		}
		out["properties"], _ = json.Marshal(props)
		return out, nil
	}})
	x.Register(PlayersOnline, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		// A game with RCON player commands (#423): who's on, over RCON.
		if egg, err := m.Egg(ctx, e.ServerID); err == nil && egg.Raptor.RCON != nil && egg.Raptor.RCON.Players != "" {
			t, err := m.RCON(ctx, e.ServerID)
			if err != nil {
				return nil, err
			}
			players, err := rconOnline(ctx, t)
			if err != nil {
				return nil, err
			}
			return map[string]any{"players": players}, nil
		}
		// Minecraft: the count and names from its ping (#422).
		if err := minecraft(ctx, e); err != nil {
			return nil, err
		}
		s, err := m.Sample(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		if s.State != server.Running || s.Query != "minecraft" || s.QueryAddr == "" {
			return Online{}, nil
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		p, err := metrics.QueryPlayers(ctx, s.Query, s.QueryAddr)
		if err != nil {
			return nil, fmt.Errorf("the server didn't answer its ping: %w", err)
		}
		return Online{Running: true, Online: p.Online, Max: p.Max, Names: p.Names}, nil
	}})
	x.Register(PlayersManage, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		// A game with RCON player commands (#423) is told over RCON.
		if egg, err := m.Egg(ctx, e.ServerID); err == nil && egg.Raptor.RCON != nil && egg.Raptor.RCON.Players != "" {
			var o rcon.Op
			if err := decode(e, &o); err != nil {
				return nil, err
			}
			t, err := m.RCON(ctx, e.ServerID)
			if err != nil {
				return nil, err
			}
			reply, err := rconManage(ctx, t, o)
			if err != nil {
				return nil, err
			}
			return map[string]any{"reply": reply}, nil
		}
		if err := minecraft(ctx, e); err != nil {
			return nil, err
		}
		var p PlayersParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		cmd, err := playersCommand(p)
		if err != nil {
			return nil, err
		}
		st, err := m.Status(e.ServerID)
		if err != nil {
			return nil, err
		}
		if st.State != server.Offline && st.State != server.Crashed {
			return nil, m.SendCommand(e.ServerID, e.UserID, cmd)
		}
		return nil, editPlayers(ctx, fsvc, e.ServerID, e.UserID, p)
	}})
}
