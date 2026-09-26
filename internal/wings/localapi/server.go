// Package localapi serves the Wings local API on a Unix socket for the raptor
// CLI and TUI (docs/WINGS.md#local-socket-api). Access is controlled by socket
// permissions; every call is attributed to the caller's Unix user.
package localapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1/localv1connect"
)

// Group is the Unix group whose members may use the socket (besides root).
const Group = "raptor"

type (
	callerKey struct{}
	rootKey   struct{}
)

// Caller returns who made the request, as "local:<username>".
func Caller(ctx context.Context) string {
	if c, ok := ctx.Value(callerKey{}).(string); ok {
		return c
	}
	return "local:unknown"
}

// IsRoot reports whether the request came from uid 0.
func IsRoot(ctx context.Context) bool {
	root, _ := ctx.Value(rootKey{}).(bool)
	return root
}

// Server serves the local API.
type Server struct {
	srv *http.Server
	ln  net.Listener
	log *slog.Logger
}

// Listen creates the socket at path (replacing a stale one), accessible to its
// owner and to members of group (Group in production). An empty group makes
// the socket owner-only.
func Listen(ctx context.Context, path, group string, svc localv1connect.LocalServiceHandler, log *slog.Logger) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the socket itself is 0660
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := restrict(path, group, log); err != nil {
		_ = ln.Close()
		return nil, err
	}

	mux := http.NewServeMux()
	mux.Handle(localv1connect.NewLocalServiceHandler(svc, connect.WithInterceptors(logCalls(log))))

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		Protocols:         new(http.Protocols),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			uid, ok := peerUID(c)
			ctx = context.WithValue(ctx, rootKey{}, ok && uid == 0)
			return context.WithValue(ctx, callerKey{}, callerName(c))
		},
	}
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	return &Server{srv: srv, ln: ln, log: log}, nil
}

// Serve serves until Shutdown is called.
func (s *Server) Serve() error {
	if err := s.srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting connections and waits for active calls to finish.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

// restrict sets the socket to 0660 with the given group, or 0600 owner-only
// if group is empty or doesn't exist.
func restrict(path, group string, log *slog.Logger) error {
	if group == "" {
		return os.Chmod(path, 0o600)
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		log.Warn("group not found; local socket is owner-only", "group", group)
		return os.Chmod(path, 0o600)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("group %s: %w", group, err)
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return err
	}
	return os.Chmod(path, 0o660) //nolint:gosec // group access is intended
}

func callerName(c net.Conn) string {
	uid, ok := peerUID(c)
	if !ok {
		return "local:unknown"
	}
	id := strconv.FormatUint(uint64(uid), 10)
	if u, err := user.LookupId(id); err == nil {
		return "local:" + u.Username
	}
	return "local:uid-" + id
}

// logCalls logs every call with its caller and outcome.
func logCalls(log *slog.Logger) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			log.Info("local api call",
				"procedure", req.Spec().Procedure,
				"caller", Caller(ctx),
				"duration", time.Since(start),
				"error", err)
			return resp, err
		}
	}
}
