package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1/localv1connect"
	"github.com/xena-studios/raptor/internal/wings/tui"
)

func tuiCmd(ctx context.Context, args []string) error {
	c, _, err := dial("tui", args, nil)
	if err != nil {
		return err
	}
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return errors.New("raptor tui needs a terminal")
	}
	// Fail now, with the usual message, if Wings isn't reachable.
	if _, err := c.ListServers(ctx, &localv1.ListServersRequest{}); err != nil {
		return rpcErr(err)
	}
	return tui.Run(ctx, tuiBackend{c}, os.Geteuid() == 0)
}

// tuiBackend is the TUI's view of the local API.
type tuiBackend struct{ c localv1connect.LocalServiceClient }

func (b tuiBackend) Servers(ctx context.Context) ([]*localv1.ServerInfo, error) {
	res, err := b.c.ListServers(ctx, &localv1.ListServersRequest{})
	if err != nil {
		return nil, rpcErr(err)
	}
	return res.GetServers(), nil
}

func (b tuiBackend) Metrics(ctx context.Context, id string) (*localv1.GetMetricsResponse, error) {
	return b.c.GetMetrics(ctx, &localv1.GetMetricsRequest{Server: id})
}

func (b tuiBackend) Power(ctx context.Context, id string, a localv1.PowerAction) error {
	_, err := b.c.Power(ctx, &localv1.PowerRequest{Server: id, Action: a})
	return rpcErr(err)
}

func (b tuiBackend) Command(ctx context.Context, id, cmd string) error {
	_, err := b.c.SendCommand(ctx, &localv1.SendCommandRequest{Server: id, Command: cmd})
	return rpcErr(err)
}

func (b tuiBackend) Backup(ctx context.Context, id string) (string, error) {
	res, err := b.c.CreateBackup(ctx, &localv1.CreateBackupRequest{Server: id})
	if err != nil {
		return "", rpcErr(err)
	}
	return fmt.Sprintf("backup %s queued", short(res.GetBackup().GetId())), nil
}

func (b tuiBackend) Console(ctx context.Context, id string) (<-chan string, error) {
	st, err := b.c.StreamConsole(ctx, &localv1.StreamConsoleRequest{Server: id})
	if err != nil {
		return nil, rpcErr(err)
	}
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		defer func() { _ = st.Close() }()
		for st.Receive() {
			select {
			case ch <- st.Msg().GetText():
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
