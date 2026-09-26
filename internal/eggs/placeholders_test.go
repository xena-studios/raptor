package eggs

import "testing"

func TestResolve(t *testing.T) {
	v := Values{
		ServerID: "srv1", IP: "0.0.0.0", Port: 25565, MemoryMiB: 2048,
		Env:             map[string]string{"SERVER_NAME": "My Server", "MAX_PLAYERS": "20"},
		DockerInterface: "172.29.0.1",
	}
	for in, want := range map[string]string{
		"{{server.build.default.port}}":                      "25565",
		"{{server.allocations.default.port}}":                "25565",
		"0.0.0.0:{{server.build.default.port}}":              "0.0.0.0:25565",
		"{{server.build.default.ip}}":                        "0.0.0.0",
		"{{server.build.env.SERVER_NAME}}":                   "My Server",
		"{{server.environment.MAX_PLAYERS}}":                 "20",
		"{{env.SERVER_NAME}} ({{env.MAX_PLAYERS}})":          "My Server (20)",
		"{{ server.build.memory }}":                          "2048",
		"{{server.build.env.MISSING}}":                       "",
		"{{server.build.nonsense}}":                          "",
		"{{config.docker.interface}}$2":                      "172.29.0.1$2",
		"{{config.unknown.key}}":                             "{{config.unknown.key}}",
		"{{QUERY_PORT}}":                                     "{{QUERY_PORT}}",
		"no placeholders":                                    "no placeholders",
		`server.hostname "{{server.build.env.SERVER_NAME}}"`: `server.hostname "My Server"`,
	} {
		if got := v.Resolve(in); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, want)
		}
	}
}
