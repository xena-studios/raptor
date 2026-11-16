package actions

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/store"
)

func TestPlayersCommand(t *testing.T) {
	for _, c := range []struct {
		p    PlayersParams
		want string
	}{
		{PlayersParams{Op: "whitelist.add", Name: "Steve"}, "whitelist add Steve"},
		{PlayersParams{Op: "whitelist.remove", Name: "Steve"}, "whitelist remove Steve"},
		{PlayersParams{Op: "op", Name: "Alex_2"}, "op Alex_2"},
		{PlayersParams{Op: "kick", Name: "Alex_2"}, "kick Alex_2"},
		{PlayersParams{Op: "ban", Name: "Grief", Reason: "  griefing\nspawn  \x1b"}, "ban Grief griefing spawn"},
		{PlayersParams{Op: "ban-ip", IP: "203.0.113.9"}, "ban-ip 203.0.113.9"},
		{PlayersParams{Op: "pardon-ip", IP: "2001:db8::1"}, "pardon-ip 2001:db8::1"},
	} {
		got, err := playersCommand(c.p)
		if err != nil || got != c.want {
			t.Errorf("%+v: %q, %v; want %q", c.p, got, err, c.want)
		}
	}
	for _, p := range []PlayersParams{
		{Op: "op", Name: "Steve; stop"},
		{Op: "op", Name: "Steve\nstop"},
		{Op: "ban", Name: ""},
		{Op: "ban", Name: strings.Repeat("a", 17)},
		{Op: "ban-ip", IP: "1.2.3.4 stop"},
		{Op: "ban-ip", IP: "999.1.1.1"},
		{Op: "stop", Name: "Steve"},
	} {
		if got, err := playersCommand(p); err == nil {
			t.Errorf("%+v: allowed as %q", p, got)
		}
	}
	if r := reasonLine(strings.Repeat("é", 150)); len([]rune(r)) != 100 {
		t.Errorf("reason not cut to 100: %d", len([]rune(r)))
	}
}

// A stopped server's lists are edited as Minecraft writes them.
func TestEditPlayers(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "server")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	eng := jobs.New(jobs.Options{Store: db, LogDir: filepath.Join(root, "jobs"), Poll: 10 * time.Millisecond})
	svc := files.NewService(files.Options{Servers: fileServers{dir}, Store: db, Jobs: eng, UID: os.Getuid(), GID: os.Getgid()})
	const uuid1 = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	if err := os.WriteFile(filepath.Join(dir, "whitelist.json"), []byte(`[{"uuid":"x","name":"Old"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	read := func(name string) []map[string]any {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	edit := func(p PlayersParams) error { return editPlayers(ctx, svc, filesServer, "alice", p) }
	if err := edit(PlayersParams{Op: "whitelist.add", Name: "Notch", UUID: uuid1}); err != nil {
		t.Fatal(err)
	}
	if l := read("whitelist.json"); len(l) != 2 || l[1]["name"] != "Notch" || l[1]["uuid"] != uuid1 {
		t.Errorf("whitelist: %v", l)
	}
	if err := edit(PlayersParams{Op: "whitelist.remove", Name: "old"}); err != nil {
		t.Fatal(err)
	}
	if l := read("whitelist.json"); len(l) != 1 || l[0]["name"] != "Notch" {
		t.Errorf("whitelist after removing: %v", l)
	}
	if err := edit(PlayersParams{Op: "ban", Name: "Notch", UUID: uuid1, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	if l := read("banned-players.json"); len(l) != 1 || l[0]["reason"] != "x" || l[0]["expires"] != "forever" || l[0]["source"] != "Raptor (alice)" {
		t.Errorf("bans: %v", l)
	}
	if err := edit(PlayersParams{Op: "ban-ip", IP: "203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	if err := edit(PlayersParams{Op: "pardon-ip", IP: "203.0.113.9"}); err != nil {
		t.Fatal(err)
	}
	if l := read("banned-ips.json"); len(l) != 0 {
		t.Errorf("ip bans: %v", l)
	}
	if err := edit(PlayersParams{Op: "op", Name: "Notch"}); err == nil {
		t.Error("an op without a UUID on a stopped server")
	}
	if err := edit(PlayersParams{Op: "kick", Name: "Notch"}); err == nil {
		t.Error("a kick on a stopped server")
	}
	if err := os.WriteFile(filepath.Join(dir, "ops.json"), []byte(`{"not":"a list"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := edit(PlayersParams{Op: "op", Name: "Notch", UUID: uuid1}); err == nil {
		t.Error("overwrote an ops.json that isn't a list")
	}
}
