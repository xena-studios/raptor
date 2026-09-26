package eggs

import (
	"regexp"
	"strconv"
	"strings"
)

// Values are what config file placeholders resolve to.
type Values struct {
	ServerID  string
	IP        string
	Port      int
	MemoryMiB int64
	SwapMiB   int64
	DiskMiB   int64
	CPU       int64 // hard CPU limit in percent, 0 = none
	// Env holds the server's variables and built-ins (SERVER_MEMORY, …).
	Env map[string]string
	// DockerInterface is the server network's gateway address, which
	// {{config.docker.interface}} resolves to (Pterodactyl uses its bridge IP).
	DockerInterface string
}

var placeholder = regexp.MustCompile(`\{\{\s*((?:server|env|config)\.[\w.-]+)\s*\}\}`)

// Resolve replaces placeholders in a config file value. Pterodactyl splits
// this between the Panel (server.* and env.*) and Wings (config.*); Raptor
// resolves all of them in Wings. Both egg generations are supported:
//
//	{{server.build.default.ip}}   {{server.allocations.default.ip}}
//	{{server.build.default.port}} {{server.allocations.default.port}}
//	{{server.build.env.X}}        {{server.environment.X}}    {{env.X}}
//	{{server.build.memory}}       {{server.build.memory_limit}}  (and swap, disk, cpu)
//	{{config.docker.interface}}
//
// Like Pterodactyl, an unknown server.* or env.* placeholder becomes empty,
// and an unknown config.* placeholder is left as it is. Anything else in
// braces (e.g. {{SERVER_PORT}}) isn't a placeholder here and is kept.
func (v Values) Resolve(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		key := placeholder.FindStringSubmatch(m)[1]
		if val, ok := v.lookup(key); ok {
			return val
		}
		if strings.HasPrefix(key, "config.") {
			return m
		}
		return ""
	})
}

func (v Values) lookup(key string) (string, bool) {
	for _, p := range []string{"server.build.env.", "server.environment.", "env."} {
		if name, ok := strings.CutPrefix(key, p); ok {
			val, found := v.Env[name]
			return val, found
		}
	}
	switch key {
	case "server.uuid", "server.id":
		return v.ServerID, true
	case "server.build.default.ip", "server.allocations.default.ip":
		return v.IP, true
	case "server.build.default.port", "server.allocations.default.port":
		return strconv.Itoa(v.Port), true
	case "server.build.memory", "server.build.memory_limit":
		return strconv.FormatInt(v.MemoryMiB, 10), true
	case "server.build.swap":
		return strconv.FormatInt(v.SwapMiB, 10), true
	case "server.build.disk", "server.build.disk_space":
		return strconv.FormatInt(v.DiskMiB, 10), true
	case "server.build.cpu", "server.build.cpu_limit":
		return strconv.FormatInt(v.CPU, 10), true
	case "config.docker.interface", "config.docker.network.interface":
		return v.DockerInterface, v.DockerInterface != ""
	}
	return "", false
}
