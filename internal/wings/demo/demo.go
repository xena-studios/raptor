// Package demo runs the public demo's node (#449, docs/WINGS.md#demo-node):
// a node whose owner ran `raptor demo on`, which then takes the Panel's
// demo key for guests' sandboxes and the hourly restore of the showcase
// servers (docs/SECURITY-MODEL.md#the-demo-key).
//
// The manager keeps the sandboxes: short-lived servers made from library
// eggs, at most a few at once, each with a deadline Wings enforces itself.
// A sandbox lasts 15 minutes from when its install finished, once
// extended, and is deleted when its time is up, whether or not the Panel
// is there. It also keeps warm templates: installed copies of the eggs
// guests pick most, which new sandboxes are copied from.
package demo

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
	"github.com/xena-studios/raptor/library"
)

// Keys in Wings' state database.
const (
	configKey    = "demo.config"
	sandboxesKey = "demo.sandboxes"
	networkKey   = "demo.network"
)

// Errors a caller can match.
var (
	ErrOff      = errors.New("this node isn't a demo node")
	ErrFull     = errors.New("the demo node is full: every sandbox slot is taken")
	ErrNotBox   = errors.New("not a sandbox")
	ErrExtended = errors.New("this sandbox was extended already")
	ErrNotReady = errors.New("this sandbox isn't ready yet")
)

// Config is a demo node's settings, from `raptor demo on`: root on the
// box sets them, never the Panel.
type Config struct {
	// Key is the Panel's demo key for this node (its public half, as
	// `panel staff demo node` shows it).
	Key          []byte `json:"key"`
	MaxSandboxes int    `json:"max_sandboxes"`
	PortMin      int    `json:"port_min"`
	PortMax      int    `json:"port_max"`
	// Templates is how many warm templates to keep (0: none).
	Templates int `json:"templates"`
}

// Defaults fills in what's unset.
func (c *Config) Defaults() {
	if c.MaxSandboxes == 0 {
		c.MaxSandboxes = demo.DefaultMaxSandboxes
	}
	if c.PortMin == 0 && c.PortMax == 0 {
		c.PortMin, c.PortMax = demo.DefaultPortMin, demo.DefaultPortMax
	}
	if c.Templates < 0 {
		c.Templates = 0
	}
}

// Validate checks settings before they're saved.
func (c Config) Validate() error {
	switch {
	case len(c.Key) != ed25519.PublicKeySize:
		return errors.New("the demo key must be a base64 Ed25519 public key (from `panel staff demo node`)")
	case c.MaxSandboxes < 1 || c.MaxSandboxes > demo.MaxSandboxes:
		return fmt.Errorf("sandboxes must be from 1 to %d", demo.MaxSandboxes)
	case c.PortMin < 1024 || c.PortMax > 65535 || c.PortMin > c.PortMax:
		return errors.New("the sandbox ports must be a range within 1024-65535")
	case c.PortMax-c.PortMin+1 < c.MaxSandboxes:
		return errors.New("the sandbox port range must have a port for each sandbox")
	case c.Templates > 32:
		return errors.New("at most 32 templates")
	}
	return nil
}

// Load returns the node's demo settings, or nil if it isn't a demo node.
func Load(ctx context.Context, db *store.DB) (*Config, error) {
	b, err := db.Read.GetKV(ctx, configKey)
	if errors.Is(err, sql.ErrNoRows) || len(b) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("demo settings: %w", err)
	}
	c.Defaults()
	return &c, c.Validate()
}

// Save stores the node's demo settings; nil turns the demo off. Wings
// reads them when it starts.
func Save(ctx context.Context, db *store.DB, c *Config) error {
	if c == nil {
		return db.Write.SetKV(ctx, store.SetKVParams{Key: configKey, Value: []byte{}})
	}
	c.Defaults()
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return db.Write.SetKV(ctx, store.SetKVParams{Key: configKey, Value: b})
}

// Sandbox is one guest's server (or a template being built), as Wings
// keeps it.
type Sandbox struct {
	ServerID    string `json:"server_id"`
	EggID       string `json:"egg_id"`
	Fingerprint string `json:"fingerprint"`
	// Template is its warm template's key; Warm marks a template build,
	// deleted once its install is captured.
	Template string `json:"template,omitempty"`
	Warm     bool   `json:"warm,omitempty"`
	// Times are unix seconds. ReadyAt is 0 until the install finished;
	// Deadline is when it's deleted.
	CreatedAt int64 `json:"created_at"`
	ReadyAt   int64 `json:"ready_at,omitempty"`
	Deadline  int64 `json:"deadline"`
	Extended  bool  `json:"extended,omitempty"`
}

// Servers is what the manager needs of the server manager.
type Servers interface {
	Get(ctx context.Context, id string) (*server.Server, error)
	List() map[string]server.State
	Delete(ctx context.Context, id string) error
	Events() (<-chan server.Event, func())
	FilesDir(id string) (string, error)
}

// Options configure the manager.
type Options struct {
	Config  Config
	DB      *store.DB
	Servers Servers
	// Templates keeps the warm templates (nil: none).
	Templates *Templates
	Log       *slog.Logger
	Now       func() time.Time
	// Arch is the node's CPU architecture (default runtime.GOARCH).
	Arch string
}

// Manager keeps a demo node's sandboxes.
type Manager struct {
	o       Options
	mu      sync.Mutex
	boxes   map[string]*Sandbox
	pending int // creates in flight
	eggs    map[string]library.DemoEgg
}

// New loads the sandboxes Wings knew before it stopped, and the library
// eggs that fit a sandbox on this node.
func New(ctx context.Context, o Options) (*Manager, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Arch == "" {
		o.Arch = runtime.GOARCH
	}
	all, err := library.DemoEggs()
	if err != nil {
		return nil, fmt.Errorf("the egg library: %w", err)
	}
	m := &Manager{o: o, boxes: map[string]*Sandbox{}, eggs: map[string]library.DemoEgg{}}
	for _, e := range all {
		if e.Fits && e.Fingerprint != "" && slices.Contains(e.Arch, o.Arch) {
			m.eggs[e.Fingerprint] = e
		}
	}
	b, err := o.DB.Read.GetKV(ctx, sandboxesKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if len(b) > 0 {
		var list []*Sandbox
		if err := json.Unmarshal(b, &list); err != nil {
			o.Log.Error("demo: the sandbox list is unreadable; starting empty (the reaper finds no deadlines for them)", "err", err)
		}
		for _, s := range list {
			m.boxes[s.ServerID] = s
		}
	}
	return m, nil
}

// Config is the node's demo settings.
func (m *Manager) Config() Config { return m.o.Config }

// Egg returns the library egg a fingerprint is, if it's one that fits a
// sandbox on this node.
func (m *Manager) Egg(fingerprint string) (library.DemoEgg, bool) {
	e, ok := m.eggs[fingerprint]
	return e, ok
}

// Fingerprints are the eggs that fit, for the Panel to offer only those.
func (m *Manager) Fingerprints() []string {
	out := make([]string, 0, len(m.eggs))
	for fp := range m.eggs {
		out = append(out, fp)
	}
	slices.Sort(out)
	return out
}

// Room reports an error when every sandbox slot is taken.
func (m *Manager) Room() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.boxes)+m.pending >= m.o.Config.MaxSandboxes {
		return ErrFull
	}
	return nil
}

// Reserve takes a slot for a create in flight; release gives it back
// (after Add, or when the create failed).
func (m *Manager) Reserve() (release func(), err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.boxes)+m.pending >= m.o.Config.MaxSandboxes {
		return nil, ErrFull
	}
	m.pending++
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.pending--
			m.mu.Unlock()
		})
	}, nil
}

// Add records a new sandbox. Until its install finishes it has
// InstallLimit to do so.
func (m *Manager) Add(ctx context.Context, s Sandbox) error {
	now := m.o.Now()
	s.CreatedAt = now.Unix()
	s.Deadline = now.Add(demo.InstallLimit).Unix()
	m.mu.Lock()
	m.boxes[s.ServerID] = &s
	err := m.saveLocked(ctx)
	m.mu.Unlock()
	return err
}

// IsSandbox reports whether a server is a sandbox.
func (m *Manager) IsSandbox(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.boxes[id]
	return ok
}

// Get returns a sandbox.
func (m *Manager) Get(id string) (Sandbox, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.boxes[id]
	if !ok {
		return Sandbox{}, false
	}
	return *s, true
}

// Ready starts a sandbox's time: its install finished.
func (m *Manager) Ready(ctx context.Context, id string) (Sandbox, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.boxes[id]
	if !ok || s.ReadyAt != 0 {
		return Sandbox{}, false
	}
	now := m.o.Now()
	s.ReadyAt, s.Deadline = now.Unix(), now.Add(demo.Lifetime).Unix()
	if err := m.saveLocked(ctx); err != nil {
		m.o.Log.Error("demo: saving the sandboxes failed", "err", err)
	}
	return *s, true
}

// Extend gives a ready sandbox Extension more, once. Whether anyone is
// waiting is the Panel's to know; that it happens once is Wings'.
func (m *Manager) Extend(ctx context.Context, id string) (Sandbox, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.boxes[id]
	switch {
	case !ok:
		return Sandbox{}, ErrNotBox
	case s.ReadyAt == 0:
		return Sandbox{}, ErrNotReady
	case s.Extended:
		return Sandbox{}, ErrExtended
	}
	s.Extended = true
	s.Deadline = time.Unix(s.Deadline, 0).Add(demo.Extension).Unix()
	return *s, m.saveLocked(ctx)
}

// List returns the sandboxes, oldest first.
func (m *Manager) List() []Sandbox {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Sandbox, 0, len(m.boxes))
	for _, s := range m.boxes {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b Sandbox) int { return int(a.CreatedAt - b.CreatedAt) })
	return out
}

// forget drops a sandbox whose server is gone.
func (m *Manager) forget(ctx context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.boxes[id]; !ok {
		return
	}
	delete(m.boxes, id)
	if err := m.saveLocked(ctx); err != nil {
		m.o.Log.Error("demo: saving the sandboxes failed", "err", err)
	}
}

func (m *Manager) saveLocked(ctx context.Context) error {
	list := make([]*Sandbox, 0, len(m.boxes))
	for _, s := range m.boxes {
		list = append(list, s)
	}
	b, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return m.o.DB.Write.SetKV(context.WithoutCancel(ctx), store.SetKVParams{Key: sandboxesKey, Value: b})
}

// reapEvery is how often deadlines are checked.
const reapEvery = 10 * time.Second

// Run enforces the deadlines and builds templates until ctx ends: a
// sandbox whose install finished starts its time, one whose time is up is
// deleted, and a template build is captured and deleted. It runs whether
// or not the Panel is connected.
func (m *Manager) Run(ctx context.Context) {
	events, stop := m.o.Servers.Events()
	defer stop()
	m.Reap(ctx)
	t := time.NewTicker(reapEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if ev.Type == server.EventInstallDone {
				m.installed(ctx, ev.ServerID)
			}
		case <-t.C:
			m.Reap(ctx)
		}
	}
}

// catchUp starts the time of sandboxes whose install finished while Wings
// wasn't watching.
func (m *Manager) catchUp(ctx context.Context) {
	for _, s := range m.List() {
		if s.ReadyAt != 0 {
			continue
		}
		if srv, err := m.o.Servers.Get(ctx, s.ServerID); err == nil && srv.InstallState == "installed" {
			m.installed(ctx, s.ServerID)
		}
	}
}

// installed handles a sandbox's finished install: a guest's starts its 15
// minutes; a template build is copied into the templates and deleted.
func (m *Manager) installed(ctx context.Context, id string) {
	s, ok := m.Ready(ctx, id)
	if !ok || !s.Warm {
		return
	}
	if m.o.Templates != nil && s.Template != "" {
		if dir, err := m.o.Servers.FilesDir(id); err != nil {
			m.o.Log.Error("demo: a template build's files are missing", "server", id, "err", err)
		} else if err := m.o.Templates.Capture(ctx, s.Template, s.EggID, dir); err != nil {
			m.o.Log.Error("demo: capturing a template failed", "egg", s.EggID, "err", err)
		} else {
			m.o.Log.Info("demo: template ready", "egg", s.EggID, "template", s.Template)
		}
	}
	m.remove(ctx, id, "template captured")
}

// Reap deletes sandboxes whose time is up, and forgets ones whose server
// was deleted some other way.
func (m *Manager) Reap(ctx context.Context) {
	// An install that finished before its sandbox was recorded.
	m.catchUp(ctx)
	now := m.o.Now().Unix()
	live := m.o.Servers.List()
	for _, s := range m.List() {
		if _, ok := live[s.ServerID]; !ok {
			m.forget(ctx, s.ServerID)
			continue
		}
		if now >= s.Deadline {
			why := "its 15 minutes are up"
			if s.ReadyAt == 0 {
				why = "its install took too long"
			}
			m.remove(ctx, s.ServerID, why)
		}
	}
	if m.o.Templates != nil {
		m.o.Templates.Prune(m.o.Config.Templates)
	}
}

// remove deletes a sandbox's server.
func (m *Manager) remove(ctx context.Context, id, why string) {
	if err := m.o.Servers.Delete(ctx, id); err != nil && !errors.Is(err, server.ErrNotFound) {
		m.o.Log.Error("demo: deleting a sandbox failed; trying again", "server", id, "err", err)
		return
	}
	m.o.Log.Info("demo: sandbox deleted", "server", id, "why", why)
	m.forget(ctx, id)
}

// Removed is told a sandbox was deleted by a command.
func (m *Manager) Removed(ctx context.Context, id string) { m.forget(ctx, id) }

// Network is the demo network's ID (EnsureNetwork), "" before.
func Network(ctx context.Context, db *store.DB) string {
	b, err := db.Read.GetKV(ctx, networkKey)
	if err != nil {
		return ""
	}
	return string(b)
}

// SetNetwork records the demo network's ID.
func SetNetwork(ctx context.Context, db *store.DB, id string) error {
	return db.Write.SetKV(ctx, store.SetKVParams{Key: networkKey, Value: []byte(id)})
}
