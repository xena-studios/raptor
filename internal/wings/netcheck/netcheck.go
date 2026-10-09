// Package netcheck reads which ports a server's game listens on, from
// inside its container's network namespace (/proc/<pid>/net), for the
// Panel's connection test (docs/PANEL.md#connection-test). Reading the
// namespace's socket tables works for TCP and UDP alike, and needs nothing
// from the game.
package netcheck

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Socket is a port the game listens on.
type Socket struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"` // "tcp" or "udp"
	// Loopback: bound to 127.0.0.1 or ::1 only, so nothing outside the
	// container reaches it (a game set to listen on localhost).
	Loopback bool `json:"loopback,omitempty"`
}

// Listening returns the TCP sockets listening and the UDP sockets bound in
// the network namespace of process pid, sorted by port.
func Listening(pid int) ([]Socket, error) {
	var out []Socket
	for _, t := range []struct{ file, proto string }{
		{"tcp", "tcp"}, {"tcp6", "tcp"}, {"udp", "udp"}, {"udp6", "udp"},
	} {
		f, err := os.Open(fmt.Sprintf("/proc/%d/net/%s", pid, t.file))
		if os.IsNotExist(err) && strings.HasSuffix(t.file, "6") {
			continue // no IPv6
		}
		if err != nil {
			return nil, err
		}
		socks, err := Parse(f, t.proto)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, socks...)
	}
	return dedupe(out), nil
}

// Parse reads one /proc/net/{tcp,udp}[6] table: TCP sockets in LISTEN,
// and every bound UDP socket.
func Parse(r io.Reader, proto string) ([]Socket, error) {
	var out []Socket
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // the header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		// TCP 0A is LISTEN; UDP 07 is an unconnected (listening) socket.
		if (proto == "tcp" && f[3] != "0A") || (proto == "udp" && f[3] != "07") {
			continue
		}
		addr, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil || port == 0 {
			continue
		}
		out = append(out, Socket{Port: int(port), Proto: proto, Loopback: loopback(addr)})
	}
	return out, sc.Err()
}

// loopback reports whether a /proc/net address (hex, in the kernel's byte
// order) is 127.0.0.0/8 or ::1.
func loopback(hex string) bool {
	switch len(hex) {
	case 8: // IPv4, little-endian on the machines Wings runs on: 0100007F
		return strings.HasSuffix(strings.ToUpper(hex), "7F")
	case 32:
		return strings.ToUpper(hex) == "00000000000000000000000001000000"
	}
	return false
}

// dedupe keeps one socket per port and protocol: listening on any address
// beats listening on loopback only.
func dedupe(socks []Socket) []Socket {
	type key struct {
		port  int
		proto string
	}
	best := map[key]Socket{}
	for _, s := range socks {
		k := key{s.Port, s.Proto}
		if old, ok := best[k]; !ok || (old.Loopback && !s.Loopback) {
			best[k] = s
		}
	}
	out := make([]Socket, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Socket) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Proto, b.Proto)
	})
	return out
}
