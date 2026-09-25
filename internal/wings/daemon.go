// Package wings implements the Wings daemon.
package wings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/localapi"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Snapshot schedule for the state database (docs/WINGS.md#local-state-sqlite).
const (
	snapshotInterval = time.Hour
	snapshotKeep     = 24
)

// Run starts the daemon and blocks until ctx is cancelled, then shuts down
// gracefully. Stopping Wings never stops servers: they belong to Docker.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	for _, dir := range []string{filepath.Dir(cfg.Paths.State), cfg.Paths.Volumes, cfg.Paths.Tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	db, err := store.Open(ctx, cfg.Paths.State)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer func() { _ = db.Close() }()

	dc, err := docker.New()
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = dc.Close() }()

	svc := &localapi.Service{
		NodeID:    cfg.NodeID,
		PanelURL:  cfg.Panel.URL,
		StartedAt: time.Now(),
		Docker:    dc,
	}
	srv, err := localapi.Listen(ctx, cfg.Paths.Socket, localapi.Group, svc, log)
	if err != nil {
		return fmt.Errorf("local api: %w", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()

	log.Info("wings started", "socket", cfg.Paths.Socket, "state", cfg.Paths.State, "linked", cfg.NodeID != "")

	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("wings stopping")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(srv.Shutdown(shutdownCtx), <-errc)
		case err := <-errc:
			return fmt.Errorf("local api: %w", err)
		case <-ticker.C:
			if path, err := db.Snapshot(ctx, "hourly", snapshotKeep); err != nil {
				log.Error("state snapshot failed", "err", err)
			} else {
				log.Debug("state snapshot written", "path", path)
			}
		}
	}
}
