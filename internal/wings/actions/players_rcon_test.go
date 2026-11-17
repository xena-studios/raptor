package actions

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/rcon"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// fakeRCON is a Source game's RCON: password "pw", status lists one
// player, banid without a writeid after it is fine, and anything else is
// an unknown command.
func fakeRCON(t *testing.T) (string, func() []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	var mu sync.Mutex
	var got []string
	write := func(w io.Writer, id, typ int32, body string) {
		var b bytes.Buffer
		_ = binary.Write(&b, binary.LittleEndian, int32(len(body)+10)) //nolint:gosec // small
		_ = binary.Write(&b, binary.LittleEndian, id)
		_ = binary.Write(&b, binary.LittleEndian, typ)
		b.WriteString(body + "\x00\x00")
		_, _ = w.Write(b.Bytes())
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				for {
					var size, id, typ int32
					if binary.Read(c, binary.LittleEndian, &size) != nil {
						return
					}
					_ = binary.Read(c, binary.LittleEndian, &id)
					_ = binary.Read(c, binary.LittleEndian, &typ)
					buf := make([]byte, size-8)
					if _, err := io.ReadFull(c, buf); err != nil {
						return
					}
					body := strings.TrimRight(string(buf), "\x00")
					if typ == 3 {
						if body != "pw" {
							id = -1
						}
						write(c, id, 2, "")
						continue
					}
					mu.Lock()
					got = append(got, body)
					mu.Unlock()
					switch f := strings.Fields(body)[0]; f {
					case "status":
						write(c, id, 0, "# userid name uniqueid connected ping loss state adr\n#  2 \"Gordon\" STEAM_1:0:42 01:00 50 0 active 203.0.113.1:27005\n#end\n")
					case "kickid", "banid", "writeid", "say":
						write(c, id, 0, "")
					default:
						write(c, id, 0, "Unknown command \""+f+"\"\n")
					}
				}
			}()
		}
	}()
	return l.Addr().String(), func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

func TestPlayersOverRCON(t *testing.T) {
	ctx := context.Background()
	addr, sent := fakeRCON(t)
	target := server.RCONTarget{Protocol: "source", Game: "source", Addr: addr, Password: "pw"}
	players, err := rconOnline(ctx, target)
	if err != nil || len(players) != 1 || players[0].ID != "STEAM_1:0:42" || players[0].Name != "Gordon" || players[0].Seconds != 60 {
		t.Fatalf("online: %+v, %v", players, err)
	}
	for _, o := range []struct{ op, id, msg string }{{"kick", "STEAM_1:0:42", ""}, {"ban", "2", ""}, {"say", "", "hello; quit"}} {
		if _, err := rconManage(ctx, target, rconOp(o.op, o.id, o.msg)); err != nil {
			t.Errorf("%s: %v", o.op, err)
		}
	}
	// The game not knowing a command is an error, not a silent success.
	if _, err := rconManage(ctx, target, rconOp("unban", "STEAM_1:0:42", "")); err == nil || !strings.Contains(err.Error(), "removeid") {
		t.Errorf("unknown command: %v", err)
	}
	// Nothing unchecked is sent.
	if _, err := rconManage(ctx, target, rconOp("kick", "2; quit", "")); err == nil {
		t.Error("kick 2; quit: sent")
	}
	want := []string{"status", "kickid STEAM_1:0:42", "banid 0 2 kick", "writeid", "say hello quit", "removeid STEAM_1:0:42"}
	if got := sent(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("sent %q\nwant %q", got, want)
	}
	target.Password = "wrong"
	if _, err := rconOnline(ctx, target); err == nil || !strings.Contains(err.Error(), "refused the RCON password") {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := rconOnline(ctx, server.RCONTarget{Protocol: "source"}); err == nil {
		t.Error("no command set: listed")
	}
}

func rconOp(op, id, msg string) rcon.Op { return rcon.Op{Op: op, ID: id, Message: msg} }
