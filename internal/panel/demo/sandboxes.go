package demo

import (
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/panel/perms"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/telemetry"
	limits "github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/library"
)

// Sandboxes are the Panel's side of guests' sandbox servers (#449,
// docs/PANEL.md#sandboxes): a queue per demo node, which the leader works
// through as the node has room, sending server.create signed with the
// node's demo key; the node does the rest, deleting each when its time is
// up even without the Panel. Every night it also has the node build warm
// templates of the eggs guests pick most.
type Sandboxes struct {
	DB       *pgxpool.Pool
	Sender   Sender
	PanelKey ed25519.PrivateKey
	// Domain is the node domain (raptornodes.net), for sandboxes'
	// addresses.
	Domain string
	Log    *slog.Logger
	Now    func() time.Time

	libOnce sync.Once
	lib     map[string]library.DemoEgg // by library ID
	libErr  error

	statusMu sync.Mutex
	status   map[string]cachedStatus
}

// Metrics: demo sessions are counted where they start (auth); these count
// sandboxes by egg, and the pages guests reach (RecordDemoPage). Neither
// says who.
var (
	sandboxesMade, _ = telemetry.Meter.Int64Counter("raptor.demo.sandboxes", metric.WithDescription("Sandboxes guests asked for in the demo, by library egg and outcome"))
	pagesReached, _  = telemetry.Meter.Int64Counter("raptor.demo.pages", metric.WithDescription("Pages guests reached in the demo, by route"))
)

// The sandbox user for warm template builds.
const warmUser = UserID

// Lock IDs, apart from the reset's.
const sandboxLock = 0x7261_7074_7362_6f78 // "raptsbox"

// The warm pool is built at night (UTC), when the demo is quiet.
const (
	warmFrom = 3
	warmTo   = 5
)

// creatingTimeout is how long a create may take to answer before the row
// is given up on (the node then has it, or doesn't: the next status tells).
const creatingTimeout = 3 * time.Minute

func (s *Sandboxes) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sandboxes) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// library returns the egg library as the demo sees it (the same view Wings
// on the demo node has: library.DemoEggs), by library ID.
func (s *Sandboxes) library() (map[string]library.DemoEgg, error) {
	s.libOnce.Do(func() {
		all, err := library.DemoEggs()
		if err != nil {
			s.libErr = err
			return
		}
		s.lib = make(map[string]library.DemoEgg, len(all))
		for _, e := range all {
			s.lib[e.ID] = e
		}
	})
	return s.lib, s.libErr
}

// nodeState is what node.demo answers (Wings' actions.DemoState).
type nodeState struct {
	Enabled      bool              `json:"enabled"`
	MaxSandboxes int               `json:"max_sandboxes"`
	PortMin      int               `json:"port_min"`
	PortMax      int               `json:"port_max"`
	Sandboxes    []nodeSandbox     `json:"sandboxes"`
	Fingerprints []string          `json:"fingerprints"`
	Templates    map[string]string `json:"templates"`
}

type nodeSandbox struct {
	ServerID string `json:"server_id"`
	EggID    string `json:"egg_id"`
	Warm     bool   `json:"warm"`
	ReadyAt  int64  `json:"ready_at"`
	Deadline int64  `json:"deadline"`
	Extended bool   `json:"extended"`
}

type cachedStatus struct {
	st  nodeState
	at  time.Time
	err error
}

// statusFresh is how long a node's demo state is reused for the app.
const statusFresh = 10 * time.Second

// nodeStatus asks a demo node for its demo state; fresh skips the cache.
func (s *Sandboxes) nodeStatus(ctx context.Context, node string, fresh bool) (nodeState, error) {
	s.statusMu.Lock()
	c, ok := s.status[node]
	s.statusMu.Unlock()
	if ok && !fresh && s.now().Sub(c.at) < statusFresh {
		return c.st, c.err
	}
	var st nodeState
	raw, err := s.send(ctx, node, UserID, "node.demo", "", nil, false)
	if err == nil {
		err = json.Unmarshal(raw, &st)
	}
	if err == nil && !st.Enabled {
		err = errors.New("the node isn't a demo node: run `raptor demo on` on it")
	}
	s.statusMu.Lock()
	if s.status == nil {
		s.status = map[string]cachedStatus{}
	}
	s.status[node] = cachedStatus{st: st, at: s.now(), err: err}
	s.statusMu.Unlock()
	return st, err
}

// send runs a command on a demo node as user, signed with the node's demo
// key if demo is set, and returns its result.
func (s *Sandboxes) send(ctx context.Context, node, user, action, server string, params any, demo bool) (json.RawMessage, error) {
	env, err := nodecmd.New(s.PanelKey, node, user, action, server, params, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	if demo {
		key, err := nodecmd.DemoKey(s.PanelKey, node)
		if err != nil {
			return nil, err
		}
		if err := nodecmd.SignDemo(key, &env); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := s.Sender.Execute(cctx, node, raw)
	if err != nil {
		return nil, err
	}
	if res.GetError() != "" {
		return nil, errors.New(res.GetError())
	}
	return res.GetResult(), nil
}

// Run works the queues every interval until ctx ends. Every Panel instance
// runs it; one at a time does the work.
func (s *Sandboxes) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("demo: working the sandbox queue failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick brings each demo node's sandboxes up to date with what the node
// reports, and starts queued ones while it has room.
func (s *Sandboxes) Tick(ctx context.Context) error {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(sandboxLock)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(sandboxLock))
	}()
	q := store.New(s.DB)
	if _, err := q.EndOrphanSandboxes(ctx); err != nil {
		return err
	}
	nodes, err := q.DemoNodes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range nodes {
		if !n.Connected || !slices.Contains(n.Capabilities, nodelink.CapDemo) {
			continue
		}
		if err := s.tickNode(ctx, q, n); err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", uuid.UUID(n.NodeID.Bytes), err))
		}
	}
	return errors.Join(errs...)
}

func (s *Sandboxes) tickNode(ctx context.Context, q *store.Queries, n store.DemoNodesRow) error {
	node := uuid.UUID(n.NodeID.Bytes).String()
	st, err := s.nodeStatus(ctx, node, true)
	if err != nil {
		return err
	}
	onNode := map[string]nodeSandbox{}
	for _, b := range st.Sandboxes {
		onNode[b.ServerID] = b
	}
	live, err := q.LiveSandboxes(ctx, n.NodeID)
	if err != nil {
		return err
	}
	creating := 0
	var queued []store.DemoSandbox
	for _, r := range live {
		switch r.State {
		case "queued":
			queued = append(queued, r)
		case "creating":
			if r.CreatedAt.Valid && s.now().Sub(r.CreatedAt.Time) > creatingTimeout {
				s.end(ctx, q, r, "failed", "the demo node didn't answer")
				continue
			}
			creating++
		case "installing", "ready":
			b, ok := onNode[r.ServerID]
			if !ok {
				s.end(ctx, q, r, "gone", "")
				continue
			}
			if b.ReadyAt != 0 && (r.State != "ready" || r.Deadline.Time.Unix() != b.Deadline || r.Extended != b.Extended) {
				if err := q.SandboxReady(ctx, store.SandboxReadyParams{
					ID: r.ID, ReadyAt: ts(time.Unix(b.ReadyAt, 0)), Deadline: ts(time.Unix(b.Deadline, 0)), Extended: b.Extended,
				}); err != nil {
					return err
				}
			}
		}
	}
	// Guests before templates, each oldest first.
	slices.SortStableFunc(queued, func(a, b store.DemoSandbox) int {
		if a.Warm != b.Warm {
			if a.Warm {
				return 1
			}
			return -1
		}
		return a.QueuedAt.Time.Compare(b.QueuedAt.Time)
	})
	room := min(int(n.MaxSandboxes), st.MaxSandboxes) - len(st.Sandboxes) - creating
	for _, r := range queued {
		if room <= 0 {
			break
		}
		if err := s.start(ctx, q, n, st, r); err != nil {
			s.log().Warn("demo: starting a sandbox failed", "egg", r.LibraryID, "err", err)
		}
		room--
	}
	return s.warm(ctx, q, n, st, queued, room)
}

// start sends a queued sandbox to the node.
func (s *Sandboxes) start(ctx context.Context, q *store.Queries, n store.DemoNodesRow, st nodeState, r store.DemoSandbox) error {
	lib, err := s.library()
	if err != nil {
		return err
	}
	egg, err := q.DemoLibraryEgg(ctx, r.EggID)
	if err != nil {
		s.end(ctx, q, r, "failed", "that egg isn't in the library any more")
		return err
	}
	entry, ok := lib[r.LibraryID]
	if !ok || !entry.Fits {
		s.end(ctx, q, r, "failed", "that egg doesn't fit the demo")
		return nil
	}
	port, err := s.freePort(ctx, q, n, st)
	if err != nil {
		s.end(ctx, q, r, "failed", err.Error())
		return err
	}
	if c, err := q.SandboxCreating(ctx, store.SandboxCreatingParams{ID: r.ID, Port: int32(port)}); err != nil || c == 0 { //nolint:gosec // a port
		return err
	}
	user := warmUser
	if r.UserID.Valid {
		user = uuid.UUID(r.UserID.Bytes).String()
	}
	node := uuid.UUID(n.NodeID.Bytes).String()
	res, err := s.send(ctx, node, user, "server.create", "", createParams(r, egg.Egg, entry, port), true)
	outcome := "created"
	defer func() {
		sandboxesMade.Add(ctx, 1, metric.WithAttributes(attribute.String("egg", r.LibraryID), attribute.String("outcome", outcome), attribute.Bool("warm", r.Warm)))
	}()
	if err != nil {
		outcome = "failed"
		s.end(ctx, q, r, "failed", err.Error())
		return err
	}
	var out struct {
		ServerID string `json:"server_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.ServerID == "" {
		outcome = "failed"
		s.end(ctx, q, r, "failed", "the demo node's answer had no server")
		return errors.Join(errors.New("no server ID"), err)
	}
	if err := q.SandboxInstalling(ctx, store.SandboxInstallingParams{ID: r.ID, ServerID: out.ServerID}); err != nil {
		return err
	}
	if r.UserID.Valid {
		// The guest's own server: what guests get on the showcase servers.
		return q.SetServerGrant(ctx, store.SetServerGrantParams{
			OrgID: r.OrgID, UserID: r.UserID, NodeID: r.NodeID, ServerID: out.ServerID, Permissions: perms.DemoGuest,
		})
	}
	return nil
}

// createParams is the sandbox's server.create, made the way the
// conformance suite runs the egg (its raptor.yaml test data): its image,
// variables, and the sandbox limits.
func createParams(r store.DemoSandbox, file []byte, entry library.DemoEgg, port int) map[string]any {
	image := ""
	if e, err := eggs.Parse(file); err == nil && entry.Test.Image != "" &&
		slices.ContainsFunc(e.Images, func(i eggs.Image) bool { return i.Ref == entry.Test.Image }) {
		image = entry.Test.Image
	}
	no := false
	p := map[string]any{
		"name": r.EggName, "egg": file, "egg_source": uuid.UUID(r.EggID.Bytes).String(),
		"variables":           entry.Test.Variables,
		"limits":              map[string]int{"memory_mib": limits.MemoryMiB, "cpu_percent": limits.CPUPercent, "disk_mib": limits.DiskMiB},
		"allocations":         []map[string]any{{"ip": "0.0.0.0", "port": port, "primary": true}},
		"start_after_install": true, "backup_schedule": &no,
		"accept_eula": r.AcceptEula,
		"demo":        map[string]bool{"warm": r.Warm},
	}
	if image != "" {
		p["image"] = image
	}
	return p
}

// freePort is a port in both the Panel's and the node's sandbox range that
// no server and no other sandbox uses.
func (s *Sandboxes) freePort(ctx context.Context, q *store.Queries, n store.DemoNodesRow, st nodeState) (int, error) {
	used, err := q.NodeServerPorts(ctx, n.NodeID)
	if err != nil {
		return 0, err
	}
	live, err := q.LiveSandboxes(ctx, n.NodeID)
	if err != nil {
		return 0, err
	}
	for _, r := range live {
		used = append(used, r.Port)
	}
	from, to := max(int(n.PortMin), st.PortMin), min(int(n.PortMax), st.PortMax)
	for p := from; p <= to; p++ {
		if !slices.Contains(used, int32(p)) { //nolint:gosec // a port
			return p, nil
		}
	}
	return 0, errors.New("every sandbox port on the demo node is taken")
}

// warm queues the night's template builds: the eggs guests picked most in
// the last month, once a night, while no guest is waiting.
func (s *Sandboxes) warm(ctx context.Context, q *store.Queries, n store.DemoNodesRow, st nodeState, queued []store.DemoSandbox, room int) error {
	now := s.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if n.WarmEggs == 0 || now.Hour() < warmFrom || now.Hour() >= warmTo || room <= 0 || len(queued) > 0 ||
		(n.WarmedOn.Valid && !n.WarmedOn.Time.Before(today)) {
		return nil
	}
	top, err := q.MostPickedEggs(ctx, store.MostPickedEggsParams{OrgID: n.OrgID, Limit: n.WarmEggs})
	if err != nil {
		return err
	}
	lib, err := s.library()
	if err != nil {
		return err
	}
	for _, t := range top {
		e, ok := lib[t.LibraryID]
		if !ok || !e.Fits {
			continue
		}
		egg, err := q.DemoLibraryEgg(ctx, t.EggID)
		if err != nil {
			continue
		}
		// Guests accept the EULA to play, so templates of eggs with one
		// are built accepted: the template's key includes it.
		eula := false
		if parsed, err := eggs.Parse(egg.Egg); err == nil {
			eula = slices.Contains(parsed.Features, "eula")
		}
		if _, err := q.QueueSandbox(ctx, store.QueueSandboxParams{
			OrgID: n.OrgID, NodeID: n.NodeID, EggID: t.EggID, LibraryID: t.LibraryID, EggName: egg.Name,
			AcceptEula: eula, Warm: true,
		}); err != nil {
			return err
		}
	}
	s.log().Info("demo: building tonight's warm templates", "node", uuid.UUID(n.NodeID.Bytes), "eggs", len(top))
	return q.SetDemoWarmed(ctx, store.SetDemoWarmedParams{NodeID: n.NodeID, WarmedOn: pgtype.Date{Time: today, Valid: true}})
}

// end marks a sandbox over.
func (s *Sandboxes) end(ctx context.Context, q *store.Queries, r store.DemoSandbox, state, why string) {
	if err := q.EndSandbox(context.WithoutCancel(ctx), store.EndSandboxParams{ID: r.ID, State: state, Error: why}); err != nil {
		s.log().Warn("demo: recording a sandbox's end failed", "err", err)
	}
}

// ipHash is a guest's address, keyed so it can't be reversed by trying
// every address without the Panel's key: enough to keep one sandbox per
// address, and nothing else.
func (s *Sandboxes) ipHash(ip string) []byte {
	key, err := hkdf.Key(sha256.New, s.PanelKey.Seed(), nil, "raptor demo address v1", 32)
	if err != nil {
		return nil
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(ip))
	return m.Sum(nil)
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
