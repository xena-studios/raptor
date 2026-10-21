package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// gatewayLock is the advisory lock the instance holding the gateway
	// connection keeps, so the bot has one session, not one per instance.
	gatewayLock = 0x52_44_49_53_47_57 // "RDISGW"
	// intentGuildMembers is the gateway intent for members joining (a
	// privileged one: Server Members, on in the Developer Portal).
	intentGuildMembers = 1 << 1
	// Status is what the bot's profile says under its name.
	Status = "Type /docs to search the guide"
)

// takeOver is how often an instance without the gateway checks whether it
// can have it.
var takeOver = 5 * time.Second

// The gateway's opcodes.
const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11
)

func (b *Bot) gateway() string {
	if b.Gateway != "" {
		return b.Gateway
	}
	return "wss://gateway.discord.gg"
}

// Online keeps the bot online until ctx is done: whichever instance takes
// the lock holds the gateway connection, and the others wait to take over
// (a deploy, a crash). The instance that has it also brings the slash
// commands up to date.
func (b *Bot) Online(ctx context.Context) {
	for {
		b.lead(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(takeOver):
		}
	}
}

// lead holds the gateway connection while this instance has the lock.
func (b *Bot) lead(ctx context.Context) {
	conn, err := b.DB.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(gatewayLock)).Scan(&locked); err != nil || !locked {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	// The lock lasts as long as the database connection: if that goes, so
	// does the lock, and another instance may take over, so this one stops.
	var wg sync.WaitGroup
	wg.Go(func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Second):
			}
			if err := conn.Ping(ctx); err != nil {
				if ctx.Err() == nil {
					b.log().Warn("discord: lost the database connection holding the gateway", "err", err)
				}
				return
			}
		}
	})
	defer func() {
		cancel()
		wg.Wait()
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(gatewayLock))
	}()

	b.log().Info("discord: this instance holds the gateway")
	if err := b.Register(ctx); err != nil && ctx.Err() == nil {
		b.log().Error("discord: updating the slash commands failed", "err", err)
	}
	for wait := time.Second; ctx.Err() == nil; {
		start := time.Now()
		err := b.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(start) > 5*time.Minute {
			wait = time.Second
		}
		var fatal fatalError
		if errors.As(err, &fatal) {
			b.log().Error("discord: the gateway refused the bot", "err", err)
			wait = 10 * time.Minute
		} else {
			b.log().Warn("discord: gateway connection ended; reconnecting", "err", err, "in", wait.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 10*time.Minute)
	}
}

// fatalError is a gateway close that trying again won't fix: a bad token,
// or an intent the bot isn't allowed.
type fatalError struct{ code websocket.StatusCode }

func (e fatalError) Error() string { return fmt.Sprintf("gateway closed with %d", int(e.code)) }

type payload struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d,omitempty"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
}

// session is one gateway connection, from identifying to its end. Missed
// events (a member who joined while it was down) are caught by Run's pass
// over the roles, so it identifies afresh rather than resuming.
func (b *Bot) session(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, _, err := websocket.Dial(ctx, b.gateway()+"/?v=10&encoding=json", nil) //nolint:bodyclose // the library owns the body
	if err != nil {
		return err
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(4 << 20)

	read := func() (payload, error) {
		var p payload
		_, data, err := c.Read(ctx)
		if err != nil {
			switch code := websocket.CloseStatus(err); code {
			case 4004, 4010, 4011, 4012, 4013, 4014:
				return p, fatalError{code}
			}
			return p, err
		}
		return p, json.Unmarshal(data, &p)
	}
	send := func(op int, d any) error {
		data, err := json.Marshal(map[string]any{"op": op, "d": d})
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		defer wcancel()
		return c.Write(wctx, websocket.MessageText, data)
	}

	hello, err := read()
	if err != nil {
		return err
	}
	var h struct {
		Interval int64 `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(hello.D, &h); err != nil || hello.Op != opHello || h.Interval <= 0 {
		return fmt.Errorf("discord: expected hello, got op %d", hello.Op)
	}
	if err := send(opIdentify, map[string]any{
		"token":      b.Token,
		"intents":    intentGuildMembers,
		"properties": map[string]string{"os": "linux", "browser": "raptor", "device": "raptor"},
		"presence": map[string]any{
			"status": "online", "afk": false, "since": nil,
			"activities": []map[string]any{{"type": 4, "name": "Custom Status", "state": Status}},
		},
	}); err != nil {
		return err
	}

	var mu sync.Mutex
	var seq *int64
	acked := true
	heartbeat := func() error {
		mu.Lock()
		s := seq
		mu.Unlock()
		return send(opHeartbeat, s)
	}
	// Heartbeats: the first after a random part of the interval, as
	// Discord asks; one that isn't acknowledged before the next is due
	// means the connection is dead.
	interval := time.Duration(h.Interval) * time.Millisecond
	go func() {
		t := time.NewTimer(time.Duration(rand.Float64() * float64(interval))) //nolint:gosec // jitter, not a secret
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			mu.Lock()
			ok := acked
			acked = false
			mu.Unlock()
			if !ok || heartbeat() != nil {
				_ = c.Close(4000, "heartbeat")
				cancel()
				return
			}
			t.Reset(interval)
		}
	}()

	for {
		p, err := read()
		if err != nil {
			return err
		}
		switch p.Op {
		case opDispatch:
			if p.S != nil {
				mu.Lock()
				seq = p.S
				mu.Unlock()
			}
			b.dispatch(ctx, p.T, p.D)
		case opHeartbeat:
			if err := heartbeat(); err != nil {
				return err
			}
		case opHeartbeatAck:
			mu.Lock()
			acked = true
			mu.Unlock()
		case opReconnect:
			return errors.New("discord asked for a reconnect")
		case opInvalidSession:
			// Discord asks for a short random wait before identifying again.
			select {
			case <-ctx.Done():
			case <-time.After(time.Second + time.Duration(rand.IntN(4000))*time.Millisecond): //nolint:gosec // jitter, not a secret
			}
			return errors.New("invalid session")
		}
	}
}

// dispatch handles the events the bot listens for.
func (b *Bot) dispatch(ctx context.Context, event string, d json.RawMessage) {
	switch event {
	case "READY":
		var r struct {
			User user `json:"user"`
		}
		_ = json.Unmarshal(d, &r)
		b.log().Info("discord: online", "bot", r.User.Username)
	case "GUILD_MEMBER_ADD":
		var m struct {
			member
			GuildID string `json:"guild_id"`
		}
		if json.Unmarshal(d, &m) != nil || m.GuildID != b.GuildID || m.User.Bot {
			return
		}
		// A linked customer who joins (or comes back) gets the role now.
		go func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if _, err := b.checkMember(cctx, m.User.ID, m.Roles); err != nil {
				b.log().Warn("discord: checking a new member's role failed", "user", m.User.ID, "err", err)
			}
		}()
	}
}
