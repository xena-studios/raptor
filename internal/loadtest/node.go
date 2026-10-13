// Package loadtest is a fake Wings for load-testing the Panel
// (docs/RELIABILITY.md#load-tests): many nodes that link with real
// identities, keep the node connection up, serve the node API with fake
// servers, and report events at a set rate, so the Panel's connections,
// mirror, and database are measured under a fleet without a fleet.
package loadtest

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Identity is a node the Panel enrolled: what the simulator needs to link
// it again.
type Identity struct {
	NodeID   string `json:"node_id"`
	Key      []byte `json:"key"`       // the node's Ed25519 private key
	PanelKey []byte `json:"panel_key"` // the Panel's signing key, as enrollment returned it
}

// Load is how hard each node works.
type Load struct {
	// Servers is how many fake servers each node has.
	Servers int
	// EventEvery is how often a node changes a server and says so
	// (EventsAvailable); about one in ten changes is a config update the
	// Panel fetches with GetServers, the rest are state changes.
	EventEvery time.Duration
}

// keptEvents is how many events a node keeps for the Panel to pull; an
// older position gets a snapshot, as from Wings.
const keptEvents = 2000

// Node is one fake Wings.
type Node struct {
	id    Identity
	load  Load
	stats *Stats

	mu      sync.Mutex
	servers []*nodev1.Server
	events  []*nodev1.Event
	seq     int64
	sent    map[int64]time.Time // when each event not yet pulled was made
}

// NewNode makes a node with its fake servers.
func NewNode(id Identity, load Load, stats *Stats) *Node {
	n := &Node{id: id, load: load, stats: stats, sent: map[int64]time.Time{}}
	now := time.Now().UnixMilli()
	for i := range load.Servers {
		sid, _ := uuid.NewV7()
		n.servers = append(n.servers, &nodev1.Server{
			Id: sid.String(), Name: fmt.Sprintf("fake-%d", i+1), Version: 1, State: "running",
			DesiredState: "running", InstallState: "installed", EggName: "Fake",
			Config:    []byte(`{"image":"fake","startup":"fake","variables":{},"limits":{},"settings":{},"allocations":[]}`),
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return n
}

// Run keeps the node linked until ctx ends, reconnecting with backoff the
// way Wings does, and makes events while linked.
func (n *Node) Run(ctx context.Context, panelURL string, client *http.Client) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		s, err := nodelink.Dial(ctx, nodelink.DialConfig{
			URL: panelURL, NodeID: n.id.NodeID, NodeKey: ed25519.PrivateKey(n.id.Key),
			PanelKey: ed25519.PublicKey(n.id.PanelKey), Purpose: nodelink.PurposeControl, Software: "loadtest", HTTPClient: client,
		})
		if err != nil {
			n.stats.dialFailed(err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff + time.Duration(rand.Int64N(int64(backoff)))): //nolint:gosec // jitter and picks for a load test, not secrets
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		n.stats.connected(time.Since(start))
		n.linked(ctx, s)
		n.stats.disconnected()
	}
}

// linked serves the node API and makes events until the session ends.
func (n *Node) linked(ctx context.Context, s *nodelink.Session) {
	defer func() { _ = s.Close() }()
	mux := http.NewServeMux()
	mux.Handle(nodev1connect.NewNodeServiceHandler(&service{n: n}))
	go func() { _ = s.Serve(mux) }()
	panel := nodev1connect.NewPanelServiceClient(s.Client(), nodelink.BaseURL)
	every := n.load.EventEvery
	if every <= 0 || len(n.servers) == 0 {
		<-s.Done()
		return
	}
	// Spread the fleet's events out instead of every node at once.
	timer := time.NewTimer(time.Duration(rand.Int64N(int64(every)))) //nolint:gosec // jitter and picks for a load test, not secrets
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.Done():
			return
		case <-timer.C:
		}
		last := n.change()
		if _, err := panel.EventsAvailable(ctx, &nodev1.EventsAvailableRequest{LastSeq: last}); err != nil && ctx.Err() == nil {
			n.stats.rpcFailed("EventsAvailable", err)
		}
		timer.Reset(every)
	}
}

// change changes a random server and records the event; it returns the
// newest sequence number.
func (n *Node) change() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	srv := n.servers[rand.IntN(len(n.servers))] //nolint:gosec // jitter and picks for a load test, not secrets
	now := time.Now()
	n.seq++
	e := &nodev1.Event{Seq: n.seq, ServerId: srv.GetId(), At: now.UnixMilli()}
	if rand.IntN(10) == 0 { //nolint:gosec // as above
		srv.Version++
		srv.UpdatedAt = now.UnixMilli()
		e.Type, e.Version = "server.updated", srv.GetVersion()
		e.Data = []byte(`{}`)
	} else {
		srv.State = map[string]string{"running": "offline", "offline": "running"}[srv.GetState()]
		e.Type, e.Version = "server.state", srv.GetVersion()
		e.Data, _ = json.Marshal(map[string]string{"state": srv.GetState()})
	}
	n.events = append(n.events, e)
	if len(n.events) > keptEvents {
		for _, old := range n.events[:len(n.events)-keptEvents] {
			delete(n.sent, old.GetSeq())
		}
		n.events = n.events[len(n.events)-keptEvents:]
	}
	n.sent[e.GetSeq()] = now
	n.stats.event()
	return n.seq
}

// service is the node API, as the Panel calls it.
type service struct {
	nodev1connect.UnimplementedNodeServiceHandler
	n *Node
}

var errFake = errors.New("this is a load-test node: it runs nothing")

func (s *service) Events(_ context.Context, req *nodev1.EventsRequest) (*nodev1.EventsResponse, error) {
	n := s.n
	n.mu.Lock()
	defer n.mu.Unlock()
	after, limit := req.GetAfterSeq(), int(req.GetLimit())
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if len(n.events) > 0 && after < n.events[0].GetSeq()-1 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("those events are gone: take a snapshot"))
	}
	if after > n.seq {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("the Panel is ahead of the node: take a snapshot"))
	}
	out := &nodev1.EventsResponse{LastSeq: n.seq}
	now := time.Now()
	for _, e := range n.events {
		if e.GetSeq() <= after {
			continue
		}
		if len(out.Events) == limit {
			break
		}
		out.Events = append(out.Events, e)
		if t, ok := n.sent[e.GetSeq()]; ok {
			n.stats.delivered(now.Sub(t))
			delete(n.sent, e.GetSeq())
		}
	}
	n.stats.call("Events")
	return out, nil
}

func (s *service) GetServers(_ context.Context, req *nodev1.GetServersRequest) (*nodev1.GetServersResponse, error) {
	n := s.n
	n.mu.Lock()
	defer n.mu.Unlock()
	out := &nodev1.GetServersResponse{LastSeq: n.seq}
	want := map[string]bool{}
	for _, id := range req.GetIds() {
		want[id] = true
	}
	for _, srv := range n.servers {
		if len(want) == 0 || want[srv.GetId()] {
			// A copy: change() edits the node's own while this one is sent.
			out.Servers = append(out.Servers, proto.CloneOf(srv))
			delete(want, srv.GetId())
		}
	}
	for id := range want {
		out.Missing = append(out.Missing, id)
	}
	n.stats.call("GetServers")
	return out, nil
}

func (s *service) Execute(context.Context, *nodev1.ExecuteRequest) (*nodev1.ExecuteResponse, error) {
	s.n.stats.call("Execute")
	return &nodev1.ExecuteResponse{Error: errFake.Error()}, nil
}
