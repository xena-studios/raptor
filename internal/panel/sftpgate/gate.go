// Package sftpgate keeps each node's SFTP port closed until it's needed
// (docs/DECISIONS.md #225): open only while someone has a temporary SFTP
// password on the node that hasn't run out, and SFTP is allowed there. It
// sends node.sftp when what a node reports differs from that, right after
// a password is made or revoked, and every minute for the ones that ran
// out.
package sftpgate

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// UserID is who the gate's commands are from.
const UserID = "panel:sftp"

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Gate opens and closes nodes' SFTP ports.
type Gate struct {
	DB       *pgxpool.Pool
	Sender   Sender
	PanelKey ed25519.PrivateKey
	Log      *slog.Logger
}

func (g *Gate) log() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

// status is node.sftp's answer.
type status struct {
	Enabled     bool   `json:"enabled"`
	Port        int32  `json:"port"`
	Fingerprint string `json:"host_key_fingerprint"`
}

// Sync opens or closes one node's SFTP port to match what's wanted, and
// records what the node answers. A node that's offline is synced when the
// next Run finds it out of step.
func (g *Gate) Sync(ctx context.Context, nodeID string) error {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return err
	}
	node := pgtype.UUID{Bytes: id, Valid: true}
	q := store.New(g.DB)
	row, err := q.NodeSFTPWanted(ctx, node)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.SftpEnabled == row.Wanted {
		return nil
	}
	env, err := nodecmd.New(g.PanelKey, nodeID, UserID, "node.sftp", "", map[string]bool{"enabled": row.Wanted}, time.Minute)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := g.Sender.Execute(cctx, nodeID, raw)
	if err != nil {
		return err
	}
	if res.GetError() != "" {
		return fmt.Errorf("node.sftp: %s", res.GetError())
	}
	var st status
	if err := json.Unmarshal(res.GetResult(), &st); err != nil {
		return fmt.Errorf("node.sftp's answer: %w", err)
	}
	// Recorded now, so the next Run doesn't send it again; the node's
	// node.sftp event says the same when the mirror gets to it.
	return q.SetNodeSFTP(ctx, store.SetNodeSFTPParams{ID: node, SftpEnabled: st.Enabled, SftpPort: st.Port, SftpHostKey: st.Fingerprint})
}

// Tick syncs every node that's out of step.
func (g *Gate) Tick(ctx context.Context) {
	ids, err := store.New(g.DB).NodesSFTPOutOfStep(ctx)
	if err != nil {
		g.log().Error("listing nodes' sftp failed", "err", err)
		return
	}
	for _, id := range ids {
		node := uuid.UUID(id.Bytes).String()
		if err := g.Sync(ctx, node); err != nil {
			g.log().Info("couldn't sync a node's sftp; trying again next time", "node", node, "err", err)
		}
	}
}

// Run ticks every interval until ctx ends. Every Panel instance runs it;
// node.sftp is idempotent.
func (g *Gate) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		g.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
