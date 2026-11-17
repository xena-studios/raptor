// Package rcon talks to a game's remote console (#423): Valve's Source
// RCON (Source games, ARK, Palworld, and most others) and Rust's WebRCON.
// Wings connects from the node to the server's container with the
// password from the server's variables; nothing here leaves the node.
package rcon

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Conn is a logged-in RCON connection.
type Conn interface {
	// Exec runs one command and returns what the game answered.
	Exec(ctx context.Context, cmd string) (string, error)
	Close() error
}

// Timeout bounds dialing, logging in, and each command's first answer.
const Timeout = 5 * time.Second

// maxReply caps an answer: a player list is a few kilobytes.
const maxReply = 1 << 20

// ErrAuth is a password the game refused.
var ErrAuth = errors.New("the game refused the RCON password")

// Dial connects to addr ("host:port") with protocol ("source" or
// "webrcon") and logs in.
func Dial(ctx context.Context, protocol, addr, password string) (Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	switch protocol {
	case "source":
		return dialSource(ctx, addr, password)
	case "webrcon":
		return dialWeb(ctx, addr, password)
	}
	return nil, fmt.Errorf("unknown rcon protocol %q", protocol)
}
