// Package rollout updates nodes' Wings in stages (docs/WINGS.md#updates): a
// version goes to 5% of nodes, then 25%, then all of them, each stage
// waiting long enough to see problems, and the rollout halts if updates
// start failing. Nodes are sent node.update, which installs the version on
// trial and rolls itself back if it isn't healthy (which includes getting
// its connection back).
package rollout

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/mod/semver"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// Stages, as percentages of nodes, and how long each must run before the
// next starts.
var (
	Stages = []int{5, 25, 100}
	Soak   = []time.Duration{time.Hour, 4 * time.Hour, 0}
)

// ResolveTimeout is how long a node has to come back on the new version
// before its update counts as failed (Wings' trial gives up after 5 minutes).
const ResolveTimeout = 15 * time.Minute

// UserID is who rollouts' commands are from.
const UserID = "panel:rollout"

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Engine advances the active rollout. Several Panel instances can run it;
// a tick holds an advisory lock, so only one acts at a time.
type Engine struct {
	DB       *pgxpool.Pool
	Sender   Sender
	PanelKey ed25519.PrivateKey
	Log      *slog.Logger
	Now      func() time.Time
	// Stages and Soak default to the package's.
	Stages []int
	Soak   []time.Duration
}

// lockID is the advisory lock a tick holds.
const lockID = 0x7261_7074_726f_6c6c // "raptroll"

// Start validates and begins a rollout of version.
func (e *Engine) Start(ctx context.Context, version string) (store.WingsRollout, error) {
	v := "v" + strings.TrimPrefix(version, "v")
	if !semver.IsValid(v) {
		return store.WingsRollout{}, fmt.Errorf("%q isn't a version", version)
	}
	r, err := store.New(e.DB).CreateRollout(ctx, strings.TrimPrefix(v, "v"))
	if err != nil && strings.Contains(err.Error(), "wings_rollouts_one_active") {
		return r, errors.New("a rollout is already running or paused; finish or cancel it first")
	}
	return r, err
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) log() *slog.Logger {
	if e.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return e.Log
}

// Run ticks every interval until ctx ends.
func (e *Engine) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := e.Tick(ctx); err != nil && ctx.Err() == nil {
			e.log().Warn("rollout tick failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func newer(a, b string) bool {
	va, vb := "v"+strings.TrimPrefix(a, "v"), "v"+strings.TrimPrefix(b, "v")
	if !semver.IsValid(vb) {
		return false // a dev build: never updated by rollouts
	}
	return semver.Compare(va, vb) > 0
}

// order is the rollout's node order: stable for a rollout, different for
// each, so the first 5% isn't always the same boxes.
func order(rollout pgtype.UUID, nodes []store.RolloutCandidatesRow) {
	key := func(n store.RolloutCandidatesRow) []byte {
		h := sha256.Sum256(append(rollout.Bytes[:], n.ID.Bytes[:]...))
		return h[:]
	}
	slices.SortFunc(nodes, func(a, b store.RolloutCandidatesRow) int { return slices.Compare(key(a), key(b)) })
}

// Tick does one step of the active rollout: resolve updates that were sent,
// halt if too many failed, send the current stage's nodes theirs, and move
// to the next stage once this one is done and has soaked.
func (e *Engine) Tick(ctx context.Context) error {
	conn, err := e.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()

	q := store.New(e.DB)
	r, err := q.ActiveRollout(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.State != "running" {
		return nil
	}
	stages, soak := e.Stages, e.Soak
	if stages == nil {
		stages, soak = Stages, Soak
	}
	nodes, err := q.RolloutCandidates(ctx)
	if err != nil {
		return err
	}
	order(r.ID, nodes)
	rows, err := q.RolloutNodes(ctx, r.ID)
	if err != nil {
		return err
	}
	done := map[[16]byte]store.RolloutNode{}
	for _, row := range rows {
		done[row.NodeID.Bytes] = row
	}
	byID := map[[16]byte]store.RolloutCandidatesRow{}
	for _, n := range nodes {
		byID[n.ID.Bytes] = n
	}

	// Resolve updates that were sent: the node came back on the new
	// version, or didn't in time.
	sent, failed, pending := 0, 0, 0
	for i, row := range rows {
		if row.Status == "sent" {
			n, ok := byID[row.NodeID.Bytes]
			switch {
			case ok && !newer(r.Version, n.WingsVersion):
				rows[i].Status = "updated"
				err = q.FinishRolloutNode(ctx, store.FinishRolloutNodeParams{RolloutID: r.ID, NodeID: row.NodeID, Status: "updated"})
			case e.now().Sub(row.SentAt.Time) > ResolveTimeout:
				why := "didn't come back"
				if ok {
					why = "still on " + n.WingsVersion
				}
				rows[i].Status = "failed"
				err = q.FinishRolloutNode(ctx, store.FinishRolloutNodeParams{
					RolloutID: r.ID, NodeID: row.NodeID, Status: "failed",
					Error: fmt.Sprintf("%s %s after the update (it rolled back, or is offline)", why, ResolveTimeout),
				})
			}
			if err != nil {
				return err
			}
		}
		switch rows[i].Status {
		case "sent":
			pending++
		case "skipped":
			continue
		case "failed":
			if row.Stage == r.Stage {
				failed++
			}
		}
		if row.Stage == r.Stage {
			sent++
		}
	}
	// Halt once a tenth of a stage's updates have failed.
	if failed > 0 && failed*10 >= sent {
		reason := fmt.Sprintf("%d of %d updates in the %d%% stage failed", failed, sent, stages[r.Stage])
		e.log().Warn("rollout halted", "version", r.Version, "reason", reason)
		return q.SetRolloutState(ctx, store.SetRolloutStateParams{ID: r.ID, State: "halted", Reason: reason})
	}

	// Send this stage's nodes their update.
	target := (stages[r.Stage]*len(nodes) + 99) / 100
	for _, n := range nodes[:target] {
		if _, ok := done[n.ID.Bytes]; ok || !newer(r.Version, n.WingsVersion) {
			continue
		}
		if !n.Connected {
			continue // it gets the update once it's back
		}
		status, why, err := e.send(ctx, n, r.Version)
		if err != nil {
			e.log().Info("couldn't send an update; trying again next time", "node", n.ID, "err", err)
			pending++
			continue
		}
		if err := q.AddRolloutNode(ctx, store.AddRolloutNodeParams{
			RolloutID: r.ID, NodeID: n.ID, Stage: r.Stage, Status: status, FromVersion: n.WingsVersion, Error: why,
		}); err != nil {
			return err
		}
		if status == "sent" {
			pending++
		}
	}
	if pending > 0 || e.now().Sub(r.StageStartedAt.Time) < soak[r.Stage] {
		return nil
	}
	if r.Stage+1 < int32(len(stages)) { //nolint:gosec // a handful of stages
		e.log().Info("rollout moving to the next stage", "version", r.Version, "percent", stages[r.Stage+1])
		return q.AdvanceRollout(ctx, r.ID)
	}
	e.log().Info("rollout done", "version", r.Version)
	return q.SetRolloutState(ctx, store.SetRolloutStateParams{ID: r.ID, State: "done", Reason: ""})
}

// send asks a node to update. A node that refuses (automatic updates off, a
// pin, a development build) is skipped, not failed.
func (e *Engine) send(ctx context.Context, n store.RolloutCandidatesRow, version string) (status, why string, err error) {
	node := uuid.UUID(n.ID.Bytes).String()
	env, err := nodecmd.New(e.PanelKey, node, UserID, "node.update", "", map[string]string{"version": version}, 5*time.Minute)
	if err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "", "", err
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := e.Sender.Execute(cctx, node, raw)
	switch {
	case err == nil && res.GetError() != "":
		return "skipped", res.GetError(), nil
	case err == nil:
		return "sent", "", nil
	case connect.CodeOf(err) == connect.CodeUnavailable || connect.CodeOf(err) == connect.CodeDeadlineExceeded:
		return "", "", err
	default:
		return "skipped", err.Error(), nil
	}
}

// SetState pauses, resumes, or cancels the active rollout.
func (e *Engine) SetState(ctx context.Context, state string) error {
	q := store.New(e.DB)
	r, err := q.ActiveRollout(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("no rollout is running or paused")
	}
	if err != nil {
		return err
	}
	switch state {
	case "paused", "running", "cancelled":
	default:
		return fmt.Errorf("unknown state %q", state)
	}
	return q.SetRolloutState(ctx, store.SetRolloutStateParams{ID: r.ID, State: state, Reason: "by an operator"})
}

// Status describes the latest rollout.
func (e *Engine) Status(ctx context.Context) (string, error) {
	q := store.New(e.DB)
	r, err := q.LatestRollout(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "no rollouts yet", nil
	}
	if err != nil {
		return "", err
	}
	rows, err := q.RolloutNodes(ctx, r.ID)
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	var failures []string
	for _, row := range rows {
		counts[row.Status]++
		if row.Status == "failed" || row.Status == "skipped" {
			failures = append(failures, fmt.Sprintf("  %s %s: %s", row.Status, uuid.UUID(row.NodeID.Bytes), row.Error))
		}
	}
	stages := e.Stages
	if stages == nil {
		stages = Stages
	}
	out := fmt.Sprintf("%s → %s: %s, stage %d%% since %s\n  %d sent, %d updated, %d failed, %d skipped",
		r.CreatedAt.Time.Format(time.DateTime), r.Version, r.State, stages[min(int(r.Stage), len(stages)-1)],
		r.StageStartedAt.Time.Format(time.DateTime), counts["sent"], counts["updated"], counts["failed"], counts["skipped"])
	if r.Reason != "" {
		out += "\n  " + r.Reason
	}
	if len(failures) > 0 {
		out += "\n" + strings.Join(failures, "\n")
	}
	return out, nil
}
