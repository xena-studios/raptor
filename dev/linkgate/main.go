// Command linkgate runs the Phase 3.0 validation gate (docs/ROADMAP.md): the
// real node connection code, Panel side and Wings side, with Cloudflare in
// between, and checks what the gate asks for:
//
//   - rpc:      calls both ways through Cloudflare
//   - cut:      the connection cut mid-command; Wings reconnects, the retry
//     gets the result, and the command ran once
//   - transfer: console-sized calls on the main connection while a large
//     upload runs on its transfer connection (latency before and during)
//   - idle:     the connection left idle; every drop is logged
//
// The Panel side listens on -listen; by default a Cloudflare quick tunnel
// (cloudflared must be installed) gives it a public https URL. With
// -public-url, something else already routes that URL to -listen (a named
// tunnel or a proxied hostname on a real zone).
//
//	go run ./dev/linkgate                     # quick tunnel; rpc, cut, transfer
//	go run ./dev/linkgate -idle 4h            # then hold it idle 4 h
//	go run ./dev/linkgate -listen 127.0.0.1:8080 -public-url https://api.example.net
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/shared/nodelink/nodelinktest"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/link"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const nodeID = "gate-node"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "linkgate:", err)
		os.Exit(1)
	}
}

// keyFile is the Panel's and the node's keys, fresh for each run.
type keyFile struct {
	Panel ed25519.PrivateKey
	Node  ed25519.PrivateKey
}

func newKeys() keyFile {
	var k keyFile
	_, k.Panel, _ = ed25519.GenerateKey(rand.Reader)
	_, k.Node, _ = ed25519.GenerateKey(rand.Reader)
	return k
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:0", "the Panel side's address")
	publicURL := flag.String("public-url", "", "a public URL already routed to -listen through Cloudflare (default: start a quick tunnel)")
	tests := flag.String("tests", "rpc,cut,transfer", "tests to run: rpc, cut, transfer")
	uploadSize := flag.Int64("upload", 1<<30, "upload size for the transfer test, in bytes")
	idle := flag.Duration("idle", 0, "then hold the connection idle this long, logging every drop")
	verbose := flag.Bool("v", false, "log the connection code's messages")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	keys := newKeys()
	g := &gate{log: log, keys: keys, announced: make(chan int64, 100)}
	g.hub = &nodes.Hub{
		PanelKey: keys.Panel,
		NodeKey: func(_ context.Context, id string) (ed25519.PublicKey, error) {
			if id != nodeID {
				return nil, nodelink.ErrUnknownNode
			}
			return keys.Node.Public().(ed25519.PublicKey), nil
		},
		EventsAvailable: func(_ context.Context, _ string, last int64) {
			select {
			case g.announced <- last:
			default:
			}
		},
		Log: log,
	}
	defer g.hub.Close()

	addr, err := g.startPanel(ctx, *listen)
	if err != nil {
		return err
	}
	panelURL := *publicURL
	if panelURL == "" {
		// Quick tunnels are best effort; try a few times.
		for attempt := 1; ; attempt++ {
			if panelURL, err = quickTunnel(ctx, addr); err == nil || attempt == 3 || ctx.Err() != nil {
				break
			}
			fmt.Fprintf(os.Stderr, "quick tunnel: %v; retrying\n", err)
		}
		if err != nil {
			return err
		}
	}
	fmt.Printf("Panel: %s → %s\n", panelURL, addr)

	if err := g.startNode(ctx, panelURL); err != nil {
		return err
	}
	defer g.stopNode()
	t0 := time.Now()
	wctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	_, err = g.hub.Wait(wctx, nodeID)
	cancel()
	if err != nil {
		return fmt.Errorf("the node didn't connect through Cloudflare: %w (link: %+v)", err, g.link.Status())
	}
	fmt.Printf("connected in %s\n\n", time.Since(t0).Round(time.Millisecond))

	failed := false
	for _, name := range strings.Split(*tests, ",") {
		var err error
		switch name {
		case "rpc":
			err = g.testRPC(ctx)
		case "cut":
			err = g.testCut(ctx)
		case "transfer":
			err = g.testTransfer(ctx, *uploadSize)
		default:
			err = fmt.Errorf("unknown test %q", name)
		}
		if err != nil {
			fmt.Printf("FAIL %s: %v\n\n", name, err)
			failed = true
		} else {
			fmt.Printf("PASS %s\n\n", name)
		}
	}
	if *idle > 0 {
		g.holdIdle(ctx, *idle)
	}
	if failed {
		return errors.New("some tests failed")
	}
	return nil
}

type gate struct {
	log  *slog.Logger
	keys keyFile
	hub  *nodes.Hub

	link      *link.Link
	proxy     *nodelinktest.Proxy
	db        *store.DB
	outbox    *events.Outbox
	stopLink  context.CancelFunc
	linkDone  chan struct{}
	runs      atomic.Int32
	slow      chan struct{} // the slow command started
	release   chan struct{}
	announced chan int64
	sink      *sink
}

func (g *gate) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET "+nodelink.Path, g.hub)
	return mux
}

func (g *gate) startPanel(ctx context.Context, addr string) (string, error) {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	srv := &http.Server{Handler: g.mux(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() { <-ctx.Done(); _ = srv.Close() }()
	return ln.Addr().String(), nil
}

var tunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

// quickTunnel starts a Cloudflare quick tunnel to addr and returns its URL
// once it answers. The tunnel lasts until ctx ends.
func quickTunnel(ctx context.Context, addr string) (u string, err error) {
	cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--no-autoupdate", "--url", "http://"+addr) //nolint:gosec // our own listener's address
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting cloudflared (brew install cloudflared): %w", err)
	}
	defer func() {
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	found := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if u := tunnelURL.FindString(sc.Text()); u != "" {
				select {
				case found <- u:
				default:
				}
			}
		}
	}()
	select {
	case u = <-found:
	case <-time.After(time.Minute):
		return "", errors.New("cloudflared didn't print a tunnel URL")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	// The hostname takes a moment to resolve and route.
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u+"/healthz", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return u, nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("the tunnel %s never answered", u)
}

// startNode runs the real link with a real executor, through a local TCP
// proxy to Cloudflare's edge (so the connection can be cut).
func (g *gate) startNode(ctx context.Context, panelURL string) error {
	u, err := url.Parse(panelURL)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "linkgate")
	if err != nil {
		return err
	}
	if g.db, err = store.Open(ctx, filepath.Join(dir, "state.db")); err != nil {
		return err
	}
	g.outbox = events.New(g.db)
	x := &command.Executor{DB: g.db, NodeID: nodeID, PanelKey: g.keys.Panel.Public().(ed25519.PublicKey), Log: g.log}
	x.Register("gate.ping", command.Handler{Signed: command.Never, ReadOnly: true, Run: func(context.Context, command.Envelope) (any, error) {
		return map[string]int64{"at": time.Now().UnixNano()}, nil
	}})
	g.slow, g.release = make(chan struct{}, 1), make(chan struct{})
	x.Register("gate.slow", command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		g.runs.Add(1)
		g.slow <- struct{}{}
		<-g.release
		return map[string]string{"done": "yes"}, nil
	}})

	port := u.Port()
	if port == "" {
		port = "443"
	}
	if g.proxy, err = nodelinktest.Listen(net.JoinHostPort(u.Hostname(), port)); err != nil {
		return err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return new(net.Dialer).DialContext(ctx, network, g.proxy.Addr)
	}
	g.sink = &sink{}
	g.link = link.New(link.Config{
		PanelURL: panelURL, NodeID: nodeID, NodeKey: g.keys.Node, PanelKey: g.keys.Panel.Public().(ed25519.PublicKey),
		Software: "linkgate", Commands: x, Events: g.outbox, Log: g.log, HTTPClient: &http.Client{Transport: tr},
		Transfers: func() link.Transfers { return g.sink },
	})
	lctx, cancel := context.WithCancel(ctx)
	g.stopLink, g.linkDone = cancel, make(chan struct{})
	go func() { _ = g.link.Run(lctx); close(g.linkDone) }()
	return nil
}

func (g *gate) stopNode() {
	g.stopLink()
	<-g.linkDone
	g.proxy.Close()
	_ = g.db.Close()
}

func (g *gate) envelope(action string) []byte {
	id, _ := uuid.NewV7()
	e := command.Envelope{CommandID: id.String(), NodeID: nodeID, UserID: "gate", Action: action, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ExpiresAt: e.ExpiresAt}
	p, _ := e.Grant.Payload()
	e.Grant.Signature = ed25519.Sign(g.keys.Panel, p)
	b, _ := json.Marshal(e)
	return b
}

// call is one console-sized round trip on the main connection.
func (g *gate) call(ctx context.Context) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	t := time.Now()
	res, err := g.hub.Execute(ctx, nodeID, g.envelope("gate.ping"))
	if err == nil && res.GetError() != "" {
		err = errors.New(res.GetError())
	}
	return time.Since(t), err
}

func (g *gate) testRPC(ctx context.Context) error {
	fmt.Println("== rpc: Panel → Wings commands, Wings → Panel event announcements")
	var lat []time.Duration
	for range 20 {
		d, err := g.call(ctx)
		if err != nil {
			return err
		}
		lat = append(lat, d)
	}
	fmt.Printf("   20 commands: %s\n", stats(lat))
	// Drain announcements from connecting, then append one.
	for len(g.announced) > 0 {
		<-g.announced
	}
	t := time.Now()
	seq, err := g.outbox.Append(ctx, events.Event{Type: "gate.test"})
	if err != nil {
		return err
	}
	for {
		select {
		case last := <-g.announced:
			if last >= seq {
				fmt.Printf("   event announced to the Panel after %s\n", time.Since(t).Round(time.Millisecond))
				c, _ := g.hub.Conn(nodeID)
				res, err := c.Node.Events(ctx, &nodev1.EventsRequest{AfterSeq: seq - 1})
				if err != nil || len(res.GetEvents()) != 1 {
					return fmt.Errorf("pulling the event (%v): %w", res, err)
				}
				return nil
			}
		case <-time.After(30 * time.Second):
			return errors.New("the event wasn't announced")
		}
	}
}

func (g *gate) testCut(ctx context.Context) error {
	fmt.Println("== cut: the connection cut mid-command")
	env := g.envelope("gate.slow")
	type result struct {
		res *nodev1.ExecuteResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		res, err := g.hub.Execute(cctx, nodeID, env)
		done <- result{res, err}
	}()
	select {
	case <-g.slow:
	case <-time.After(30 * time.Second):
		return errors.New("the command didn't start")
	}
	first, _ := g.hub.Conn(nodeID)
	t := time.Now()
	g.proxy.Cut()
	// The Panel side learns of the cut from Cloudflare (or its pings).
	var reconnected time.Duration
	for reconnected == 0 {
		if c, ok := g.hub.Conn(nodeID); ok && c != first {
			reconnected = time.Since(t)
		}
		if time.Since(t) > 3*time.Minute {
			return fmt.Errorf("the node didn't reconnect (link: %+v)", g.link.Status())
		}
		time.Sleep(50 * time.Millisecond)
	}
	fmt.Printf("   reconnected %s after the cut\n", reconnected.Round(time.Millisecond))
	close(g.release)
	r := <-done
	if r.err != nil {
		return r.err
	}
	if string(r.res.GetResult()) != `{"done":"yes"}` {
		return fmt.Errorf("result %q", r.res.GetResult())
	}
	if n := g.runs.Load(); n != 1 {
		return fmt.Errorf("the command ran %d times", n)
	}
	fmt.Printf("   the retry got the result; the command ran once (duplicate=%v)\n", r.res.GetDuplicate())
	return nil
}

// sink is a transfer target that discards what it's sent.
type sink struct {
	mu       sync.Mutex
	size     int64
	received int64
}

func (s *sink) HasTransfer(context.Context, string) bool { return true }

func (s *sink) WriteChunk(_ context.Context, id string, offset int64, r io.Reader) (files.Upload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up := files.Upload{ID: id, Size: s.size, Received: s.received, MaxChunk: files.MaxChunk}
	if offset != s.received {
		return up, files.ErrOffset
	}
	n, err := io.Copy(io.Discard, r)
	if err != nil {
		return up, err
	}
	s.received += n
	up.Received, up.Done = s.received, s.received == s.size
	return up, nil
}

func (s *sink) ReadChunk(context.Context, string, int64, int64, io.Writer) (int64, error) {
	return 0, files.ErrTransferNotFound
}

func (g *gate) testTransfer(ctx context.Context, size int64) error {
	fmt.Printf("== transfer: console latency while %d MiB uploads on its own connection\n", size>>20)
	base, err := g.sample(ctx, 10*time.Second, nil)
	if err != nil {
		return err
	}
	fmt.Printf("   idle:        %s\n", stats(base))

	g.sink.size = size
	tr, err := g.hub.OpenTransfer(ctx, nodeID, "gate-upload")
	if err != nil {
		return err
	}
	defer func() { _ = tr.Close() }()
	uploaded := make(chan error, 1)
	start := time.Now()
	go func() {
		chunk := make([]byte, files.MaxChunk)
		_, _ = rand.Read(chunk)
		for off := int64(0); off < size; {
			n := min(int64(len(chunk)), size-off)
			st, err := tr.Upload(ctx, off, bytes.NewReader(chunk[:n]))
			if err != nil {
				uploaded <- fmt.Errorf("chunk at %d: %w", off, err)
				return
			}
			off = st.Received
		}
		uploaded <- nil
	}()
	var upErr error
	during, err := g.sample(ctx, 0, func() bool {
		select {
		case upErr = <-uploaded:
			return true
		default:
			return false
		}
	})
	if err != nil {
		return err
	}
	if upErr != nil {
		return upErr
	}
	secs := time.Since(start).Seconds()
	fmt.Printf("   uploading:   %s\n", stats(during))
	fmt.Printf("   upload: %d MiB in %.0f s (%.1f MB/s)\n", size>>20, secs, float64(size)/secs/1e6)
	// Acceptable: the console's p95 rises by at most 200 ms during a transfer.
	if p(during, 95) > p(base, 95)+200*time.Millisecond {
		return fmt.Errorf("console p95 went from %s to %s during the upload", p(base, 95), p(during, 95))
	}
	return nil
}

// sample makes a call every 250 ms for d, or until done reports true.
func (g *gate) sample(ctx context.Context, d time.Duration, done func() bool) ([]time.Duration, error) {
	var out []time.Duration
	end := time.Now().Add(d)
	for {
		if done != nil && done() || done == nil && time.Now().After(end) {
			return out, nil
		}
		lat, err := g.call(ctx)
		if err != nil {
			return out, err
		}
		out = append(out, lat)
		time.Sleep(250 * time.Millisecond)
	}
}

func (g *gate) holdIdle(ctx context.Context, d time.Duration) {
	fmt.Printf("== idle: holding the connection for %s (Ctrl-C ends it early)\n", d)
	start := time.Now()
	last := g.link.Status()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	report := time.NewTicker(15 * time.Minute)
	defer report.Stop()
	drops := 0
	for time.Since(start) < d {
		select {
		case <-ctx.Done():
			d = time.Since(start)
		case <-report.C:
			lat, err := g.call(ctx)
			fmt.Printf("   %s: a call took %s (err %v), %d drops so far\n", time.Since(start).Round(time.Minute), lat.Round(time.Millisecond), err, drops)
		case <-tick.C:
			st := g.link.Status()
			if st.Reconnects != last.Reconnects || st.State != last.State {
				if st.State != link.Connected || st.Reconnects != last.Reconnects {
					if st.Reconnects != last.Reconnects {
						drops++
					}
					fmt.Printf("   %s: %s (reconnects %d) %s\n", time.Since(start).Round(time.Second), st.State, st.Reconnects, st.LastError)
				}
				last = st
			}
		}
	}
	fmt.Printf("   held %s: %d drops (%.1f per hour)\n", d.Round(time.Second), drops, float64(drops)/max(d.Hours(), 1e-9))
}

func p(ds []time.Duration, pct int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := slices.Clone(ds)
	slices.Sort(s)
	return s[min(len(s)-1, len(s)*pct/100)]
}

func stats(ds []time.Duration) string {
	return fmt.Sprintf("n=%d p50 %s, p95 %s, max %s", len(ds),
		p(ds, 50).Round(time.Millisecond), p(ds, 95).Round(time.Millisecond), p(ds, 100).Round(time.Millisecond))
}
