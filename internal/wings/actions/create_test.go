package actions

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// The server.create params the web app builds (web/src/lib/servers.test.ts
// has the same vector): what the user's passkey signs must decode into
// exactly the server they chose.
const webCreateVector = `{"name":"Survival","egg":"eyJ4IjoxfQ==","egg_source":"minecraft/paper","image":"ghcr.io/x/java:25","variables":{"MINECRAFT_VERSION":"1.21.10"},"limits":{"memory_mib":2048,"disk_mib":10240},"allocations":[{"ip":"0.0.0.0","port":25565,"primary":true}],"start_after_install":true,"accept_eula":true}`

func TestWebCreateVector(t *testing.T) {
	var p CreateParams
	dec := json.NewDecoder(strings.NewReader(webCreateVector))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		t.Fatal(err)
	}
	want := CreateParams{
		ServerConfig: ServerConfig{
			Name: "Survival", Egg: []byte(`{"x":1}`), EggSource: "minecraft/paper", Image: "ghcr.io/x/java:25",
			Variables:   map[string]string{"MINECRAFT_VERSION": "1.21.10"},
			Limits:      containers.Limits{MemoryMiB: 2048, DiskMiB: 10240},
			Allocations: []server.Allocation{{IP: "0.0.0.0", Port: 25565, Primary: true}},
		},
		StartAfterInstall: true,
		AcceptEULA:        true,
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("decoded %+v", p)
	}
}
