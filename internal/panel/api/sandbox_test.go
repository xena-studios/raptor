package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/demo"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
	limits "github.com/xena-studios/raptor/internal/shared/demo"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// demoNodeFake is the demo node as the Panel sees it: it checks every
// grant, and the demo key's signature on what needs one, as Wings does
// (Wings' own tests cover which actions it takes the key for), and keeps
// sandboxes.
type demoNodeFake struct {
	t        *testing.T
	panelKey ed25519.PublicKey
	demoKey  ed25519.PublicKey
	mu       sync.Mutex
	got      []nodecmd.Envelope
	boxes    map[string]map[string]any
	next     int
}

func (n *demoNodeFake) Execute(_ context.Context, node string, raw []byte) (*nodev1.ExecuteResponse, error) {
	var e nodecmd.Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, err
	}
	p, _ := e.Grant.Payload()
	if !ed25519.Verify(n.panelKey, p, e.Grant.Signature) || e.Grant.NodeID != node {
		n.t.Errorf("bad grant on %s", e.Action)
		return nil, errors.New("bad grant")
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, e)
	signed := e.Action == "server.create" || e.Action == "server.delete" || e.Action == "backup.restore"
	if signed {
		h, _ := e.Hash()
		if len(e.Demo) == 0 || !ed25519.Verify(n.demoKey, nodecmd.DemoPayload(h), e.Demo) {
			return &nodev1.ExecuteResponse{Error: "this action must be signed"}, nil
		}
	}
	ok := func(v any) (*nodev1.ExecuteResponse, error) {
		b, _ := json.Marshal(v)
		return &nodev1.ExecuteResponse{Result: b}, nil
	}
	switch e.Action {
	case "node.demo":
		list := []map[string]any{}
		for _, b := range n.boxes {
			list = append(list, b)
		}
		return ok(map[string]any{"enabled": true, "max_sandboxes": 4, "port_min": 30000, "port_max": 30099, "sandboxes": list, "fingerprints": nil, "templates": map[string]string{}})
	case "server.create":
		n.next++
		id := fmt.Sprintf("sb%d", n.next)
		n.boxes[id] = map[string]any{"server_id": id, "deadline": time.Now().Add(30 * time.Minute).Unix()}
		return ok(map[string]string{"server_id": id})
	case "server.delete":
		delete(n.boxes, e.ServerID)
		return ok(nil)
	case "node.demo.extend":
		b := n.boxes[e.ServerID]
		if b["extended"] == true {
			return &nodev1.ExecuteResponse{Error: "this sandbox was extended already"}, nil
		}
		b["extended"] = true
		b["deadline"] = b["deadline"].(int64) + 900
		return ok(b)
	}
	return ok(nil)
}

func (n *demoNodeFake) ready(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	n.boxes[id]["ready_at"] = now.Unix()
	n.boxes[id]["deadline"] = now.Add(limits.Lifetime).Unix()
}

func (n *demoNodeFake) last(action string) nodecmd.Envelope {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i := len(n.got) - 1; i >= 0; i-- {
		if n.got[i].Action == action {
			return n.got[i]
		}
	}
	return nodecmd.Envelope{}
}

func demoClient(b *browser, srv *httptest.Server) panelv1connect.DemoServiceClient {
	origin := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Origin", appOrigin)
			return next(ctx, req)
		}
	})
	return panelv1connect.NewDemoServiceClient(b.http, srv.URL+"/api", connect.WithInterceptors(origin))
}

// The public demo, Phase 2 (#449): a guest picks a library egg that fits,
// waits in the queue, gets a sandbox the Panel makes on the demo node with
// the node's demo key, may extend it once, and deletes it; the hourly
// reset restores the showcase servers with the same key.
func TestDemoSandboxes(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	if _, _, _, err := catalog.Seed(ctx, db, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	t.Setenv("PANEL_SIGNING_KEY", writeKey(t, key))
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Turnstile: fakeTurnstile{}}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	node := &demoNodeFake{t: t, panelKey: pub, boxes: map[string]map[string]any{}}
	sandboxes := &demo.Sandboxes{DB: db, Sender: node, PanelKey: key, Domain: "raptornodes.test"}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: orgSvc, Demo: &demo.Service{Auth: authSvc, Sandboxes: sandboxes},
		Commands: &commands.Service{Auth: authSvc, Sender: node, PanelKey: key}, AppOrigin: appOrigin,
	}))
	defer srv.Close()

	// The demo org, its node, and a showcase server, as staff set them up.
	owner := newBrowser(t, srv, appOrigin)
	if _, err := owner.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "staff@example.com", TurnstileToken: "ok"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := owner.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: "staff@example.com", Code: code}}}); err != nil {
		t.Fatal(err)
	}
	created, err := owner.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Raptor demo"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key, capabilities, facts) VALUES ($1, 'demo', decode(repeat('00', 32), 'hex'), '{raptor-eggs}', '{"arch":"amd64"}') RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, config) VALUES ($1, 'paper', 'Paper', 1, now(), now(), '{"allocations":[{"port":30000}]}')`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'test')`, nodeID); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, args := range [][]string{{"demo", "on", org}, {"demo", "server", org, nodeID, "paper"}, {"demo", "backup", nodeID, "paper", "b-good"}, {"demo", "node", nodeID, "-sandboxes", "1"}} {
		if err := staff.CLI(ctx, db, &out, args); err != nil {
			t.Fatalf("panel staff %v: %v", args, err)
		}
	}
	demoKey, _ := nodecmd.DemoKey(key, nodeID)
	node.demoKey = demoKey.Public().(ed25519.PublicKey)
	if !strings.Contains(out.String(), "raptor demo on -key ") {
		t.Fatalf("staff demo node didn't show the key:\n%s", out.String())
	}

	guest := newBrowser(t, srv, appOrigin)
	if _, err := guest.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"}); err != nil {
		t.Fatal(err)
	}
	gd := demoClient(guest, srv)

	// Until the node says it's a demo node (raptor demo on), sandboxes
	// aren't open.
	list, err := gd.ListDemoEggs(ctx, &panelv1.ListDemoEggsRequest{OrgId: org})
	if err != nil || list.GetOpen() {
		t.Fatalf("before raptor demo on: %v %v", list.GetOpen(), err)
	}
	if _, err := db.Exec(ctx, `UPDATE nodes SET capabilities = '{raptor-eggs,demo}' WHERE id = $1`, nodeID); err != nil {
		t.Fatal(err)
	}
	list, err = gd.ListDemoEggs(ctx, &panelv1.ListDemoEggsRequest{OrgId: org})
	if err != nil || !list.GetOpen() || list.GetArch() != "amd64" {
		t.Fatalf("ListDemoEggs: %v %v", list, err)
	}
	var paper, big *panelv1.DemoEgg
	for _, e := range list.GetEggs() {
		switch e.GetLibraryId() {
		case "minecraft/paper":
			paper = e
		case "steam/ark":
			big = e
		}
	}
	if paper == nil || !paper.GetFits() {
		t.Fatalf("paper: %v", paper)
	}
	if big == nil || big.GetFits() || !strings.Contains(big.GetWhy(), "too big") {
		t.Errorf("a big Steam game fits: %v", big)
	}
	for _, e := range list.GetEggs() {
		if !e.GetFits() && !strings.Contains(e.GetWhy(), "too big") {
			t.Errorf("%s doesn't fit for another reason: %s", e.GetLibraryId(), e.GetWhy())
		}
	}

	// Minecraft's EULA first; then it's queued.
	if _, err := gd.CreateSandbox(ctx, &panelv1.CreateSandboxRequest{OrgId: org, EggId: paper.GetEggId()}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("without the EULA: %v", err)
	}
	if big != nil {
		if _, err := gd.CreateSandbox(ctx, &panelv1.CreateSandboxRequest{OrgId: org, EggId: big.GetEggId(), AcceptEula: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Errorf("a big egg: %v", err)
		}
	}
	res, err := gd.CreateSandbox(ctx, &panelv1.CreateSandboxRequest{OrgId: org, EggId: paper.GetEggId(), AcceptEula: true})
	if err != nil || res.GetSandbox().GetState() != "queued" {
		t.Fatalf("CreateSandbox: %v %v", res, err)
	}
	// One per guest, and one per address.
	if _, err := gd.CreateSandbox(ctx, &panelv1.CreateSandboxRequest{OrgId: org, EggId: paper.GetEggId(), AcceptEula: true}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("a second sandbox: %v", err)
	}
	other := newBrowser(t, srv, appOrigin)
	if _, err := other.auth.StartDemo(ctx, &panelv1.StartDemoRequest{TurnstileToken: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := demoClient(other, srv).CreateSandbox(ctx, &panelv1.CreateSandboxRequest{OrgId: org, EggId: paper.GetEggId(), AcceptEula: true}); err == nil || !strings.Contains(err.Error(), "address") {
		t.Errorf("a second guest at the same address: %v", err)
	}

	// The leader sends it to the node with the demo key: a library egg,
	// the sandbox limits, a port in the range.
	if err := sandboxes.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	c := node.last("server.create")
	var p struct {
		Egg         []byte         `json:"egg"`
		Limits      map[string]int `json:"limits"`
		Allocations []struct {
			Port int `json:"port"`
		} `json:"allocations"`
		AcceptEULA bool `json:"accept_eula"`
	}
	if err := json.Unmarshal(c.Params, &p); err != nil || len(c.Demo) == 0 || c.Signature != nil {
		t.Fatalf("server.create: %+v %v", c, err)
	}
	if p.Limits["memory_mib"] != limits.MemoryMiB || p.Limits["cpu_percent"] != limits.CPUPercent || p.Limits["disk_mib"] != limits.DiskMiB ||
		len(p.Allocations) != 1 || p.Allocations[0].Port != 30001 || !p.AcceptEULA {
		t.Errorf("sandbox params: %+v", p)
	}
	got, err := gd.GetSandbox(ctx, &panelv1.GetSandboxRequest{OrgId: org})
	if err != nil || got.GetSandbox().GetState() != "installing" || got.GetSandbox().GetServerId() != "sb1" {
		t.Fatalf("after the create: %v %v", got, err)
	}
	// The guest has their own server: they can run it.
	if _, err := guest.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, ServerId: "sb1", Action: "server.start"}); err != nil {
		t.Errorf("the guest starting their sandbox: %v", err)
	}
	if _, err := guest.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, ServerId: "sb1", Action: "server.delete"}); !offInDemo(err) {
		t.Errorf("the guest deleting it as a command: %v", err)
	}

	// Ready: 15 minutes, its address, and it can be extended once.
	node.ready("sb1")
	if err := sandboxes.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = gd.GetSandbox(ctx, &panelv1.GetSandboxRequest{OrgId: org})
	sb := got.GetSandbox()
	if sb.GetState() != "ready" || !sb.GetCanExtend() || sb.GetDeadline() == nil ||
		!strings.HasSuffix(sb.GetAddress(), ".raptornodes.test:30001") {
		t.Fatalf("ready: %v", sb)
	}
	ext, err := gd.ExtendSandbox(ctx, &panelv1.ExtendSandboxRequest{OrgId: org})
	if err != nil || !ext.GetSandbox().GetExtended() || ext.GetSandbox().GetCanExtend() ||
		ext.GetSandbox().GetDeadline().AsTime().Sub(sb.GetDeadline().AsTime()) != 15*time.Minute {
		t.Fatalf("extend: %v %v", ext, err)
	}
	if _, err := gd.ExtendSandbox(ctx, &panelv1.ExtendSandboxRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("a second extension: %v", err)
	}

	// Deleting it: with the demo key, on the node.
	if _, err := gd.DeleteSandbox(ctx, &panelv1.DeleteSandboxRequest{OrgId: org}); err != nil {
		t.Fatal(err)
	}
	if d := node.last("server.delete"); d.ServerID != "sb1" || len(d.Demo) == 0 {
		t.Errorf("delete sent %+v", d)
	}
	if got, _ := gd.GetSandbox(ctx, &panelv1.GetSandboxRequest{OrgId: org}); got.GetSandbox().GetState() != "gone" {
		t.Errorf("after deleting: %v", got)
	}
	if _, err := gd.DeleteSandbox(ctx, &panelv1.DeleteSandboxRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("deleting nothing: %v", err)
	}

	// The egg editor's preview: reads a file, saves nothing.
	var e struct{ Egg []byte }
	if err := db.QueryRow(ctx, `SELECT egg FROM eggs WHERE catalog_id = 'minecraft/paper'`).Scan(&e.Egg); err != nil {
		t.Fatal(err)
	}
	var eggsBefore int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM eggs").Scan(&eggsBefore)
	prev, err := gd.PreviewEgg(ctx, &panelv1.PreviewEggRequest{OrgId: org, File: e.Egg})
	if err != nil || prev.GetSpec().GetName() == "" || prev.GetReview().GetInstallScript() == "" {
		t.Fatalf("PreviewEgg: %v", err)
	}
	var eggsAfter int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM eggs").Scan(&eggsAfter)
	if eggsAfter != eggsBefore {
		t.Error("the preview saved an egg")
	}
	if _, err := gd.PreviewEgg(ctx, &panelv1.PreviewEggRequest{OrgId: org, File: []byte("not an egg")}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad file: %v", err)
	}
	// Saving an egg is still off.
	if _, err := guest.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: e.Egg}); !offInDemo(err) {
		t.Errorf("importing an egg: %v", err)
	}

	// Pages: routes only, never IDs.
	if _, err := gd.RecordDemoPage(ctx, &panelv1.RecordDemoPageRequest{Page: "/orgs/$orgId/servers/$serverId/console"}); err != nil {
		t.Errorf("a route: %v", err)
	}
	if _, err := gd.RecordDemoPage(ctx, &panelv1.RecordDemoPageRequest{Page: "/orgs/" + org}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a URL with an ID: %v", err)
	}

	// Uploads are up to 10 MB for guests.
	params, _ := json.Marshal(map[string]any{"path": "/big.zip", "size": 50 << 20})
	if _, err := guest.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, ServerId: "paper", Action: "files.upload", ParamsJson: string(params)}); err == nil || !strings.Contains(err.Error(), "10 MB") {
		t.Errorf("a 50 MB upload: %v", err)
	}
	params, _ = json.Marshal(map[string]any{"path": "/small.txt", "size": 1 << 20})
	if _, err := guest.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, ServerId: "paper", Action: "files.upload", ParamsJson: string(params)}); err != nil {
		t.Errorf("a 1 MB upload: %v", err)
	}

	// Outside a demo org, there's no such thing.
	acme, err := owner.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := demoClient(owner, srv).ListDemoEggs(ctx, &panelv1.ListDemoEggsRequest{OrgId: acme.GetOrg().GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("ListDemoEggs in a normal org: %v", err)
	}

	// The hourly reset restores the showcase server's known-good backup
	// with the demo key, now that the node is a demo node.
	reset := &demo.Reset{DB: db, Sender: node, PanelKey: key}
	rr, err := reset.ResetOrg(ctx, pgtype.UUID{Bytes: uuid.MustParse(org), Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	if rr.Restored != 1 || rr.RestoresSkipped != 0 || rr.RestoresFailed != 0 {
		t.Errorf("reset: %+v", rr)
	}
	if r := node.last("backup.restore"); r.ServerID != "paper" || len(r.Demo) == 0 || !strings.Contains(string(r.Params), "b-good") {
		t.Errorf("restore sent %+v", r)
	}
	// The other guest's queued sandbox ends with them.
	if _, err := db.Exec(ctx, "DELETE FROM users WHERE guest_until IS NOT NULL"); err != nil {
		t.Fatal(err)
	}
	if err := sandboxes.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var live int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM demo_sandboxes WHERE state IN ('queued','creating','installing','ready')").Scan(&live)
	if live != 0 {
		t.Errorf("%d sandboxes left after the guests went", live)
	}
	if slices.ContainsFunc(node.got, func(e nodecmd.Envelope) bool { return e.Signature != nil }) {
		t.Error("a passkey signature from the Panel")
	}
}

// writeKey writes the Panel's signing key where PANEL_SIGNING_KEY says, as
// panel keygen does, for `panel staff demo node`.
func writeKey(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key.Seed())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
