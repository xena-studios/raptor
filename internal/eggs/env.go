package eggs

import (
	"strconv"
	"strings"
)

// Runtime is the per-server information eggs see through environment variables.
type Runtime struct {
	ServerID        string
	Startup         string // the startup command, unexpanded ({{VAR}} placeholders intact)
	MemoryMiB       int64
	IP              string
	Port            int
	Timezone        string
	Location        string // exposed as P_SERVER_LOCATION
	AllocationLimit int    // exposed as P_SERVER_ALLOCATION_LIMIT
	Variables       map[string]string
}

// Environment returns the container environment, matching Pterodactyl Wings:
// built-ins first, then every egg variable (upper-cased), where egg variables
// can't override built-ins. STARTUP is passed unexpanded; the yolks'
// entrypoints turn {{VAR}} into ${VAR} and evaluate it inside the container.
func (r Runtime) Environment() []string {
	env := []string{
		"TZ=" + r.Timezone,
		"STARTUP=" + r.Startup,
		"SERVER_MEMORY=" + strconv.FormatInt(r.MemoryMiB, 10),
		"SERVER_IP=" + r.IP,
		"SERVER_PORT=" + strconv.Itoa(r.Port),
		"P_SERVER_UUID=" + r.ServerID,
		"P_SERVER_LOCATION=" + r.Location,
		"P_SERVER_ALLOCATION_LIMIT=" + strconv.Itoa(r.AllocationLimit),
	}
	seen := make(map[string]bool, len(env))
	for _, kv := range env {
		seen[kv[:strings.IndexByte(kv, '=')]] = true
	}
	for _, k := range sortedKeys(r.Variables) {
		up := strings.ToUpper(k)
		if seen[up] {
			continue
		}
		seen[up] = true
		env = append(env, up+"="+r.Variables[k])
	}
	return env
}

// Defaults returns the egg's variables with their default values.
func (e *Egg) Defaults() map[string]string {
	out := make(map[string]string, len(e.Variables))
	for _, v := range e.Variables {
		out[v.Env] = v.Default
	}
	return out
}
