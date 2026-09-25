// Package wings implements the Wings daemon.
package wings

import (
	"context"
	"log/slog"
)

// Run starts the daemon and blocks until ctx is cancelled.
func Run(ctx context.Context, log *slog.Logger) error {
	log.Info("wings started")
	<-ctx.Done()
	log.Info("wings stopping")
	return nil
}
