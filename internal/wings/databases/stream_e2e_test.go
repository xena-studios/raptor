//go:build e2e

package databases

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testReader checks those who may only look run their SQL as the
// database's reader: a user that can't write even outside a read-only
// transaction, can't log in over the network, and is made again when it's
// gone.
func testReader(t *testing.T, svc *Service, server string, d Database, in Instance, addr, engine string) {
	t.Helper()
	ctx := context.Background()
	who := "SELECT CURRENT_USER()"
	if engine == Postgres {
		who = "SELECT current_user"
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "CREATE TABLE readers (id INT PRIMARY KEY)"}, false); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: who}, true)
	if err != nil || !strings.HasPrefix(str(res.Rows[0][0]), "v") {
		t.Fatalf("a reader's query ran as %+v (%v), not the database's reader", res.Rows, err)
	}
	if res, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: who}, false); err != nil || !strings.HasPrefix(str(res.Rows[0][0]), d.User) {
		t.Errorf("a writer's query ran as %+v (%v)", res.Rows, err)
	}

	r, err := svc.o.Store.Read.GetDatabase(ctx, d.ID)
	if err != nil || r.ViewerUsername == "" {
		t.Fatalf("the reader isn't recorded: %+v %v", r, err)
	}
	pass, err := svc.sealer.open(r.ViewerPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, ir, _ := svc.instance(ctx, svc.o.Store.Read, in.ID)
	root, _ := svc.sealer.open(ir.RootPassword)
	se, err := connectAs(ctx, in, svc.dir(in.ID), root, r.ViewerUsername, pass, r.DbName, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"INSERT INTO readers VALUES (1)", "DROP TABLE readers", "CREATE TABLE sneaky (i INT)"} {
		var err error
		if se.pg != nil {
			_, err = se.pg.Exec(ctx, stmt)
		} else {
			_, err = se.my.ExecContext(ctx, stmt)
		}
		if err == nil {
			t.Errorf("the reader ran %q outside a read-only transaction", stmt)
		}
	}
	se.Close()
	if err := query(engine, addr, r.ViewerUsername, pass, d.Name, "SELECT 1"); err == nil {
		t.Error("the reader logged in over the network")
	}

	// Dropped behind Wings' back, it's made again.
	if err := dropReadOnly(ctx, in, svc.dir(in.ID), root, r.DbName, r.ViewerUsername); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "SELECT COUNT(*) FROM readers"}, true); err != nil {
		t.Errorf("after the reader was dropped: %v", err)
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "DROP TABLE readers"}, false); err != nil {
		t.Fatal(err)
	}
}

// syncBuffer is a stream's output, read while it's written.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// streamLines runs a query to the end and returns its lines.
func streamLines(t *testing.T, svc *Service, server, id string, in QueryInput, readOnly bool) []map[string]json.RawMessage {
	t.Helper()
	ctx := context.Background()
	qid, err := svc.StartQuery(ctx, server, id, "u1", in, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	if err := svc.StreamQuery(ctx, qid, &out, nil); err != nil {
		t.Fatal(err)
	}
	var lines []map[string]json.RawMessage
	for l := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("line %q: %v", l, err)
		}
		lines = append(lines, m)
	}
	return lines
}

type doneLine struct {
	Statements int   `json:"statements"`
	Affected   int64 `json:"affected"`
	Rows       int64 `json:"rows"`
	Shown      int   `json:"shown"`
	More       bool  `json:"more"`
}

// testStream checks streamed queries: a big result shows its first
// MaxStreamRows rows and counts the rest; a change says how many rows it
// changed; and a query is stopped on the engine when it's cancelled or
// its stream ends, and only by the user who started it.
func testStream(t *testing.T, svc *Service, server string, d Database, in Instance, engine string) {
	t.Helper()
	ctx := context.Background()
	big := "SELECT 1 AS n FROM information_schema.COLUMNS a CROSS JOIN information_schema.COLUMNS b LIMIT 10005"
	if engine == Postgres {
		big = "SELECT n FROM generate_series(1, 10005) n"
	}
	lines := streamLines(t, svc, server, d.ID, QueryInput{SQL: big}, true)
	rows := 0
	var done doneLine
	for _, l := range lines {
		if r, ok := l["rows"]; ok {
			var batch [][]*string
			_ = json.Unmarshal(r, &batch)
			rows += len(batch)
		}
		if r, ok := l["done"]; ok {
			_ = json.Unmarshal(r, &done)
		}
		if e, ok := l["error"]; ok {
			t.Fatalf("a big result: %s", e)
		}
	}
	if _, ok := lines[0]["columns"]; !ok || rows != MaxStreamRows || done.Rows != 10005 || done.Shown != MaxStreamRows || !done.More {
		t.Errorf("a big result: %d rows sent, done %+v, first %v", rows, done, lines[0])
	}

	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "CREATE TABLE streamed (id INT PRIMARY KEY, n INT);\nINSERT INTO streamed VALUES (1, 1), (2, 2)"}, false); err != nil {
		t.Fatal(err)
	}
	lines = streamLines(t, svc, server, d.ID, QueryInput{SQL: "UPDATE streamed SET n = n + 1"}, false)
	_ = json.Unmarshal(lines[len(lines)-1]["done"], &done)
	if done.Affected != 2 {
		t.Errorf("an update's stream: %v", lines)
	}
	if _, err := svc.Query(ctx, server, d.ID, QueryInput{SQL: "DROP TABLE streamed"}, false); err != nil {
		t.Fatal(err)
	}

	sleep := "SELECT SLEEP(60)"
	if engine == Postgres {
		sleep = "SELECT pg_sleep(60)"
	}
	running := func() *Process {
		ps, err := svc.InstanceProcesses(ctx, in.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if strings.Contains(strings.ToLower(p.Query), "sleep(60)") {
				return &p
			}
		}
		return nil
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); !ok(); {
			if time.Now().After(deadline) {
				t.Fatalf("waited too long for %s", what)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	for _, how := range []string{"cancel as a reader", "cancel as a writer", "close the stream", "an admin kills it"} {
		readOnly := how == "cancel as a reader"
		qid, err := svc.StartQuery(ctx, server, d.ID, "u1", QueryInput{SQL: sleep, Timeout: 120}, readOnly)
		if err != nil {
			t.Fatal(err)
		}
		sctx, closeStream := context.WithCancel(ctx)
		var out syncBuffer
		ended := make(chan error, 1)
		go func() { ended <- svc.StreamQuery(sctx, qid, &out, nil) }()
		var p *Process
		waitFor("the query to run", func() bool { p = running(); return p != nil })
		if p.Database != d.Name {
			t.Errorf("%s: the instance's process is on %q", how, p.Database)
		}
		if err := svc.CancelQuery(server, "u2", qid); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: another user cancelled it: %v", how, err)
		}
		start := time.Now()
		switch how {
		case "close the stream":
			closeStream()
		case "an admin kills it":
			if err := svc.KillInstanceQuery(ctx, in.ID, p.ID); err != nil {
				t.Errorf("killing it: %v", err)
			}
		default:
			if err := svc.CancelQuery(server, "u1", qid); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case <-ended:
		case <-time.After(15 * time.Second):
			t.Fatalf("%s: the stream didn't end", how)
		}
		closeStream()
		if time.Since(start) > 10*time.Second {
			t.Errorf("%s: took %s to stop", how, time.Since(start))
		}
		if strings.HasPrefix(how, "cancel") && !strings.Contains(out.String(), "cancelled") {
			t.Errorf("%s: the stream says %q", how, out.String())
		}
		waitFor("the query to be gone from the engine", func() bool { return running() == nil })
		if svc.HasQuery(qid) {
			t.Errorf("%s: the query is still waiting", how)
		}
	}
	if err := svc.StreamQuery(ctx, "nope", &syncBuffer{}, nil); !errors.Is(err, ErrNoQuery) {
		t.Errorf("a stream for no query: %v", err)
	}
	if _, err := svc.StartQuery(ctx, server, d.ID, "u1", QueryInput{SQL: "SELECT 1", Timeout: MaxStreamTimeout + 1}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("a time limit past the most: %v", err)
	}
}

// testInstancePage checks an instance's own page: its databases of every
// server, and its users with what each can reach, without the browser's
// readers.
func testInstancePage(t *testing.T, svc *Service, in Instance, d1, d2 Database) {
	t.Helper()
	ctx := context.Background()
	dbs, err := svc.InstanceDatabases(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, d := range dbs {
		ids = append(ids, d.ID)
	}
	if !slices.Contains(ids, d1.ID) || !slices.Contains(ids, d2.ID) {
		t.Errorf("the instance's databases: %v", ids)
	}
	ro, _, err := svc.SetReadOnly(ctx, d1.ServerID, d1.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.RemoveReadOnly(ctx, d1.ServerID, d1.ID) }()
	users, err := svc.InstanceUsers(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]InstanceUser{}
	for _, u := range users {
		byName[u.Name] = u
		if strings.HasPrefix(u.Name, "v") && u.Kind != "other" {
			t.Errorf("a browser's reader is listed: %+v", u)
		}
	}
	if u := byName[d1.User]; u.Kind != "database" || u.ServerID != d1.ServerID || !slices.Contains(u.Reaches, d1.Name) || slices.Contains(u.Reaches, d2.Name) {
		t.Errorf("the database's user: %+v", u)
	}
	if u := byName[ro]; u.Kind != "readonly" || u.DatabaseID != d1.ID || !slices.Equal(u.Reaches, []string{d1.Name}) {
		t.Errorf("the read-only user: %+v", u)
	}
	admin := "root"
	if in.Engine == Postgres {
		admin = "postgres"
	}
	if u := byName[admin]; u.Kind != "admin" || !slices.Equal(u.Reaches, []string{"*"}) {
		t.Errorf("the administrator: %+v (%+v)", u, users)
	}
}
