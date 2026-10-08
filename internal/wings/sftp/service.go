package sftp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"sync"

	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// EventNode is recorded whenever SFTP is turned on or off or moves to a new
// port, so the Panel shows the real port and the host key fingerprint.
const EventNode = "node.sftp"

// Port range tried when the preferred port is taken (docs/DECISIONS.md #37).
const (
	portFirst = 2022
	portLast  = 2099
)

// kv keys.
const (
	kvEnabled = "sftp.enabled"
	kvPort    = "sftp.port"
)

// ServiceOptions configure a Service.
type ServiceOptions struct {
	Options
	Store *store.DB
	// Port is the preferred port (ports.sftp in config.yml).
	Port int
	// Allocated returns ports allocated to servers, which SFTP never takes
	// even while those servers are stopped.
	Allocated func(context.Context) ([]int, error)
	// Listen opens the listener; net.Listen by default.
	Listen func(network, addr string) (net.Listener, error)
}

// Status is SFTP's state on the node.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Port        int    `json:"port,omitempty"` // 0 while off
	Fingerprint string `json:"host_key_fingerprint"`
}

// Service runs the SFTP server while it's enabled for the node. Enabled is
// stored in SQLite and set by the Panel; SFTP is off by default.
type Service struct {
	o ServiceOptions

	mu   sync.Mutex
	srv  *Server
	port int
	done chan struct{} // closed when Serve returns
}

// NewService returns a Service. Call Start to resume the stored state.
func NewService(o ServiceOptions) *Service {
	if o.Listen == nil {
		o.Listen = net.Listen
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o}
}

// Start turns SFTP on if it was enabled before Wings stopped.
func (s *Service) Start(ctx context.Context) error {
	v, err := s.o.Store.Read.GetKV(ctx, kvEnabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && string(v) != "1") {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.SetEnabled(ctx, true)
	return err
}

// Status returns the current state.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Service) statusLocked() Status {
	return Status{Enabled: s.srv != nil, Port: s.port, Fingerprint: Fingerprint(s.o.HostKey)}
}

// SetEnabled turns SFTP on or off and stores the choice. Turning it on
// listens on the port it used last, else the preferred port, else the first
// free port in 2022–2099.
func (s *Service) SetEnabled(ctx context.Context, enabled bool) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if enabled == (s.srv != nil) {
		return s.statusLocked(), s.store(ctx, enabled)
	}
	if !enabled {
		s.stopLocked()
		return s.statusLocked(), errors.Join(s.store(ctx, false), s.record(ctx))
	}
	ln, port, err := s.listen(ctx)
	if err != nil {
		return s.statusLocked(), err
	}
	srv := New(s.o.Options)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil {
			s.o.Log.Error("sftp server stopped", "err", err)
		}
	}()
	s.srv, s.port, s.done = srv, port, done
	s.o.Log.Info("sftp listening", "port", port, "host_key", Fingerprint(s.o.HostKey))
	if err := s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		return q.SetKV(ctx, store.SetKVParams{Key: kvPort, Value: []byte(strconv.Itoa(port))})
	}); err != nil {
		return s.statusLocked(), err
	}
	return s.statusLocked(), errors.Join(s.store(ctx, true), s.record(ctx))
}

// Disconnect drops a server's connections.
func (s *Service) Disconnect(serverID string) int {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return 0
	}
	return srv.Disconnect(serverID)
}

// DisconnectLogin drops one login's connections to a server.
func (s *Service) DisconnectLogin(serverID, username string) int {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return 0
	}
	return srv.DisconnectLogin(serverID, username)
}

// Close stops the server without changing the stored state.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Service) stopLocked() {
	if s.srv == nil {
		return
	}
	_ = s.srv.Close()
	<-s.done
	s.srv, s.port, s.done = nil, 0, nil
}

func (s *Service) store(ctx context.Context, enabled bool) error {
	v := "0"
	if enabled {
		v = "1"
	}
	return s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		return q.SetKV(ctx, store.SetKVParams{Key: kvEnabled, Value: []byte(v)})
	})
}

func (s *Service) record(ctx context.Context) error {
	if s.o.Events == nil {
		return nil
	}
	st := s.statusLocked()
	_, err := s.o.Events.Append(ctx, events.Event{Type: EventNode, Data: map[string]any{
		"enabled": st.Enabled, "port": st.Port, "host_key_fingerprint": st.Fingerprint,
	}})
	return err
}

// listen binds the first usable port: the one used last (so users' saved
// connections keep working), the preferred one, then 2022–2099. Ports
// allocated to servers are skipped even when nothing listens on them yet.
func (s *Service) listen(ctx context.Context) (net.Listener, int, error) {
	var ports []int
	if v, err := s.o.Store.Read.GetKV(ctx, kvPort); err == nil {
		if p, err := strconv.Atoi(string(v)); err == nil {
			ports = append(ports, p)
		}
	}
	ports = append(ports, s.o.Port)
	for p := portFirst; p <= portLast; p++ {
		ports = append(ports, p)
	}
	var allocated []int
	if s.o.Allocated != nil {
		var err error
		if allocated, err = s.o.Allocated(ctx); err != nil {
			return nil, 0, err
		}
	}
	var errs []error
	for i, p := range ports {
		if p < 1 || p > 65535 || slices.Contains(ports[:i], p) || slices.Contains(allocated, p) {
			continue
		}
		ln, err := s.o.Listen("tcp", ":"+strconv.Itoa(p))
		if err == nil {
			return ln, p, nil
		}
		errs = append(errs, err)
	}
	return nil, 0, fmt.Errorf("sftp: no free port (preferred %d, then %d–%d): %w", s.o.Port, portFirst, portLast, errors.Join(errs...))
}
