package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func TestConsoleHistory(t *testing.T) {
	c := newConsole()
	for i := range 1500 {
		c.Write(fmt.Sprint(i))
	}
	h := c.History()
	if len(h) != historyLines || h[0] != "500" || h[len(h)-1] != "1499" {
		t.Fatalf("history: %d lines, %s…%s", len(h), h[0], h[len(h)-1])
	}
	if got := c.Tail(2); len(got) != 2 || got[1] != "1499" {
		t.Fatalf("tail = %v", got)
	}
}

func TestConsoleByteCap(t *testing.T) {
	c := newConsole()
	long := strings.Repeat("x", 8<<10)
	for range 500 {
		c.Write(long)
	}
	h := c.History()
	if total := len(h) * len(long); total > historyBytes || len(h) < 100 {
		t.Fatalf("%d lines, %d bytes", len(h), total)
	}
}

func TestConsoleThrottle(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newConsole()
	c.now = clk.now
	_, sub, cancel := c.Subscribe()
	defer cancel()

	for i := range 1200 {
		c.Write(fmt.Sprint(i))
	}
	clk.t = clk.t.Add(time.Second)
	c.Write("next")

	var got []string
	for len(sub.C) > 0 {
		got = append(got, <-sub.C)
	}
	// 1000 lines, then the marker, then the next window's line; the queue
	// (512) is smaller than a full window, so a viewer that doesn't read in
	// between sees the most recent lines only up to its queue size.
	if len(got) != subscriberQueue {
		t.Fatalf("viewer got %d lines", len(got))
	}
	if len(c.History()) != historyLines {
		t.Fatal("history must keep every line, throttled or not")
	}
}

func TestConsoleThrottleMarker(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newConsole()
	c.now = clk.now
	_, sub, cancel := c.Subscribe()
	defer cancel()

	var got []string
	drain := func() {
		for len(sub.C) > 0 {
			got = append(got, <-sub.C)
		}
	}
	for i := range 1200 {
		c.Write(fmt.Sprint(i))
		drain()
	}
	clk.t = clk.t.Add(time.Second)
	c.Write("next")
	drain()
	if len(got) != 1002 || got[1000] != "[raptor] 200 lines suppressed" || got[1001] != "next" {
		t.Fatalf("got %d lines, ending %v", len(got), got[len(got)-3:])
	}
}

func TestConsoleSubscribe(t *testing.T) {
	c := newConsole()
	c.Write("old")
	hist, sub, cancel := c.Subscribe()
	c.Write("new")
	if len(hist) != 1 || hist[0] != "old" || <-sub.C != "new" {
		t.Fatal("subscribe history/stream mismatch")
	}
	cancel()
	cancel()         // safe twice
	c.Write("after") // no panic writing after unsubscribe
	if _, ok := <-sub.C; ok {
		t.Fatal("channel should be closed")
	}
}

func TestAllowCommand(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	c := newConsole()
	c.now = clk.now
	for i := range 10 {
		if err := c.allowCommand("alice", "say hi"); err != nil {
			t.Fatalf("command %d: %v", i, err)
		}
	}
	if err := c.allowCommand("alice", "say hi"); !errors.Is(err, ErrCommandRate) {
		t.Fatalf("11th command: %v", err)
	}
	if err := c.allowCommand("bob", "say hi"); err != nil {
		t.Fatalf("other user is limited separately: %v", err)
	}
	clk.t = clk.t.Add(time.Second)
	if err := c.allowCommand("alice", "say hi"); err != nil {
		t.Fatalf("after a second: %v", err)
	}
	if err := c.allowCommand("alice", strings.Repeat("x", 4097)); !errors.Is(err, ErrCommandTooLong) {
		t.Fatal("long command accepted")
	}
	if err := c.allowCommand("carol", "stop\nop me"); !errors.Is(err, ErrCommandInvalid) {
		t.Fatal("multi-line command accepted")
	}
}
