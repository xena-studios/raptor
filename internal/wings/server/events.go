package server

import (
	"sync"
	"time"
)

// Event types. Every state change and notable action is an event; the job
// engine (Phase 1.5) persists them to the outbox that feeds the Panel.
const (
	EventCreated        = "server.created"
	EventUpdated        = "server.updated"
	EventDeleted        = "server.deleted"
	EventState          = "server.state"
	EventInstallStarted = "server.install.started"
	EventInstallDone    = "server.install.finished"
	EventInstallFailed  = "server.install.failed"
	EventCrashed        = "server.crashed"
	EventCrashLoop      = "server.crash_loop"
	EventCommand        = "server.console.command"
	EventConfigError    = "server.config_file.error"
)

// Event is something that happened to a server.
type Event struct {
	Type     string
	ServerID string
	Version  int64 // the server's config version at the time
	Time     time.Time
	Data     map[string]any
}

// bus fans events out to subscribers without ever blocking the publisher.
type bus struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func (b *bus) publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

func (b *bus) subscribe(buffer int) (<-chan Event, func()) {
	ch := make(chan Event, buffer)
	b.mu.Lock()
	if b.subs == nil {
		b.subs = map[chan Event]struct{}{}
	}
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
	}
}
