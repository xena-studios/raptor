package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// receiver records what a webhook URL got.
type receiver struct {
	mu     sync.Mutex
	bodies []map[string]any
	heads  []http.Header
	fail   int // answer this many requests with failStatus first
	status int
	srv    *httptest.Server
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{status: http.StatusInternalServerError}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.fail > 0 {
			r.fail--
			w.WriteHeader(r.status)
			return
		}
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["_raw"] = string(b)
		r.bodies = append(r.bodies, m)
		r.heads = append(r.heads, req.Header.Clone())
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) got() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.bodies...)
}

type env struct {
	t   *testing.T
	db  *store.DB
	out *events.Outbox
	now time.Time
}

func newEnv(t *testing.T) *env {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Write.InsertServer(context.Background(), store.InsertServerParams{
		ID: "srv-1", Name: "survival", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x",
		Variables: "{}", Limits: "{}", Settings: "{}", DesiredState: "running", InstallState: "installed",
	}); err != nil {
		t.Fatal(err)
	}
	return &env{t: t, db: db, out: events.New(db), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
}

func (e *env) notifier(targets ...config.Notification) *Notifier {
	n := &Notifier{
		Targets: targets, DB: e.db, Outbox: e.out, Node: "box-1", NodeID: "node-1",
		Now: func() time.Time { return e.now }, RetryBase: time.Millisecond,
	}
	n.init()
	if err := n.initCursors(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) event(typ, server string, data map[string]any) {
	e.t.Helper()
	if _, err := e.out.Append(context.Background(), events.Event{Type: typ, ServerID: server, At: e.now, Data: data}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) audit(action, outcome, detail string) {
	e.t.Helper()
	if _, err := e.db.Write.InsertAudit(context.Background(), store.InsertAuditParams{
		At: e.now.UnixMilli(), Action: action, ServerID: "srv-1", UserID: "alice", Outcome: outcome, Detail: detail,
	}); err != nil {
		e.t.Fatal(err)
	}
}

func TestDelivery(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.event("server.crashed", "srv-1", map[string]any{"exit_code": 1}) // before notifications existed: not sent
	discord, hook, crashesOnly := newReceiver(t), newReceiver(t), newReceiver(t)
	n := e.notifier(
		config.Notification{Type: "discord", URL: discord.srv.URL},
		config.Notification{Type: "webhook", URL: hook.srv.URL, Secret: "s3cret"},
		config.Notification{Type: "webhook", URL: crashesOnly.srv.URL, Events: []string{"crash"}},
	)
	e.event("server.crashed", "srv-1", map[string]any{"exit_code": 137, "reason": "oom", "console": []any{"line one", "line two"}})
	e.event("server.state", "srv-1", map[string]any{"state": "running"}) // not notified about
	e.event("backup.finished", "srv-1", map[string]any{"status": "ok"})  // only failures are
	e.audit("server.delete", command.AuditRejected, "signature doesn't match")
	n.Process(ctx)

	d := discord.got()
	if len(d) != 2 {
		t.Fatalf("discord got %d messages", len(d))
	}
	embed := d[0]["embeds"].([]any)[0].(map[string]any)
	if embed["title"] != "Server crashed" || !strings.Contains(embed["description"].(string), "Exit code 137 (oom)") ||
		!strings.Contains(embed["description"].(string), "line two") || d[0]["allowed_mentions"] == nil {
		t.Errorf("discord crash: %v", d[0]["_raw"])
	}
	if !strings.Contains(d[0]["_raw"].(string), `"value":"survival"`) {
		t.Errorf("discord message doesn't name the server: %v", d[0]["_raw"])
	}
	sec := d[1]["embeds"].([]any)[0].(map[string]any)
	if sec["title"] != "Signed action rejected: server.delete" || int(sec["color"].(float64)) != colors[Critical] {
		t.Errorf("discord security: %v", d[1]["_raw"])
	}

	h := hook.got()
	if len(h) != 2 || h[0]["category"] != "crash" || h[0]["server_name"] != "survival" || h[0]["node"] != "box-1" || h[1]["category"] != "security" {
		t.Fatalf("webhook: %v", h)
	}
	head := hook.heads[0]
	if head.Get("X-Raptor-Event") != "server.crashed" || head.Get("X-Raptor-Signature") != "sha256="+Sign("s3cret", head.Get("X-Raptor-Timestamp"), []byte(h[0]["_raw"].(string))) {
		t.Errorf("signature headers: %v", head)
	}
	if got := crashesOnly.got(); len(got) != 1 || got[0]["category"] != "crash" {
		t.Errorf("crash-only target: %v", got)
	}

	// A restart (a new notifier) carries on: nothing is sent twice.
	e.event("node.disk_low", "", map[string]any{"min_free": 10 << 30})
	n2 := e.notifier(config.Notification{Type: "discord", URL: discord.srv.URL})
	n2.Process(ctx)
	if d := discord.got(); len(d) != 3 || !strings.Contains(d[2]["_raw"].(string), "Host disk low") {
		t.Fatalf("after a restart: %d messages", len(d))
	}
}

// A signed action that's still running is sent once it finishes, and
// later entries wait for it, so they arrive in order.
func TestAuditOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	r := newReceiver(t)
	n := e.notifier(config.Notification{Type: "webhook", URL: r.srv.URL})
	e.audit("server.delete", command.AuditRunning, "")
	e.audit("backup.delete", command.AuditOK, "")
	n.Process(ctx)
	if len(r.got()) != 0 {
		t.Fatal("sent before the first finished")
	}
	if err := e.db.Write.FinishAudit(ctx, store.FinishAuditParams{Outcome: command.AuditOK, ID: 1}); err != nil {
		t.Fatal(err)
	}
	n.Process(ctx)
	got := r.got()
	if len(got) != 2 || got[0]["type"] != "server.delete" || got[1]["type"] != "backup.delete" {
		t.Fatalf("order: %v", got)
	}
}

func TestRetries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	flaky, gone, ok := newReceiver(t), newReceiver(t), newReceiver(t)
	flaky.fail = 2                                     // two 500s, then fine
	gone.fail, gone.status = 1000, http.StatusNotFound // a deleted webhook
	n := e.notifier(
		config.Notification{Type: "webhook", URL: flaky.srv.URL},
		config.Notification{Type: "webhook", URL: gone.srv.URL},
		config.Notification{Type: "webhook", URL: ok.srv.URL},
	)
	e.event("server.crash_loop", "srv-1", map[string]any{"crashes": 5})
	n.Process(ctx)
	if len(flaky.got()) != 1 || len(ok.got()) != 1 {
		t.Fatalf("flaky %d, ok %d", len(flaky.got()), len(ok.got()))
	}
	if gone.fail != 999 {
		t.Errorf("a 404 was retried (%d requests)", 1000-gone.fail)
	}
}

// A dead target is skipped for a while instead of holding up the others,
// and what it missed is counted when it's back.
func TestDeadTarget(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	dead, ok := newReceiver(t), newReceiver(t)
	dead.fail = 1000
	n := e.notifier(config.Notification{Type: "webhook", URL: dead.srv.URL}, config.Notification{Type: "webhook", URL: ok.srv.URL})
	for range 3 {
		e.event("server.crashed", "srv-1", map[string]any{"exit_code": 1})
	}
	n.Process(ctx)
	if tries := 1000 - dead.fail; tries != attempts {
		t.Errorf("the dead target got %d requests, want %d (one message, then skipped)", tries, attempts)
	}
	if len(ok.got()) != 3 {
		t.Errorf("the working target got %d", len(ok.got()))
	}
	e.now = e.now.Add(downFor + time.Second)
	dead.mu.Lock()
	dead.fail = 0
	dead.mu.Unlock()
	e.event("server.crashed", "srv-1", map[string]any{"exit_code": 1})
	n.Process(ctx)
	got := dead.got()
	if len(got) != 1 || !strings.Contains(got[0]["text"].(string), "3 earlier notifications weren't sent") {
		t.Fatalf("back up: %v", got)
	}
}

func TestRateLimit(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	r := newReceiver(t)
	n := e.notifier(config.Notification{Type: "webhook", URL: r.srv.URL})
	for range perMinute + 5 {
		e.event("server.crashed", "srv-1", map[string]any{"exit_code": 1})
	}
	n.Process(ctx)
	if got := len(r.got()); got != perMinute {
		t.Fatalf("sent %d in a minute", got)
	}
	e.now = e.now.Add(time.Minute)
	e.event("server.crashed", "srv-1", map[string]any{"exit_code": 1})
	n.Process(ctx)
	got := r.got()
	if !strings.Contains(got[len(got)-1]["text"].(string), "5 earlier notifications weren't sent") {
		t.Errorf("summary: %v", got[len(got)-1]["text"])
	}
}

func TestTest(t *testing.T) {
	e := newEnv(t)
	good, bad := newReceiver(t), newReceiver(t)
	bad.fail, bad.status = 1, http.StatusUnauthorized
	n := e.notifier(config.Notification{Name: "ops", Type: "discord", URL: good.srv.URL}, config.Notification{Type: "webhook", URL: bad.srv.URL})
	res := n.Test(context.Background())
	if len(res) != 2 || res[0].Target != "ops" || res[0].Err != nil || res[1].Target != "webhook #2" || res[1].Err == nil {
		t.Fatalf("results: %+v", res)
	}
}

func TestMessages(t *testing.T) {
	for _, tt := range []struct {
		typ   string
		data  map[string]any
		title string
		cat   string
	}{
		{"server.install.failed", map[string]any{"error": "pull failed"}, "Install failed", "install"},
		{"backup.finished", map[string]any{"status": "failed", "error": "bucket gone"}, "Backup failed", "backup"},
		{"backup.restore.finished", map[string]any{"ok": false, "error": "x"}, "Restore failed", "backup"},
		{"server.delete.failed", map[string]any{"error": "x"}, "Server not deleted: its final backup failed", "backup"},
		{"server.disk_limit_exceeded", map[string]any{"used_bytes": float64(2 << 30), "limit_bytes": float64(1 << 30)}, "Server over its disk limit", "disk"},
		{"node.update", map[string]any{"result": "succeeded", "from": "1.0.0", "to": "1.1.0"}, "Wings updated to 1.1.0", "update"},
		{"node.update", map[string]any{"result": "failed", "from": "1.0.0", "to": "1.1.0", "error": "crashed"}, "Wings update to 1.1.0 rolled back", "update"},
	} {
		m, ok := FromEvent(events.Event{Type: tt.typ, Data: tt.data})
		if !ok || m.Title != tt.title || m.Category != tt.cat {
			t.Errorf("%s: %+v, %v", tt.typ, m, ok)
		}
	}
	m := FromAudit(command.AuditEntry{Action: "keys.reset", UserID: "local:root", Detail: "pinned X"})
	if m.Level != Critical || m.Category != "security" {
		t.Errorf("key reset: %+v", m)
	}
}
