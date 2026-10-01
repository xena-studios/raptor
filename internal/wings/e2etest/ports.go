// Package e2etest has helpers shared by the end-to-end tests.
package e2etest

import (
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// Ports for game servers in tests come from below the kernel's ephemeral
// range. A port the kernel handed out with :0 is in that range, and between
// picking it and the server binding it (minutes, after an install), an
// outbound connection (an install script's download, an image pull) can
// take it as its source port.
const (
	portLow         = 20000
	defaultEphemera = 32768 // Linux's default ip_local_port_range start
)

// FreePort returns a port free for both TCP and UDP on every address, below
// the ephemeral range.
func FreePort(t testing.TB) int {
	t.Helper()
	high := ephemeralStart()
	for range 200 {
		p := portLow + rand.IntN(high-portLow) //nolint:gosec // not for security
		if containers.CheckPorts([]containers.Port{{Port: p}}) == nil {
			return p
		}
	}
	t.Fatal("no free port below the ephemeral range")
	return 0
}

func ephemeralStart() int {
	b, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return defaultEphemera
	}
	f := strings.Fields(string(b))
	if len(f) < 1 {
		return defaultEphemera
	}
	n, err := strconv.Atoi(f[0])
	if err != nil || n <= portLow+1000 {
		return defaultEphemera
	}
	return n
}
