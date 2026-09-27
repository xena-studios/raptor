package localapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Short IDs are the last characters of a server's ID: IDs are UUIDv7, whose
// leading characters are a timestamp shared by servers created close
// together, while the end is random.
const shortID = 8

// resolve finds a server by full ID, ID suffix (at least shortID
// characters), or exact name.
func resolve(ctx context.Context, srv Servers, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("no server given"))
	}
	var byID, byName []string
	for id := range srv.List() {
		if id == ref {
			return id, nil
		}
		if len(ref) >= shortID && strings.HasSuffix(id, ref) {
			byID = append(byID, id)
		}
		if s, err := srv.Get(ctx, id); err == nil && s.Name == ref {
			byName = append(byName, id)
		}
	}
	for _, ids := range [][]string{byID, byName} {
		switch len(ids) {
		case 0:
		case 1:
			return ids[0], nil
		default:
			sort.Strings(ids)
			return "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%q matches several servers (%s); use the ID", ref, strings.Join(ids, ", ")))
		}
	}
	return "", connect.NewError(connect.CodeNotFound, fmt.Errorf("no server %q (see raptor ps)", ref))
}

// connectErr maps server manager errors to Connect codes.
func connectErr(err error) error {
	var ce *connect.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ce):
		return err
	case errors.Is(err, server.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, server.ErrInvalid), errors.Is(err, server.ErrCommandTooLong), errors.Is(err, server.ErrCommandInvalid):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, server.ErrCommandRate):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case errors.Is(err, server.ErrInstalling), errors.Is(err, server.ErrNotInstalled), errors.Is(err, server.ErrRunning),
		errors.Is(err, server.ErrDiskLimit), errors.Is(err, server.ErrConsoleNotReady):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, server.ErrClosed):
		return connect.NewError(connect.CodeUnavailable, err)
	}
	return err
}

func requireRoot(ctx context.Context, what string) error {
	if !IsRoot(ctx) {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("only root can %s (try sudo)", what))
	}
	return nil
}

// ListServers lists servers with their usage, measured in parallel.
func (s *Service) ListServers(ctx context.Context, _ *localv1.ListServersRequest) (*localv1.ListServersResponse, error) {
	srv, err := s.serverManager()
	if err != nil {
		return nil, err
	}
	ids := srv.List()
	out := make([]*localv1.ServerInfo, 0, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id := range ids {
		wg.Go(func() {
			info, err := serverInfo(ctx, srv, id)
			if err != nil {
				return // deleted meanwhile
			}
			mu.Lock()
			out = append(out, info)
			mu.Unlock()
		})
	}
	wg.Wait()
	sort.Slice(out, func(a, b int) bool {
		if out[a].GetName() != out[b].GetName() {
			return out[a].GetName() < out[b].GetName()
		}
		return out[a].GetId() < out[b].GetId()
	})
	return &localv1.ListServersResponse{Servers: out}, nil
}

func serverInfo(ctx context.Context, srv Servers, id string) (*localv1.ServerInfo, error) {
	cfg, err := srv.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	u, err := srv.Usage(ctx, id)
	if err != nil {
		return nil, err
	}
	p := cfg.Primary()
	info := &localv1.ServerInfo{
		Id:               id,
		Name:             cfg.Name,
		State:            string(u.State),
		CpuPercent:       u.CPUPercent,
		MemoryBytes:      u.MemoryBytes,
		MemoryLimitBytes: cfg.Limits.MemoryMiB << 20,
		DiskBytes:        u.Disk.Bytes,
		DiskLimitBytes:   u.Disk.LimitBytes,
		Address:          net.JoinHostPort(p.IP, strconv.Itoa(p.Port)),
		InstallError:     cfg.InstallError,
	}
	if !u.RunningSince.IsZero() {
		info.RunningSince = timestamppb.New(u.RunningSince)
	}
	return info, nil
}

var powerActions = map[localv1.PowerAction]server.PowerAction{
	localv1.PowerAction_POWER_ACTION_START:   server.PowerStart,
	localv1.PowerAction_POWER_ACTION_STOP:    server.PowerStop,
	localv1.PowerAction_POWER_ACTION_RESTART: server.PowerRestart,
	localv1.PowerAction_POWER_ACTION_KILL:    server.PowerKill,
}

// Power runs a power action as the calling Unix user.
func (s *Service) Power(ctx context.Context, req *localv1.PowerRequest) (*localv1.PowerResponse, error) {
	a, ok := powerActions[req.GetAction()]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown power action"))
	}
	if err := requireRoot(ctx, string(a)+" servers"); err != nil {
		return nil, err
	}
	srv, err := s.serverManager()
	if err != nil {
		return nil, err
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return nil, err
	}
	if err := srv.Power(ctx, id, a, Caller(ctx)); err != nil {
		return nil, connectErr(err)
	}
	st, err := srv.Status(id)
	if err != nil {
		return nil, connectErr(err)
	}
	return &localv1.PowerResponse{ServerId: id, State: string(st.State)}, nil
}

// SendCommand writes a console command as the calling Unix user.
func (s *Service) SendCommand(ctx context.Context, req *localv1.SendCommandRequest) (*localv1.SendCommandResponse, error) {
	if err := requireRoot(ctx, "send console commands"); err != nil {
		return nil, err
	}
	srv, err := s.serverManager()
	if err != nil {
		return nil, err
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return nil, err
	}
	return &localv1.SendCommandResponse{}, connectErr(srv.SendCommand(id, Caller(ctx), req.GetCommand()))
}

// StreamConsole sends the console history, then live lines. It ends when
// the client leaves, the server is deleted, or Wings shuts down.
func (s *Service) StreamConsole(ctx context.Context, req *localv1.StreamConsoleRequest, stream *connect.ServerStream[localv1.StreamConsoleResponse]) error {
	srv, err := s.serverManager()
	if err != nil {
		return err
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return err
	}
	st, err := srv.Status(id)
	if err != nil {
		return connectErr(err)
	}
	history, sub, unsubscribe := st.Console.Subscribe()
	defer unsubscribe()
	for _, l := range history {
		if err := stream.Send(&localv1.StreamConsoleResponse{Text: l}); err != nil {
			return err
		}
	}
	check := time.NewTicker(5 * time.Second)
	defer check.Stop()
	for {
		select {
		case l, ok := <-sub.C:
			if !ok {
				return nil
			}
			if err := stream.Send(&localv1.StreamConsoleResponse{Text: l}); err != nil {
				return err
			}
		case <-check.C:
			if _, err := srv.Status(id); err != nil {
				return connectErr(err) // deleted
			}
		case <-ctx.Done():
			return nil
		case <-shuttingDown(ctx):
			return connect.NewError(connect.CodeUnavailable, errors.New("wings is shutting down"))
		}
	}
}

// defaultLogLines is how much history TailLogs sends when not asked.
const defaultLogLines = 100

// TailLogs streams a server's output from Docker's log store.
func (s *Service) TailLogs(ctx context.Context, req *localv1.TailLogsRequest, stream *connect.ServerStream[localv1.TailLogsResponse]) error {
	srv, err := s.serverManager()
	if err != nil {
		return err
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return err
	}
	tail := int(req.GetLines())
	switch {
	case tail == 0:
		tail = defaultLogLines
	case tail < 0:
		tail = -1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-shuttingDown(ctx):
			cancel()
		case <-ctx.Done():
		}
	}()
	lines, errc, err := srv.Logs(ctx, id, tail, req.GetFollow())
	if err != nil {
		return connectErr(err)
	}
	for l := range lines {
		resp := &localv1.TailLogsResponse{Text: l.Text}
		if !l.Time.IsZero() {
			resp.Time = timestamppb.New(l.Time)
		}
		if err := stream.Send(resp); err != nil {
			cancel()
			for range lines { //nolint:revive // drain so the reader can exit
			}
			return err
		}
	}
	if err := <-errc; err != nil && ctx.Err() == nil {
		if strings.Contains(err.Error(), "No such container") {
			return connect.NewError(connect.CodeNotFound, errors.New("the server has no output yet (it hasn't been started)"))
		}
		return err
	}
	return nil
}
