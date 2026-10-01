package server

import (
	"context"
	"fmt"
	"os"

	"github.com/xena-studios/raptor/internal/wings/files"
)

// ImportFrom creates a server from another panel's: cfg as it was there,
// and src, its directory, copied in (never moved: the original stays as it
// was). The server is created installed and stopped. progress, if set, is
// called as files are copied.
func (m *Manager) ImportFrom(ctx context.Context, cfg Config, src string, progress func(files.CopyResult)) (string, files.CopyResult, error) {
	var res files.CopyResult
	in, err := os.OpenRoot(src)
	if err != nil {
		return "", res, fmt.Errorf("source: %w", err)
	}
	defer func() { _ = in.Close() }()
	id, err := m.Create(ctx, cfg, CreateOptions{Import: func(ctx context.Context, dir string) error {
		out, err := files.Open(dir, m.o.UID, m.o.GID, nil)
		if err != nil {
			return err
		}
		defer func() { _ = out.Close() }()
		res, err = out.CopyTree(ctx, in, progress)
		return err
	}})
	return id, res, err
}
