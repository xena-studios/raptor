package rcon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

// fakeSource is a Source RCON server: it logs in with "pw" as Source
// games do (an empty response, then the auth answer) and answers each
// command with answer(cmd), split into 10-byte packets to test joining.
func fakeSource(t *testing.T, answer func(string) string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				for {
					id, typ, body, err := readPacket(c)
					if err != nil {
						return
					}
					switch typ {
					case typeAuth:
						_ = writePacket(c, id, typeResponse, "")
						if body != "pw" {
							id = -1
						}
						_ = writePacket(c, id, typeAuthResponse, "")
					case typeExec:
						a := answer(body)
						for len(a) > 10 {
							_ = writePacket(c, id, typeResponse, a[:10])
							a = a[10:]
						}
						_ = writePacket(c, id, typeResponse, a)
					}
				}
			}()
		}
	}()
	return l.Addr().String()
}

func TestSource(t *testing.T) {
	ctx := context.Background()
	var got []string
	addr := fakeSource(t, func(cmd string) string {
		got = append(got, cmd)
		return "you said: " + cmd + " and that is a long answer"
	})
	c, err := Dial(ctx, "source", addr, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for _, cmd := range []string{"status", "say hi"} {
		out, err := c.Exec(ctx, cmd)
		if err != nil || out != "you said: "+cmd+" and that is a long answer" {
			t.Errorf("%s: %q, %v", cmd, out, err)
		}
	}
	if _, err := c.Exec(ctx, "say hi\nquit"); err == nil {
		t.Error("two lines: sent")
	}
	if !slices.Equal(got, []string{"status", "say hi"}) {
		t.Errorf("sent %q", got)
	}
	if _, err := Dial(ctx, "source", addr, "nope"); !errors.Is(err, ErrAuth) {
		t.Errorf("wrong password: %v", err)
	}
}

func TestWebRCON(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.CloseNow() }()
		for {
			_, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			var m webMessage
			_ = json.Unmarshal(data, &m)
			// A log line first, with another identifier.
			log, _ := json.Marshal(webMessage{Identifier: 0, Message: "someone joined"})
			_ = c.Write(r.Context(), websocket.MessageText, log)
			out, _ := json.Marshal(webMessage{Identifier: m.Identifier, Message: "ok " + m.Message})
			_ = c.Write(r.Context(), websocket.MessageText, out)
		}
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")
	c, err := Dial(ctx, "webrcon", addr, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if out, err := c.Exec(ctx, "playerlist"); err != nil || out != "ok playerlist" {
		t.Errorf("%q, %v", out, err)
	}
	if _, err := Dial(ctx, "webrcon", addr, "secret"); !errors.Is(err, ErrAuth) || strings.Contains(err.Error(), "secret") {
		t.Errorf("wrong password: %v", err)
	}
}

func TestParsePlayers(t *testing.T) {
	source := `hostname: My Server
version : 7929086/24 7929086 secure
# userid name                uniqueid            connected ping loss state  adr
#      2 "Some \"One\" Here" STEAM_1:0:12345     05:23      60    0 active 203.0.113.9:27005
#      3 "Bot"               BOT                 active
#      4 1 "Slot Game"       [U:1:999]           1:02:03    20    0 active 203.0.113.10:27005
#end`
	cs2 := `---------players--------
  id     time ping loss      state   rate adr name
65535 [NoChan]    0    0 challenging      0unknown ''
    2    01:02   20    0     active 786432 203.0.113.9:27005 'Player One'
#end`
	for _, c := range []struct {
		game, out string
		want      []Player
	}{
		{"source", source, []Player{
			{ID: "STEAM_1:0:12345", Name: `Some \"One\" Here`, Ping: 60, Seconds: 323},
			{ID: "3", Name: "Bot", Bot: true},
			{ID: "[U:1:999]", Name: "Slot Game", Ping: 20, Seconds: 3723},
		}},
		{"source", cs2, []Player{{ID: "2", Name: "Player One", Ping: 20, Seconds: 62}}},
		{"ark", "0. Some, Name, 76561198000000001\n1. Other, 0002abcdef0123456789abcdef012345\n", []Player{
			{ID: "76561198000000001", Name: "Some, Name"}, {ID: "0002abcdef0123456789abcdef012345", Name: "Other"},
		}},
		{"ark", "No Players Connected", []Player{}},
		{"palworld", "name,playeruid,steamid\nA, B,123,steam_76561198000000001\n", []Player{{ID: "steam_76561198000000001", Name: "A, B"}}},
		{"rust", `[{"SteamID":"76561198000000001","DisplayName":"Rusty","Ping":30,"ConnectedSeconds":61.5}]`, []Player{
			{ID: "76561198000000001", Name: "Rusty", Ping: 30, Seconds: 61},
		}},
	} {
		got, err := ParsePlayers(c.game, c.out)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s: %+v, %v; want %+v", c.game, got, err, c.want)
		}
	}
	if _, err := ParsePlayers("rust", "not json"); err == nil {
		t.Error("rust: bad json parsed")
	}
}

func TestCommands(t *testing.T) {
	for _, c := range []struct {
		game string
		op   Op
		want []string
	}{
		{"source", Op{Op: "kick", ID: "5", Reason: "be nice; quit"}, []string{"kickid 5 be nice quit"}},
		{"source", Op{Op: "ban", ID: "STEAM_1:0:12345"}, []string{"banid 0 STEAM_1:0:12345 kick", "writeid"}},
		{"source", Op{Op: "unban", ID: "[U:1:999]"}, []string{"removeid [U:1:999]", "writeid"}},
		{"source", Op{Op: "say", Message: "  hello\n\"all\"  "}, []string{"say hello all"}},
		{"ark", Op{Op: "ban", ID: "76561198000000001"}, []string{"BanPlayer 76561198000000001"}},
		{"ark", Op{Op: "say", Message: "restart soon"}, []string{"ServerChat restart soon"}},
		{"palworld", Op{Op: "unban", ID: "steam_76561198000000001"}, []string{"UnBanPlayer steam_76561198000000001"}},
		{"palworld", Op{Op: "say", Message: "hi"}, []string{"Broadcast hi"}},
		{"rust", Op{Op: "kick", ID: "76561198000000001", Reason: `"x" y`}, []string{`kick 76561198000000001 "x y"`}},
		{"rust", Op{Op: "ban", ID: "76561198000000001", Name: "Rusty"}, []string{`banid 76561198000000001 "Rusty" ""`, "server.writecfg"}},
		{"rust", Op{Op: "say", Message: "hi"}, []string{`say "hi"`}},
	} {
		got, err := Commands(c.game, c.op)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%s %+v: %q, %v; want %q", c.game, c.op, got, err, c.want)
		}
	}
	for _, c := range []struct {
		game string
		op   Op
	}{
		{"source", Op{Op: "kick", ID: "5; quit"}},
		{"source", Op{Op: "kick", ID: "STEAM_1:0:1 quit"}},
		{"source", Op{Op: "unban", ID: "5"}}, // a user ID doesn't last
		{"source", Op{Op: "say", Message: " ;\" "}},
		{"source", Op{Op: "rcon_password", ID: "5"}},
		{"ark", Op{Op: "kick", ID: "123"}},
		{"palworld", Op{Op: "kick", ID: "steam 1"}},
		{"rust", Op{Op: "ban", ID: "7656119800000000x"}},
		{"other", Op{Op: "kick", ID: "1"}},
	} {
		if got, err := Commands(c.game, c.op); err == nil {
			t.Errorf("%s %+v: allowed as %q", c.game, c.op, got)
		}
	}
	if r := Text(strings.Repeat("é", 150), 100); len([]rune(r)) != 100 {
		t.Errorf("text not cut: %d", len([]rune(r)))
	}
	if !Refused(`Unknown command "banid"`) || Refused("Kicked Bob") {
		t.Error("refused")
	}
}
