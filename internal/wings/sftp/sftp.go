// Package sftp is Wings' built-in SFTP server (docs/WINGS.md#files-and-sftp).
// It's off by default and enabled per node. Users log in as user.serverid
// with a password or SSH key, which the Panel checks; accepted keys are
// cached so key logins keep working while the Panel is unreachable. Each
// session is confined to the server's directory with os.Root.
package sftp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/wings/events"
	wfiles "github.com/xena-studios/raptor/internal/wings/files"
)

// EventLogin is recorded for every SFTP login, for the Panel's audit log.
const EventLogin = "server.sftp.login"

// Connection limits. Logins are cheap to attempt and each one may ask the
// Panel, so failures are rate limited per address.
const (
	handshakeTimeout = 30 * time.Second
	authTimeout      = 10 * time.Second // per Panel check
	maxConns         = 512
	maxConnsPerAddr  = 32
	maxSessions      = 8 // SFTP sessions per connection
	maxAuthTries     = 6 // per connection
	failWindow       = 5 * time.Minute
	maxFailures      = 30 // per address per window, then refused until it ends
)

// Options configure a Server.
type Options struct {
	HostKey ssh.Signer
	Auth    Authenticator
	Keys    *KeyCache // nil: no cache, key logins need the Panel
	Servers Servers
	Events  *events.Outbox // nil: logins aren't recorded
	// UID and GID own what SFTP creates: the servers' user.
	UID, GID int
	Log      *slog.Logger
}

// Server accepts SFTP connections.
type Server struct {
	o   Options
	cfg *ssh.ServerConfig

	mu       sync.Mutex
	closed   bool
	lns      map[net.Listener]struct{}
	conns    map[*conn]struct{}
	perAddr  map[netip.Addr]int
	failures map[netip.Addr]*failures
	wg       sync.WaitGroup
}

type conn struct {
	net.Conn
	serverID string
	username string
}

type failures struct {
	n     int
	since time.Time
}

// session is who logged in; it rides in ssh.Permissions from the auth
// callback that accepted the login, so the grant is always the one for the
// key or password that was actually verified.
type session struct {
	login  Login
	grant  Grant
	method string
	cached bool // accepted from the key cache while the Panel was unreachable
}

const sessionKey = "raptor.session"

// New returns a Server.
func New(o Options) *Server {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	s := &Server{
		o:        o,
		lns:      map[net.Listener]struct{}{},
		conns:    map[*conn]struct{}{},
		perAddr:  map[netip.Addr]int{},
		failures: map[netip.Addr]*failures{},
	}
	s.cfg = &ssh.ServerConfig{
		ServerVersion:     "SSH-2.0-Raptor",
		MaxAuthTries:      maxAuthTries,
		PasswordCallback:  s.password,
		PublicKeyCallback: s.publicKey,
	}
	s.cfg.AddHostKey(o.HostKey)
	return s
}

// Serve accepts connections on ln until Close. It returns nil after Close.
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	s.lns[ln] = struct{}{}
	s.mu.Unlock()
	for {
		nc, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			delete(s.lns, ln)
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		c := &conn{Conn: nc}
		addr := remoteAddr(nc.RemoteAddr())
		if !s.admit(c, addr) {
			_ = nc.Close()
			continue
		}
		s.wg.Go(func() {
			defer s.release(c, addr)
			s.handle(c, addr)
		})
	}
}

// Close stops listening, drops every connection, and waits for them.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	for ln := range s.lns {
		_ = ln.Close()
	}
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

// Disconnect drops every connection to a server, e.g. when it's deleted or
// an install starts over its files. It returns how many were dropped.
func (s *Server) Disconnect(serverID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.conns {
		if c.serverID == serverID {
			_ = c.Close()
			n++
		}
	}
	return n
}

// DisconnectLogin drops one login's connections to a server, e.g. when its
// temporary password is revoked. It returns how many were dropped.
func (s *Server) DisconnectLogin(serverID, username string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.conns {
		if c.serverID == serverID && c.username == username {
			_ = c.Close()
			n++
		}
	}
	return n
}

func remoteAddr(a net.Addr) netip.Addr {
	if ap, err := netip.ParseAddrPort(a.String()); err == nil {
		return ap.Addr().Unmap()
	}
	return netip.Addr{}
}

func (s *Server) admit(c *conn, addr netip.Addr) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.conns) >= maxConns || s.perAddr[addr] >= maxConnsPerAddr {
		return false
	}
	if f := s.failures[addr]; f != nil {
		if time.Since(f.since) > failWindow {
			delete(s.failures, addr)
		} else if f.n >= maxFailures {
			return false
		}
	}
	s.conns[c] = struct{}{}
	s.perAddr[addr]++
	return true
}

func (s *Server) release(c *conn, addr netip.Addr) {
	_ = c.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
	if s.perAddr[addr]--; s.perAddr[addr] <= 0 {
		delete(s.perAddr, addr)
	}
}

func (s *Server) failed(addr netip.Addr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[addr]
	if f == nil || time.Since(f.since) > failWindow {
		f = &failures{since: time.Now()}
		s.failures[addr] = f
	}
	f.n++
	// Forget stale entries so the map can't grow without bound.
	if len(s.failures) > 10_000 {
		for a, f := range s.failures {
			if time.Since(f.since) > failWindow {
				delete(s.failures, a)
			}
		}
	}
}

func (s *Server) handle(c *conn, addr netip.Addr) {
	_ = c.SetDeadline(time.Now().Add(handshakeTimeout))
	sc, chans, reqs, err := ssh.NewServerConn(c, s.cfg)
	if err != nil {
		s.o.Log.Debug("sftp handshake failed", "addr", addr, "err", err)
		return
	}
	_ = c.SetDeadline(time.Time{})
	sess, ok := sc.Permissions.ExtraData[sessionKey].(*session)
	if !ok {
		return
	}

	s.mu.Lock()
	c.serverID, c.username = sess.login.ServerID, sess.login.Username
	s.mu.Unlock()
	// A login that runs out (a temporary password) ends with it.
	if !sess.grant.ExpiresAt.IsZero() {
		t := time.AfterFunc(time.Until(sess.grant.ExpiresAt), func() { _ = sc.Close() })
		defer t.Stop()
	}
	// A delete or install that started during the handshake is caught by
	// the per-request checks.
	s.o.Log.Info("sftp login", "server", sess.login.ServerID, "user", sess.login.Username, "addr", addr, "method", sess.method, "cached", sess.cached)
	if s.o.Events != nil {
		if _, err := s.o.Events.Append(context.Background(), events.Event{
			Type: EventLogin, ServerID: sess.login.ServerID,
			Data: map[string]any{"user": sess.login.Username, "user_id": sess.grant.UserID, "ip": addr.String(), "method": sess.method, "cached": sess.cached},
		}); err != nil {
			s.o.Log.Error("recording sftp login failed", "err", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ssh.DiscardRequests(reqs) // no port forwarding or other global requests
	var wg sync.WaitGroup
	defer wg.Wait()
	defer func() { _ = sc.Close() }() // ends the sessions before waiting for them
	sessions := 0
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sftp sessions are supported")
			continue
		}
		if sessions >= maxSessions {
			_ = nc.Reject(ssh.ResourceShortage, "too many sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		sessions++
		wg.Go(func() { s.session(ctx, sess, ch, chReqs) })
	}
}

// session serves one channel: only the sftp subsystem, never a shell,
// command, or terminal.
func (s *Server) session(ctx context.Context, sess *session, ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()
	for req := range reqs {
		if req.Type != "subsystem" || string(req.Payload[min(4, len(req.Payload)):]) != "sftp" {
			_ = req.Reply(false, nil)
			continue
		}
		fsys, err := wfiles.OpenServer(s.o.Servers, sess.login.ServerID, s.o.UID, s.o.GID)
		if err != nil {
			s.o.Log.Warn("sftp session refused", "server", sess.login.ServerID, "err", err)
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		go ssh.DiscardRequests(reqs)
		h := &files{ctx: ctx, fs: fsys, serverID: sess.login.ServerID, grant: sess.grant, servers: s.o.Servers}
		rs := sftp.NewRequestServer(ch, h.handlers())
		if err := rs.Serve(); err != nil && !errors.Is(err, os.ErrClosed) {
			s.o.Log.Debug("sftp session ended", "server", sess.login.ServerID, "err", err)
		}
		_ = rs.Close()
		_ = fsys.Close()
		return
	}
}

// login parses and resolves the username. The error never says whether the
// server exists: that would let anyone probe server IDs.
func (s *Server) login(meta ssh.ConnMetadata) (Login, error) {
	user, ref, ok := parseUsername(meta.User())
	if !ok {
		return Login{}, errors.New("username must be user.serverid")
	}
	id, err := s.o.Servers.Resolve(ref)
	if err != nil {
		return Login{}, fmt.Errorf("resolve %q: %w", ref, err)
	}
	return Login{Username: user, ServerID: id, Addr: remoteAddr(meta.RemoteAddr())}, nil
}

func (s *Server) password(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
	l, err := s.login(meta)
	if err != nil {
		return s.reject(meta, "password", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()
	g, err := s.o.Auth.Password(ctx, l, string(pw))
	if err != nil {
		return s.reject(meta, "password", err)
	}
	return s.accept(meta, &session{login: l, grant: g, method: "password"})
}

// publicKey checks a key with the Panel. It's also called for keys the
// client only offers without proving it holds them; nothing here depends on
// that proof except the accepted session, which ssh only uses for the key
// that was verified.
func (s *Server) publicKey(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	l, err := s.login(meta)
	if err != nil {
		return s.reject(meta, "publickey", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), authTimeout)
	defer cancel()
	g, err := s.o.Auth.PublicKey(ctx, l, key)
	cached := false
	switch {
	case err == nil:
		if s.o.Keys != nil {
			if err := s.o.Keys.Put(ctx, l, key, g); err != nil {
				s.o.Log.Error("caching sftp key failed", "err", err)
			}
		}
	case errors.Is(err, ErrDenied):
		// The Panel is the authority whenever it answers.
		if s.o.Keys != nil {
			if err := s.o.Keys.Delete(ctx, l, key); err != nil {
				s.o.Log.Error("forgetting sftp key failed", "err", err)
			}
		}
		return s.reject(meta, "publickey", err)
	case errors.Is(err, ErrUnavailable) && s.o.Keys != nil:
		var ok bool
		var cerr error
		g, ok, cerr = s.o.Keys.Get(ctx, l, key)
		if cerr != nil || !ok {
			return s.reject(meta, "publickey", errors.Join(err, cerr))
		}
		cached = true
	default:
		return s.reject(meta, "publickey", err)
	}
	return s.accept(meta, &session{login: l, grant: g, method: "publickey", cached: cached})
}

func (s *Server) accept(meta ssh.ConnMetadata, sess *session) (*ssh.Permissions, error) {
	if !sess.grant.Has(PermSFTP) {
		return s.reject(meta, sess.method, errors.New("no sftp permission"))
	}
	return &ssh.Permissions{ExtraData: map[any]any{sessionKey: sess}}, nil
}

func (s *Server) reject(meta ssh.ConnMetadata, method string, err error) (*ssh.Permissions, error) {
	s.failed(remoteAddr(meta.RemoteAddr()))
	s.o.Log.Debug("sftp login rejected", "user", meta.User(), "addr", meta.RemoteAddr(), "method", method, "err", err)
	return nil, errors.New("login failed")
}
