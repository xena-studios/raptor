package server

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Console limits (docs/SERVERS.md#console).
const (
	historyLines    = 1000
	historyBytes    = 1 << 20
	streamPerSecond = 1000 // lines per second streamed to viewers, per server
	maxCommand      = 4096
	commandsPerSec  = 10 // per user
	subscriberQueue = 512
)

// Console holds a server's recent output and fans it out to viewers. Writes
// never block: Wings always drains the container's output, and a slow viewer
// only loses lines itself.
type Console struct {
	mu      sync.Mutex
	lines   []string // lines[head:] is the history, oldest first
	head    int
	bytes   int
	subs    map[*Subscription]struct{}
	window  time.Time // current 1-second streaming window
	sent    int       // lines streamed in the window
	dropped int       // lines not streamed in the window
	now     func() time.Time

	cmdMu    sync.Mutex
	commands map[string][]time.Time // per user, the last second of commands
}

// Subscription receives live console lines.
type Subscription struct {
	C chan string
}

func newConsole() *Console {
	return &Console{subs: map[*Subscription]struct{}{}, commands: map[string][]time.Time{}, now: time.Now}
}

// Write records a line and streams it to viewers, subject to the per-server
// rate limit. Lines over the limit are replaced by one marker line per second.
func (c *Console) Write(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record(line)

	now := c.now()
	if now.Sub(c.window) >= time.Second {
		if c.dropped > 0 {
			c.broadcast(fmt.Sprintf("[raptor] %d lines suppressed", c.dropped))
		}
		c.window, c.sent, c.dropped = now, 0, 0
	}
	if c.sent >= streamPerSecond {
		c.dropped++
		return
	}
	c.sent++
	c.broadcast(line)
}

// Notice writes a Wings message ("[raptor] …") to the console. It isn't
// rate limited.
func (c *Console) Notice(format string, a ...any) {
	line := "[raptor] " + fmt.Sprintf(format, a...)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.record(line)
	c.broadcast(line)
}

// record appends a line, dropping the oldest ones past the line and byte
// caps. Amortized O(1): dropped lines are compacted away in bulk.
func (c *Console) record(line string) {
	c.lines = append(c.lines, line)
	c.bytes += len(line)
	for len(c.lines)-c.head > historyLines || (c.bytes > historyBytes && len(c.lines)-c.head > 1) {
		c.bytes -= len(c.lines[c.head])
		c.lines[c.head] = ""
		c.head++
	}
	if c.head > historyLines {
		c.lines = append([]string(nil), c.lines[c.head:]...)
		c.head = 0
	}
}

func (c *Console) broadcast(line string) {
	for s := range c.subs {
		select {
		case s.C <- line:
		default: // viewer is too slow; it misses this line
		}
	}
}

func (c *Console) ordered() []string {
	return append([]string(nil), c.lines[c.head:]...)
}

// History returns the buffered lines, oldest first.
func (c *Console) History() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ordered()
}

// Tail returns the last n lines.
func (c *Console) Tail(n int) []string {
	h := c.History()
	if len(h) > n {
		h = h[len(h)-n:]
	}
	return h
}

// Subscribe returns the history and a subscription for new lines. Call the
// returned function to unsubscribe.
func (c *Console) Subscribe() ([]string, *Subscription, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := &Subscription{C: make(chan string, subscriberQueue)}
	c.subs[s] = struct{}{}
	return c.ordered(), s, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, ok := c.subs[s]; ok {
			delete(c.subs, s)
			close(s.C)
		}
	}
}

// Command errors.
var (
	ErrCommandTooLong  = errors.New("command is longer than 4 KiB")
	ErrCommandInvalid  = errors.New("command can't contain line breaks")
	ErrCommandRate     = errors.New("too many commands; at most 10 per second")
	ErrConsoleNotReady = errors.New("server isn't running")
)

// allowCommand checks a command's length and the user's rate.
func (c *Console) allowCommand(user, cmd string) error {
	if len(cmd) > maxCommand {
		return ErrCommandTooLong
	}
	if strings.ContainsAny(cmd, "\r\n") {
		return ErrCommandInvalid
	}
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()
	now := c.now()
	recent := c.commands[user][:0]
	for _, t := range c.commands[user] {
		if now.Sub(t) < time.Second {
			recent = append(recent, t)
		}
	}
	if len(recent) >= commandsPerSec {
		c.commands[user] = recent
		return ErrCommandRate
	}
	c.commands[user] = append(recent, now)
	return nil
}
