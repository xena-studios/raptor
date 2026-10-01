package localapi

import (
	"context"
	"errors"
	"path/filepath"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Importer creates servers from another panel's (*server.Manager).
type Importer interface {
	ImportFrom(ctx context.Context, cfg server.Config, src string, progress func(files.CopyResult)) (string, files.CopyResult, error)
}

// ImportServer creates a server from another panel's, copying its files.
// Root only: like a local backup restore, root on the box already owns
// everything on it (docs/DECISIONS.md #122).
func (s *Service) ImportServer(ctx context.Context, req *localv1.ImportServerRequest) (*localv1.ImportServerResponse, error) {
	if err := requireRoot(ctx, "import servers"); err != nil {
		return nil, err
	}
	srv, err := s.serverManager()
	if err != nil {
		return nil, err
	}
	imp, ok := srv.(Importer)
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("importing isn't available"))
	}
	if !filepath.IsAbs(req.GetSourceDir()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("source_dir must be an absolute path"))
	}
	l := req.GetLimits()
	cfg := server.Config{
		Name: req.GetName(), Egg: req.GetEgg(), EggSource: req.GetEggSource(), Image: req.GetImage(),
		Startup: req.GetStartup(), Variables: req.GetVariables(), Settings: server.DefaultSettings(),
		Limits: containers.Limits{
			MemoryMiB: l.GetMemoryMib(), SwapMiB: l.GetSwapMib(), DiskMiB: l.GetDiskMib(),
			CPUPercent: l.GetCpuPercent(), Cpuset: l.GetCpuset(),
		},
	}
	for _, a := range req.GetAllocations() {
		cfg.Allocations = append(cfg.Allocations, server.Allocation{IP: a.GetIp(), Port: int(a.GetPort()), Primary: a.GetPrimary()})
	}
	id, res, err := imp.ImportFrom(ctx, cfg, req.GetSourceDir(), nil)
	if err != nil {
		return nil, connectErr(err)
	}
	return &localv1.ImportServerResponse{ServerId: id, Files: int64(res.Files), Bytes: res.Bytes, Skipped: int64(res.Skipped)}, nil
}
