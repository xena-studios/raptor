package configfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
)

func edit(t *testing.T, parser, in string, rules ...Rule) string {
	t.Helper()
	out, err := Edit(parser, []byte(in), rules)
	if err != nil {
		t.Fatalf("%s: %v", parser, err)
	}
	return string(out)
}

func TestProperties(t *testing.T) {
	in := "#Minecraft server properties\n#Thu Sep 25\nserver-ip=\nserver-port = 25565\nmotd=A Minecraft Server\nlong=first \\\n    second\nquery.port=25565\n"
	got := edit(t, "properties", in,
		Rule{Key: "server-ip", Value: "0.0.0.0"},
		Rule{Key: "server-port", Value: "25570"},
		Rule{Key: "motd", Value: "Héllo \\ world"},
		Rule{Key: "long", Value: "short"},
		Rule{Key: "enable-query", Value: true},
		Rule{Key: "query.port", IfValue: "1", Value: "9"}, // condition doesn't hold
	)
	want := "#Minecraft server properties\n#Thu Sep 25\nserver-ip=0.0.0.0\nserver-port = 25570\nmotd=H\\u00E9llo \\\\ world\nlong=short\nquery.port=25565\nenable-query=true\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// The result reads back to the intended values.
	for _, e := range propertyEntries(strings.Split(got, "\n")) {
		if e.key == "motd" && e.value != "Héllo \\ world" {
			t.Errorf("motd reads back as %q", e.value)
		}
	}
	if got := edit(t, "properties", "", Rule{Key: "a", Value: "b"}); got != "a=b\n" {
		t.Errorf("new file: %q", got)
	}
	if got := edit(t, "properties", "a=1\r\n", Rule{Key: "a", Value: "2"}); got != "a=2\r\n" {
		t.Errorf("CRLF: %q", got)
	}
}

func TestText(t *testing.T) {
	in := "server.hostname \"default\"\r\nserver.worldsize 4500\r\nserver.seed 12345\r\n"
	got := edit(t, "file", in,
		Rule{Key: "server.hostname ", Value: `server.hostname "My Rust"`},
		Rule{Key: "server.worldsize ", Value: "server.worldsize 1000"},
	)
	want := "server.hostname \"My Rust\"\r\nserver.worldsize 1000\r\nserver.seed 12345\r\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestINI(t *testing.T) {
	in := "; comment\nlogfile=old.log\n\n[/Script/Engine.GameSession]\nMaxPlayers=10\n\n[ServerSettings]\n+Admin=a\n+Admin=b\nServerName = \"Old\"\n\n[SystemSettings]\n"
	got := edit(t, "ini", in,
		Rule{Key: "[/Script/Engine.GameSession].MaxPlayers", Value: "32"},
		Rule{Key: "ServerSettings.ServerName", Value: "New Name"},
		Rule{Key: "ServerSettings.Port", Value: "7777"},
		Rule{Key: "[SystemSettings].net.AllowEncryption", Value: "False"},
		Rule{Key: "Host.port", Value: "1"},
		Rule{Key: "logfile", Value: "server.log"},
		Rule{Key: "database", Value: "db.sqlite"},
	)
	want := "; comment\nlogfile=server.log\ndatabase=db.sqlite\n\n[/Script/Engine.GameSession]\nMaxPlayers=32\n\n[ServerSettings]\n+Admin=a\n+Admin=b\nServerName = \"New Name\"\nPort=7777\n\n[SystemSettings]\nnet.AllowEncryption=False\n\n[Host]\nport=1\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if s, k := iniPath("GameSettings/General.MODSelect"); s != "GameSettings/General" || k != "MODSelect" {
		t.Errorf("iniPath = %q, %q", s, k)
	}
}

const bungee = `# BungeeCord config
listeners:
- query_port: 25577
  host: 0.0.0.0:25577 # bind
  max_players: 1
servers:
  lobby:
    motd: Lobby
    address: localhost:25565
  games:
    address: 10.0.0.5:25566
  hub:
    address: 127.0.0.1
ip_forward: false
`

func TestYAML(t *testing.T) {
	got := edit(t, "yaml", bungee,
		Rule{Key: "listeners[0].host", Value: "0.0.0.0:25600"},
		Rule{Key: "listeners[0].query_port", Value: "25600"},
		Rule{Key: "servers.*.address", IfValue: `regex:^(127\.0\.0\.1|localhost)(:\d{1,5})?$`, Value: "172.29.0.1$2"},
		Rule{Key: "servers.*.address", IfValue: "127.0.0.1", Value: "172.29.0.1"},
		Rule{Key: "ip_forward", Value: true},
		Rule{Key: "new.nested.key", Value: "x"},
	)
	for _, want := range []string{
		"# BungeeCord config",
		"host: 0.0.0.0:25600 # bind",
		"query_port: 25600\n",
		"address: 172.29.0.1:25565",
		"address: 10.0.0.5:25566",
		"address: 172.29.0.1\n",
		"ip_forward: true",
		"new:\n  nested:\n    key: x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "listeners") > strings.Index(got, "servers") {
		t.Error("key order changed")
	}
	if _, err := Edit("yaml", []byte("a: 1\n---\nb: 2\n"), nil); err == nil {
		t.Error("multi-document YAML should be refused")
	}
}

func TestJSON(t *testing.T) {
	in := "{\n  \"game\": {\n    \"port\": 1,\n    \"gameProperties\": {\"fastValidation\": false}\n  },\n  \"url\": \"a<b>&c\",\n  \"big\": 12345678901234567890\n}\n"
	got := edit(t, "json", in,
		Rule{Key: "game.port", Value: "7777"},
		Rule{Key: "game.gameProperties.fastValidation", Value: true},
		Rule{Key: "game.name", Value: "My Server"},
	)
	want := "{\n  \"game\": {\n    \"port\": 7777,\n    \"gameProperties\": {\n      \"fastValidation\": true\n    },\n    \"name\": \"My Server\"\n  },\n  \"url\": \"a<b>&c\",\n  \"big\": 12345678901234567890\n}\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := edit(t, "json", "", Rule{Key: "a.b", Value: "1"}); got != "{\n    \"a\": {\n        \"b\": 1\n    }\n}\n" {
		t.Errorf("new file: %q", got)
	}
	if _, err := Edit("json", []byte("{bad"), nil); err == nil {
		t.Error("invalid JSON should fail")
	}
}

const spaceEngineers = `<?xml version="1.0"?>
<MyConfigDedicated xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <!-- settings -->
  <SessionSettings>
    <MaxPlayers>4</MaxPlayers>
  </SessionSettings>
  <ServerPort>27016</ServerPort>
  <ServerName>Old &amp; busted</ServerName>
</MyConfigDedicated>
`

func TestXML(t *testing.T) {
	got := edit(t, "xml", spaceEngineers,
		Rule{Key: "MyConfigDedicated.SessionSettings.MaxPlayers", Value: "16"},
		Rule{Key: "MyConfigDedicated.ServerPort", Value: "27020"},
		Rule{Key: "MyConfigDedicated.ServerName", Value: "New & <shiny>"},
		Rule{Key: "MyConfigDedicated/WorldName", Value: "Star System"},
	)
	want := `<?xml version="1.0"?>
<MyConfigDedicated xmlns:xsd="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <!-- settings -->
  <SessionSettings>
    <MaxPlayers>16</MaxPlayers>
  </SessionSettings>
  <ServerPort>27020</ServerPort>
  <ServerName>New &amp; &lt;shiny&gt;</ServerName>
  <WorldName>Star System</WorldName>
</MyConfigDedicated>
`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	sdtd := "<ServerSettings>\n\t<property name=\"ServerPort\" value=\"26900\"/>\n\t<property name=\"ServerName\" value=\"x\"/>\n</ServerSettings>\n"
	got = edit(t, "xml", sdtd,
		Rule{Key: "ServerSettings/property[@name='ServerPort']", Value: "[value='26950']"},
		Rule{Key: "ServerSettings.property[@name='Region']", Value: "[value='Europe']"},
	)
	want = "<ServerSettings>\n\t<property name=\"ServerPort\" value=\"26950\"/>\n\t<property name=\"ServerName\" value=\"x\"/>\n\t<property name=\"Region\" value=\"Europe\"/>\n</ServerSettings>\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}

	if got := edit(t, "xml", "", Rule{Key: "Settings.Port", Value: "1"}); got != "<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<Settings><Port>1</Port></Settings>" {
		t.Errorf("new file: %q", got)
	}
	for _, bad := range []string{"<a>", "<a></b>", "<a/><b/>", "<a>&custom;</a>"} {
		if _, err := Edit("xml", []byte(bad), []Rule{{Key: "a", Value: "1"}}); err == nil {
			t.Errorf("%q should fail to parse", bad)
		}
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("server.properties", "server-port=1\n")
	write("broken.json", "{nope")
	if err := os.Symlink("server.properties", filepath.Join(dir, "link.properties")); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	vals := eggs.Values{Port: 25565, Env: map[string]string{"NAME": "srv"}}
	files := []eggs.ConfigFile{
		{Path: "server.properties", Parser: "properties", Find: []eggs.FindRule{{Key: "server-port", Value: "{{server.build.default.port}}"}}},
		{Path: "/plugins/new/config.yml", Parser: "yaml", Find: []eggs.FindRule{{Key: "name", Value: "{{env.NAME}}"}}},
		{Path: "broken.json", Parser: "json", Find: []eggs.FindRule{{Key: "a", Value: "1"}}},
		{Path: "../escape.properties", Parser: "properties", Find: []eggs.FindRule{{Key: "a", Value: "1"}}},
		{Path: "x.cfg", Parser: "toml"},
	}
	err = Apply(root, files, vals, Owner{UID: os.Getuid(), GID: os.Getgid()})
	if err == nil || !strings.Contains(err.Error(), "broken.json") || !strings.Contains(err.Error(), "unknown parser") {
		t.Fatalf("expected errors for broken.json and the unknown parser, got %v", err)
	}

	b, _ := os.ReadFile(filepath.Join(dir, "server.properties"))
	if string(b) != "server-port=25565\n" {
		t.Errorf("server.properties = %q", b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "server.properties")); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode changed to %v", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "plugins/new/config.yml")); string(b) != "name: srv\n" {
		t.Errorf("new file = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "broken.json")); string(b) != "{nope" {
		t.Errorf("unparseable file was changed: %q", b)
	}
	// "../escape.properties" is cleaned to the root, never outside it.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.properties")); err == nil {
		t.Error("wrote outside the root")
	}
	if fi, err := os.Lstat(filepath.Join(dir, "link.properties")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".raptor-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// Egg values that bring their own quotes must not be quoted again on the
// next start (OpenRCT2's egg does this).
func TestINIStableWithQuotedValues(t *testing.T) {
	r := Rule{Key: "network.player_name", Value: `"Server"`}
	once := edit(t, "ini", "", r)
	if twice := edit(t, "ini", once, r); twice != once || !strings.Contains(once, `player_name="Server"`) {
		t.Fatalf("once:\n%s\ntwice:\n%s", once, twice)
	}
}
