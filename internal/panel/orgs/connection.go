package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// The connection test (docs/PANEL.md#connection-test): the Panel, being on
// the internet, tries each of a server's ports on its node's public address,
// the way a player would. It only ever dials a node's own recorded address
// and its server's allocated ports, never an address a user names.

const (
	probeTimeout = 5 * time.Second
	// closeWait is how long an accepted connection must stay open to count
	// as reaching the game.
	closeWait = 750 * time.Millisecond
	maxProbes = 16
	// testsPerMinute bounds how often one user can make the Panel dial out.
	testsPerMinute = 10
)

// probeAllocation is an allocation as the mirror has it.
type probeAllocation struct {
	IP      string `json:"ip"`
	Port    int32  `json:"port"`
	Primary bool   `json:"primary"`
}

func (s *Service) dial(ctx context.Context, addr string) (net.Conn, error) {
	if s.Dial != nil {
		return s.Dial(ctx, "tcp", addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func (s *Service) lookupAddr(ctx context.Context, ip string) ([]string, error) {
	if s.LookupAddr != nil {
		return s.LookupAddr(ctx, ip)
	}
	return net.DefaultResolver.LookupAddr(ctx, ip)
}

// probe tries one port: whether it answered, how fast, and why not.
func (s *Service) probe(ctx context.Context, ip netip.Addr, a probeAllocation) *panelv1.PortProbe {
	out := &panelv1.PortProbe{Port: a.Port, Primary: a.Primary}
	if a.IP == "127.0.0.1" {
		out.Failure = "local_only"
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	start := time.Now()
	c, err := s.dial(ctx, net.JoinHostPort(ip.String(), strconv.Itoa(int(a.Port))))
	switch {
	case err == nil:
		out.LatencyMs = int32(time.Since(start).Milliseconds()) //nolint:gosec // at most probeTimeout
		// A port forwarder in front of the game (Docker's userland proxy,
		// a router) accepts the connection even when nothing is behind it,
		// then hangs up at once. A game waits for the player to speak.
		_ = c.SetReadDeadline(time.Now().Add(closeWait))
		_, rerr := c.Read(make([]byte, 1))
		_ = c.Close()
		if rerr != nil && !isTimeout(rerr) {
			out.Failure = "closed"
			return out
		}
		out.Reachable = true
	case errors.Is(err, syscall.ECONNREFUSED):
		out.Failure = "refused"
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		out.Failure = "timeout"
	default:
		out.Failure = "unknown"
	}
	return out
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// TestConnection implements OrgService.
func (s *Service) TestConnection(ctx context.Context, req *panelv1.TestConnectionRequest) (*panelv1.TestConnectionResponse, error) {
	var allocs []probeAllocation
	var node store.Node
	var userID string
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		userID = uuid.UUID(sess.UserID.Bytes).String()
		_, nodeID, _, err := serverAccess(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId())
		if err != nil {
			return err
		}
		r, err := q.NodeServer(ctx, store.NodeServerParams{NodeID: nodeID, ServerID: req.GetServerId()})
		if err != nil {
			return errNoServer
		}
		if err := json.Unmarshal(r.Allocations, &allocs); err != nil {
			return err
		}
		node, err = s.q().GetNode(ctx, nodeID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RateLimit(ctx, "conntest:user:"+userID, testsPerMinute, time.Minute); err != nil {
		return nil, err
	}
	out := &panelv1.TestConnectionResponse{}
	var ip netip.Addr
	switch {
	case node.PublicIpv4 != nil:
		ip = *node.PublicIpv4
	case node.PublicIpv6 != nil:
		ip = *node.PublicIpv6
	default:
		return out, nil // the node hasn't told us where it is yet
	}
	out.Address = ip.String()
	if len(allocs) > maxProbes {
		allocs = allocs[:maxProbes]
	}
	out.Ports = make([]*panelv1.PortProbe, len(allocs))
	var wg sync.WaitGroup
	for i, a := range allocs {
		wg.Go(func() { out.Ports[i] = s.probe(ctx, ip, a) })
	}
	// The provider guess, while the probes run.
	lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	if names, err := s.lookupAddr(lctx, ip.String()); err == nil && len(names) > 0 {
		out.ReverseDns = strings.TrimSuffix(names[0], ".")
	}
	cancel()
	out.Provider = GuessProvider(out.ReverseDns)
	wg.Wait()
	// The primary port first.
	for i, p := range out.Ports {
		if p.GetPrimary() && i > 0 {
			out.Ports[0], out.Ports[i] = out.Ports[i], out.Ports[0]
		}
	}
	return out, nil
}

// providers maps reverse DNS endings to the hosts whose firewall guides
// the web app has.
var providers = []struct{ suffix, name string }{
	{"your-server.de", "hetzner"},
	{"hetzner.com", "hetzner"},
	{"hetzner.cloud", "hetzner"},
	{"ovh.net", "ovh"},
	{"ovh.ca", "ovh"},
	{"ovh.us", "ovh"},
	{"kimsufi.com", "ovh"},
	{"amazonaws.com", "aws"},
	{"googleusercontent.com", "gcp"},
	{"cloudapp.azure.com", "azure"},
	{"oraclecloud.com", "oracle"},
	{"oraclevcn.com", "oracle"},
	{"linodeusercontent.com", "linode"},
	{"linode.com", "linode"},
	{"vultrusercontent.com", "vultr"},
	{"vultr.com", "vultr"},
	{"digitalocean.com", "digitalocean"},
}

// homeHints are words home internet providers put in their customers'
// reverse DNS.
var homeHints = []string{"dynamic", "dyn.", "dsl", "cable", "dhcp", "pool", "res.", "residential", "fios", "broadband", "cpe", "hsd1", "rev.sfr", "home"}

// GuessProvider names the hosting provider a reverse DNS name belongs to,
// "home" for what looks like a home connection, or "" if it can't tell.
func GuessProvider(rdns string) string {
	n := strings.ToLower(rdns)
	if n == "" {
		return ""
	}
	for _, p := range providers {
		if n == p.suffix || strings.HasSuffix(n, "."+p.suffix) {
			return p.name
		}
	}
	for _, h := range homeHints {
		if strings.Contains(n, h) {
			return "home"
		}
	}
	return ""
}
