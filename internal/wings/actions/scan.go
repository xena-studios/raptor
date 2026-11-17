package actions

import (
	"context"
	"errors"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/backdoor"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/files"
)

// errNoScan is files.scan on a server whose egg has no x-raptor.scan.
var errNoScan = errors.New("this server's game has no backdoor scan")

// RegisterScan adds files.scan (#428): the egg's script folders (FiveM's
// resources) checked for known backdoor patterns on the node, so the files
// never leave it. It only reads, within backdoor.DefaultLimits, and
// reports file, line, and severity; deleting is up to the user, through
// the trash like any file.
func RegisterScan(x *command.Executor, m interface {
	Egg(ctx context.Context, id string) (*eggs.Egg, error)
}, fsvc *files.Service,
) {
	x.Register(FilesScan, command.Handler{Signed: command.Never, ReadOnly: true, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		egg, err := m.Egg(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		if egg == nil || egg.Raptor.Scan == nil {
			return nil, errNoScan
		}
		sc := egg.Raptor.Scan
		f, err := fsvc.Open(ctx, e.ServerID, false)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		return backdoor.ScanDirs(ctx, f, sc.Rules, sc.Dirs, backdoor.DefaultLimits)
	}})
}
