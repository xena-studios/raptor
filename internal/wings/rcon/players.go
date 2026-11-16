package rcon

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Player is someone on the server, as the game listed them.
type Player struct {
	// ID is what kick and ban take: a Steam ID (or the game's own ID),
	// or a Source game's user ID when it shows no Steam ID.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Ping and Seconds (connected) are 0 when the game doesn't say.
	Ping    int  `json:"ping,omitempty"`
	Seconds int  `json:"seconds,omitempty"`
	Bot     bool `json:"bot,omitempty"`
}

// Games are the Players tab's command sets (the egg's x-raptor.rcon.players).
var Games = []string{"source", "ark", "palworld", "rust"}

// ListCommand is the command that lists who's on.
func ListCommand(game string) string {
	return map[string]string{"source": "status", "ark": "ListPlayers", "palworld": "ShowPlayers", "rust": "playerlist"}[game]
}

// What a game's IDs look like. Nothing else is sent, so a command can't
// be stretched: no spaces, quotes, or separators.
var (
	sourceUserID  = regexp.MustCompile(`^\d{1,5}$`)
	sourceSteamID = regexp.MustCompile(`^(STEAM_[0-5]:[01]:\d{1,10}|\[U:1:\d{1,10}\])$`)
	steamID64     = regexp.MustCompile(`^\d{17}$`)
	// ARK: Steam IDs; Ascended: Epic's 32-hex IDs.
	arkID = regexp.MustCompile(`^(\d{17}|[0-9a-fA-F]{32})$`)
	// Palworld: Steam IDs, bare or as steam_<id>, and other platforms'.
	palworldID = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// ValidID reports whether id is one of the game's player IDs. unban needs
// a lasting one (a Source game's user ID is only for this connection).
func ValidID(game, id string, lasting bool) bool {
	switch game {
	case "source":
		return sourceSteamID.MatchString(id) || (!lasting && sourceUserID.MatchString(id))
	case "ark":
		return arkID.MatchString(id)
	case "palworld":
		return palworldID.MatchString(id)
	case "rust":
		return steamID64.MatchString(id)
	}
	return false
}

// Text is a reason or message as one plain line of at most n characters:
// no control characters, quotes, backslashes, or semicolons (which
// Source games and Rust read as the end of a command).
func Text(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return ' '
		case r == '"' || r == '\\' || r == ';':
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = strings.TrimSpace(string(r[:n]))
	}
	return s
}

// Op is a change to a player: kick, ban, unban, or say (a message to
// everyone, which needs no ID).
type Op struct {
	Op     string `json:"op"`
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Message is say's text.
	Message string `json:"message,omitempty"`
}

// Commands are the commands for an op, from the game's templates, its
// arguments checked.
func Commands(game string, o Op) ([]string, error) {
	switch o.Op {
	case "kick", "ban", "unban":
		if !ValidID(game, o.ID, o.Op == "unban") {
			if o.Op == "unban" && game == "source" {
				return nil, errors.New("unban takes a Steam ID, like STEAM_1:0:123 or [U:1:123]")
			}
			return nil, errors.New("that isn't one of this game's player IDs")
		}
	case "say":
		if Text(o.Message, 120) == "" {
			return nil, errors.New("write a message")
		}
	default:
		return nil, fmt.Errorf("unknown op %q", o.Op)
	}
	reason, msg := Text(o.Reason, 100), Text(o.Message, 120)
	with := func(cmd, arg string) string {
		if arg != "" {
			return cmd + " " + arg
		}
		return cmd
	}
	switch game + "/" + o.Op {
	case "source/kick":
		return []string{with("kickid "+o.ID, reason)}, nil
	case "source/ban":
		// Permanent, kicked now, and written to banned_user.cfg so it
		// lasts a restart.
		return []string{"banid 0 " + o.ID + " kick", "writeid"}, nil
	case "source/unban":
		return []string{"removeid " + o.ID, "writeid"}, nil
	case "source/say":
		return []string{"say " + msg}, nil
	case "ark/kick", "palworld/kick":
		return []string{"KickPlayer " + o.ID}, nil
	case "ark/ban", "palworld/ban":
		return []string{"BanPlayer " + o.ID}, nil
	case "ark/unban":
		return []string{"UnbanPlayer " + o.ID}, nil
	case "palworld/unban":
		return []string{"UnBanPlayer " + o.ID}, nil
	case "ark/say":
		return []string{"ServerChat " + msg}, nil
	case "palworld/say":
		return []string{"Broadcast " + msg}, nil
	case "rust/kick":
		return []string{`kick ` + o.ID + ` "` + reason + `"`}, nil
	case "rust/ban":
		return []string{`banid ` + o.ID + ` "` + Text(o.Name, 32) + `" "` + reason + `"`, "server.writecfg"}, nil
	case "rust/unban":
		return []string{"unban " + o.ID, "server.writecfg"}, nil
	case "rust/say":
		return []string{`say "` + msg + `"`}, nil
	}
	return nil, fmt.Errorf("unknown game %q", game)
}

// Refused reports a game's answer that says it didn't take a command.
func Refused(reply string) bool {
	l := strings.ToLower(reply)
	return strings.Contains(l, "unknown command") || strings.Contains(l, "command not found")
}

// ParsePlayers reads the list command's answer.
func ParsePlayers(game, out string) ([]Player, error) {
	players := []Player{}
	switch game {
	case "source":
		return parseSource(out), nil
	case "ark":
		// "0. Name, 76561198000000000" a line, or "No Players Connected".
		for _, l := range lines(out) {
			if m := arkLine.FindStringSubmatch(l); m != nil {
				players = append(players, Player{ID: m[2], Name: m[1]})
			}
		}
		return players, nil
	case "palworld":
		// CSV under a "name,playeruid,steamid" header; names can hold
		// commas, so the IDs are the last two fields.
		for _, l := range lines(out) {
			i := strings.LastIndexByte(l, ',')
			if i < 0 {
				continue
			}
			j := strings.LastIndexByte(l[:i], ',')
			if j < 0 || strings.EqualFold(l, "name,playeruid,steamid") {
				continue
			}
			players = append(players, Player{ID: strings.TrimSpace(l[i+1:]), Name: l[:j]})
		}
		return players, nil
	case "rust":
		var list []struct {
			SteamID          string  `json:"SteamID"`
			DisplayName      string  `json:"DisplayName"`
			Ping             int     `json:"Ping"`
			ConnectedSeconds float64 `json:"ConnectedSeconds"`
		}
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			return nil, fmt.Errorf("rust's player list: %w", err)
		}
		for _, p := range list {
			players = append(players, Player{ID: p.SteamID, Name: p.DisplayName, Ping: p.Ping, Seconds: int(p.ConnectedSeconds)})
		}
		return players, nil
	}
	return nil, fmt.Errorf("unknown game %q", game)
}

var (
	arkLine = regexp.MustCompile(`^\d+\.\s+(.*),\s*([0-9a-fA-F]{17,32})$`)
	// Source 1: # userid [slot] "name" uniqueid [connected ping loss state rate adr]
	sourceLine = regexp.MustCompile(`^#\s*(\d+)\s+(?:\d+\s+)?"(.*)"\s+(\S+)(?:\s+([\d:]+)\s+(\d+))?`)
	// CS2: id time ping loss state rate adr 'name'
	cs2Line = regexp.MustCompile(`^(\d+)\s+(\S+)\s+(\d+)\s+(\d+)\s+(\w+)\s+\d+\s*\S*\s+'(.*)'$`)
)

func parseSource(out string) []Player {
	players := []Player{}
	for _, l := range lines(out) {
		if m := sourceLine.FindStringSubmatch(l); m != nil {
			p := Player{ID: m[1], Name: m[2], Bot: m[3] == "BOT"}
			if sourceSteamID.MatchString(m[3]) {
				p.ID = m[3]
			}
			p.Seconds = clock(m[4])
			p.Ping, _ = strconv.Atoi(m[5])
			players = append(players, p)
			continue
		}
		if m := cs2Line.FindStringSubmatch(l); m != nil {
			// 65535 is a connection that isn't a player yet.
			if m[1] == "65535" || m[5] == "challenging" {
				continue
			}
			p := Player{ID: m[1], Name: m[6], Bot: m[2] == "BOT"}
			p.Seconds = clock(m[2])
			p.Ping, _ = strconv.Atoi(m[3])
			players = append(players, p)
		}
	}
	return players
}

// clock is "MM:SS" or "HH:MM:SS" in seconds; 0 if it isn't one.
func clock(s string) int {
	if s == "" {
		return 0
	}
	t := 0
	for f := range strings.SplitSeq(s, ":") {
		n, err := strconv.Atoi(f)
		if err != nil {
			return 0
		}
		t = t*60 + n
	}
	return t
}

func lines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
