//go:build e2e

package databases

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestKeyBrowser browses and changes a Redis database's keys of every
// type, binary ones included, and runs commands: those who may only look
// run read-only ones, and nothing blocking runs at all.
func TestKeyBrowser(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E key browser")
	in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E keys", Engine: Redis, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 256}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
	in = waitRunning(t, e.svc, in.ID)
	d, pw, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "cache")
	if err != nil {
		t.Fatal(err)
	}
	c := redisAs(in.Addresses[e.net.ID], d.User, pw)
	defer func() { _ = c.Close() }()
	pipe := c.Pipeline()
	pipe.Set(ctx, "player:alex", "42", time.Hour)
	pipe.Set(ctx, "player:sam", "7", 0)
	pipe.HSet(ctx, "ranks", "admin", "alex", "mod", "sam")
	pipe.RPush(ctx, "queue", "a", "b", "c")
	pipe.SAdd(ctx, "online", "alex", "sam")
	pipe.Do(ctx, "ZADD", "top", 10, "alex", 5, "sam")
	pipe.Set(ctx, "\xff\x00bin", "x", 0)
	if _, err := pipe.Exec(ctx); err != nil && !strings.Contains(err.Error(), "ERR") {
		t.Fatal(err)
	}
	if err := c.Do(ctx, "XADD", "events", "*", "kind", "join", "who", "alex").Err(); err != nil {
		t.Fatal(err)
	}

	page, err := e.svc.Keys(ctx, e.server, d.ID, KeysQuery{})
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	var bin KeyInfo
	for _, k := range page.Keys {
		types[k.Key] = k.Type
		if k.Hex != "" {
			bin = k
		}
	}
	for k, want := range map[string]string{"player:alex": "string", "ranks": "hash", "queue": "list", "online": "set", "top": "zset", "events": "stream"} {
		if types[k] != want {
			t.Errorf("%s is %q, want %s (%v)", k, types[k], want, types)
		}
	}
	if page.Total != 8 || page.Cursor != "0" {
		t.Errorf("total %d cursor %s", page.Total, page.Cursor)
	}
	if bin.Hex != "ff0062696e" {
		t.Errorf("the binary key: %+v", bin)
	}
	if p, _ := e.svc.Keys(ctx, e.server, d.ID, KeysQuery{Pattern: "player:*"}); len(p.Keys) != 2 {
		t.Errorf("pattern: %+v", p.Keys)
	}
	if v, err := e.svc.Key(ctx, e.server, d.ID, KeyRef{Key: "player:alex"}, ""); err != nil || *v.String != "42" || v.TTL <= 0 {
		t.Errorf("string: %+v %v", v, err)
	}
	if v, err := e.svc.Key(ctx, e.server, d.ID, KeyRef{Key: "top"}, ""); err != nil || len(v.Items) != 2 || v.Items[0].Value != "sam" || *v.Items[1].Score != 10 {
		t.Errorf("zset: %+v %v", v, err)
	}
	if v, err := e.svc.Key(ctx, e.server, d.ID, KeyRef{Key: "events"}, ""); err != nil || len(v.Items) != 1 || v.Items[0].Fields["who"] != "alex" {
		t.Errorf("stream: %+v %v", v, err)
	}
	if v, err := e.svc.Key(ctx, e.server, d.ID, KeyRef{Hex: bin.Hex}, ""); err != nil || *v.String != "x" {
		t.Errorf("binary key by hex: %+v %v", v, err)
	}

	score := 99.0
	for _, edit := range []KeyEdit{
		{KeyRef: KeyRef{Key: "player:alex"}, Op: "set", Value: "43"},
		{KeyRef: KeyRef{Key: "ranks"}, Op: "hset", Field: "vip", Value: "kai"},
		{KeyRef: KeyRef{Key: "ranks"}, Op: "hdel", Field: "mod"},
		{KeyRef: KeyRef{Key: "queue"}, Op: "lset", Index: 0, Value: "z"},
		{KeyRef: KeyRef{Key: "online"}, Op: "srem", Value: "sam"},
		{KeyRef: KeyRef{Key: "top"}, Op: "zadd", Value: "kai", Score: &score},
		{KeyRef: KeyRef{Key: "player:sam"}, Op: "expire", TTL: 60_000},
		{KeyRef: KeyRef{Key: "player:sam"}, Op: "rename", Value: "player:samuel"},
		{KeyRef: KeyRef{Hex: bin.Hex}, Op: "delete"},
	} {
		if n, err := e.svc.EditKey(ctx, e.server, d.ID, edit); err != nil || n != 1 {
			t.Errorf("%s %s: %d %v", edit.Op, edit.Key, n, err)
		}
	}
	if v, _ := c.Get(ctx, "player:alex").Result(); v != "43" {
		t.Errorf("set kept %q", v)
	}
	if ttl, _ := c.TTL(ctx, "player:alex").Result(); ttl <= 0 {
		t.Error("set dropped the key's time to live")
	}
	if ttl, _ := c.TTL(ctx, "player:samuel").Result(); ttl <= 0 {
		t.Error("expire and rename")
	}
	if _, err := e.svc.EditKey(ctx, e.server, d.ID, KeyEdit{KeyRef: KeyRef{Key: "ranks"}, Op: "rename", Value: "queue"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("renaming onto a key: %v", err)
	}

	// Commands.
	if r, err := e.svc.RunCommand(ctx, e.server, d.ID, []string{"GET", "player:alex"}, true); err != nil || r.Reply != "43" {
		t.Errorf("GET as a reader: %+v %v", r, err)
	}
	if r, err := e.svc.RunCommand(ctx, e.server, d.ID, []string{"HGETALL", "ranks"}, true); err != nil {
		t.Errorf("HGETALL: %v", err)
	} else if b, _ := json.Marshal(r.Reply); !strings.Contains(string(b), "kai") {
		t.Errorf("HGETALL reply %s", b)
	}
	if _, err := e.svc.RunCommand(ctx, e.server, d.ID, []string{"SET", "x", "1"}, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("SET as a reader: %v", err)
	}
	if _, err := e.svc.RunCommand(ctx, e.server, d.ID, []string{"SET", "x", "1"}, false); err != nil {
		t.Errorf("SET as a writer: %v", err)
	}
	for _, cmd := range [][]string{{"BLPOP", "queue", "0"}, {"SUBSCRIBE", "c"}, {"MONITOR"}} {
		if _, err := e.svc.RunCommand(ctx, e.server, d.ID, cmd, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", cmd, err)
		}
	}
	if _, err := e.svc.RunCommand(ctx, e.server, d.ID, []string{"CONFIG", "GET", "dir"}, false); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Errorf("CONFIG GET as the server's user: %v", err)
	}
	if _, err := e.svc.RunCommand(ctx, e.server, d.ID, []string{"NOSUCH"}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("an unknown command: %v", err)
	}
}

// TestDocumentBrowser makes a collection, inserts, finds, replaces, and
// deletes documents with Extended JSON, and drops it.
func TestDocumentBrowser(t *testing.T) {
	ctx := context.Background()
	e := newE2E(t, "E2E document browser")
	in, err := e.svc.Save(ctx, InstanceInput{Name: "E2E documents", Engine: MongoDB, Networks: []string{e.net.ID}, Limits: Limits{MemoryMiB: 768}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.svc.Delete(context.Background(), in.ID) })
	waitRunning(t, e.svc, in.ID)
	d, _, err := e.svc.CreateDatabase(ctx, e.server, in.ID, "stats")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EditCollection(ctx, e.server, d.ID, "create", "players"); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EditCollection(ctx, e.server, d.ID, "create", "system.x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("a system collection: %v", err)
	}
	for _, doc := range []string{
		`{"name": "alex", "kills": 10, "joined": {"$date": "2026-10-01T12:00:00Z"}}`,
		`{"name": "sam", "kills": 3}`,
		`{"_id": 7, "name": "kai", "kills": 20}`,
	} {
		if n, err := e.svc.EditDocuments(ctx, e.server, d.ID, DocumentEdit{Collection: "players", Op: "insert", Document: doc}); err != nil || n != 1 {
			t.Fatalf("insert %s: %d %v", doc, n, err)
		}
	}
	cols, err := e.svc.Collections(ctx, e.server, d.ID)
	if err != nil || len(cols) != 1 || cols[0].Name != "players" || cols[0].Count != 3 || len(cols[0].Indexes) != 1 {
		t.Fatalf("collections: %+v %v", cols, err)
	}
	res, err := e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "players", Filter: `{"kills": {"$gt": 5}}`, Sort: `{"kills": -1}`})
	if err != nil || len(res.Documents) != 2 || res.Count != 2 || !strings.Contains(res.Documents[0], `"kai"`) {
		t.Fatalf("find: %+v %v", res, err)
	}
	if !strings.Contains(res.Documents[1], `"$date"`) || !strings.Contains(res.Documents[1], `"$oid"`) {
		t.Errorf("Extended JSON: %s", res.Documents[1])
	}
	if res, _ := e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "players", Limit: 2}); len(res.Documents) != 2 || !res.More {
		t.Errorf("a page: %+v", res)
	}
	if res, _ := e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "players", Projection: `{"name": 1, "_id": 0}`, Filter: `{"_id": 7}`}); len(res.Documents) != 1 || res.Documents[0] != `{"name":"kai"}` {
		t.Errorf("projection: %+v", res.Documents)
	}
	if _, err := e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "players", Filter: `{nope`}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a broken filter: %v", err)
	}
	if _, err := e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "nowhere"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a missing collection: %v", err)
	}

	// Replace by an ObjectId, and by a number.
	var alex struct {
		ID map[string]string `json:"_id"`
	}
	res, _ = e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "players", Filter: `{"name": "alex"}`})
	_ = json.Unmarshal([]byte(res.Documents[0]), &alex)
	oid, _ := json.Marshal(alex.ID)
	if n, err := e.svc.EditDocuments(ctx, e.server, d.ID, DocumentEdit{Collection: "players", Op: "replace", ID: string(oid), Document: `{"name": "alex", "kills": 11}`}); err != nil || n != 1 {
		t.Errorf("replace by ObjectId: %d %v", n, err)
	}
	if n, err := e.svc.EditDocuments(ctx, e.server, d.ID, DocumentEdit{Collection: "players", Op: "replace", ID: "7", Document: `{"_id": 7, "name": "kai", "kills": 21}`}); err != nil || n != 1 {
		t.Errorf("replace by number: %d %v", n, err)
	}
	res, _ = e.svc.Documents(ctx, e.server, d.ID, DocumentsQuery{Collection: "players", Sort: `{"kills": 1}`})
	if len(res.Documents) != 3 || !strings.Contains(res.Documents[1], `"kills":11`) || !strings.Contains(res.Documents[2], `"kills":21`) {
		t.Errorf("after replacing: %v", res.Documents)
	}
	if n, err := e.svc.EditDocuments(ctx, e.server, d.ID, DocumentEdit{Collection: "players", Op: "delete", IDs: []string{string(oid), "7"}}); err != nil || n != 2 {
		t.Errorf("delete: %d %v", n, err)
	}
	if err := e.svc.EditCollection(ctx, e.server, d.ID, "drop", "players"); err != nil {
		t.Fatal(err)
	}
	if cols, _ := e.svc.Collections(ctx, e.server, d.ID); slices.ContainsFunc(cols, func(c Collection) bool { return c.Name == "players" }) {
		t.Error("the collection is still there")
	}
}
