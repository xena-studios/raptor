package storage

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/hosted"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/wings/backup"
)

// fakeB2 is B2's API, as much as Raptor uses: keys, and file versions.
type fakeB2 struct {
	mu      sync.Mutex
	keys    map[string]string // id -> name prefix
	files   map[string]int64  // name -> size of its current version
	hidden  map[string]int64  // name -> size of a hidden (deleted) version
	calls   []string
	nextKey int
}

func (f *fakeB2) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.calls = append(f.calls, name)
		if name == "b2_authorize_account" {
			if id, key, ok := r.BasicAuth(); !ok || id != "master" || key != "master-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"status": 401, "code": "unauthorized", "message": "bad"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accountId": "acct", "authorizationToken": "tok",
				"apiInfo": map[string]any{"storageApi": map[string]any{"apiUrl": "http://" + r.Host}},
			})
			return
		}
		if r.Header.Get("Authorization") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch name {
		case "b2_create_key":
			// A node's key writes and hides, and never deletes for good.
			caps := toStrings(body["capabilities"])
			if body["bucketId"] != "bucket-1" || !slices.Contains(caps, "writeFiles") || slices.Contains(caps, "deleteFiles") ||
				slices.Contains(caps, "writeKeys") || slices.Contains(caps, "deleteBuckets") {
				t.Errorf("create_key: %v", body)
			}
			f.nextKey++
			id := "key-" + string(rune('0'+f.nextKey))
			f.keys[id] = body["namePrefix"].(string)
			_ = json.NewEncoder(w).Encode(map[string]string{"applicationKeyId": id, "applicationKey": "secret-" + id})
		case "b2_delete_key":
			delete(f.keys, body["applicationKeyId"].(string))
			_, _ = w.Write([]byte("{}"))
		case "b2_list_file_versions":
			prefix := body["prefix"].(string)
			var names []string
			for n := range f.files {
				if strings.HasPrefix(n, prefix) {
					names = append(names, n)
				}
			}
			for n := range f.hidden {
				if strings.HasPrefix(n, prefix) && f.files[n] == 0 {
					names = append(names, n)
				}
			}
			slices.Sort(names)
			start, _ := body["startFileName"].(string)
			var page []map[string]any
			var next any
			for _, n := range names {
				if n < start {
					continue
				}
				if len(page) == 2 { // small pages, to test paging
					next = n
					break
				}
				if size, ok := f.files[n]; ok {
					page = append(page, map[string]any{"fileName": n, "fileId": "id-" + n, "contentLength": size, "action": "upload"})
				} else {
					// Hidden: the hide marker first, then the old version.
					page = append(page,
						map[string]any{"fileName": n, "fileId": "hide-" + n, "contentLength": 0, "action": "hide"},
						map[string]any{"fileName": n, "fileId": "old-" + n, "contentLength": f.hidden[n], "action": "upload"})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"files": page, "nextFileName": next, "nextFileId": next})
		case "b2_delete_file_version":
			delete(f.files, body["fileName"].(string))
			delete(f.hidden, body["fileName"].(string))
			_, _ = w.Write([]byte("{}"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// sender is a node that keeps the destination it's sent.
type sender struct {
	mu      sync.Mutex
	offline bool
	got     []nodecmd.Envelope
}

func (s *sender) Execute(_ context.Context, _ string, raw []byte) (*nodev1.ExecuteResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offline {
		return nil, context.DeadlineExceeded
	}
	var env nodecmd.Envelope
	_ = json.Unmarshal(raw, &env)
	s.got = append(s.got, env)
	return &nodev1.ExecuteResponse{}, nil
}

func TestBackupStorage(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	fb := &fakeB2{keys: map[string]string{}, files: map[string]int64{}, hidden: map[string]int64{}}
	srv := httptest.NewServer(fb.handler(t))
	defer srv.Close()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	node := &sender{}
	s := &Service{
		DB: db, B2: &B2{KeyID: "master", Key: "master-secret", AuthURL: srv.URL + "/b2api/v3/b2_authorize_account"},
		BucketID: "bucket-1", Endpoint: "s3.us-west-004.backblazeb2.com", Region: "us-west-004",
		Sender: node, PanelKey: key, Now: func() time.Time { return now },
	}
	org, n1 := uuid.New(), uuid.New()

	// Turning it on: a key for the node's folder, sent to the node.
	if err := s.Enable(ctx, org, n1, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	if len(fb.keys) != 1 || fb.keys["key-1"] != hosted.Prefix(org.String(), n1.String()) {
		t.Fatalf("keys: %v", fb.keys)
	}
	env := node.got[0]
	if env.Action != "backup.destination.save" || env.UserID != UserID || env.Signature != nil {
		t.Fatalf("command: %+v", env)
	}
	// What the node gets is what Wings accepts unsigned as Raptor Backup
	// Storage for that node, and nothing else.
	var d backup.Destination
	if err := json.Unmarshal(env.Params, &d); err != nil {
		t.Fatal(err)
	}
	if d.Raptor == nil || d.Raptor.SecretKey != "secret-key-1" || d.Raptor.Bucket != hosted.Bucket {
		t.Fatalf("destination: %+v", d)
	}
	if !hostedFor(d, n1.String()) || hostedFor(d, uuid.NewString()) {
		t.Fatal("Wings wouldn't take it as this node's Raptor Backup Storage")
	}

	// Again: a new key, the old one deleted, still one row.
	if err := s.Enable(ctx, org, n1, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fb.keys["key-1"]; ok || len(fb.keys) != 1 {
		t.Fatalf("keys after replacing: %v", fb.keys)
	}
	on, _ := store.New(db).OrgBackupStorage(ctx, pgtype.UUID{Bytes: org, Valid: true})
	if len(on) != 1 || on[0].KeyID != "key-2" {
		t.Fatalf("rows: %+v", on)
	}

	// Offline: no key is left behind.
	node.offline = true
	if err := s.Enable(ctx, org, uuid.New(), pgtype.UUID{}); !errors.Is(err, ErrOffline) || len(fb.keys) != 1 {
		t.Fatalf("offline: %v, keys %v", err, fb.keys)
	}
	node.offline = false

	// Measuring: every file under the org's folder, across pages; once a day.
	pre := hosted.Prefix(org.String(), n1.String())
	for i, size := range []int64{100, 200, 300, 400, 500} {
		fb.files[pre+"p"+string(rune('a'+i))] = size
	}
	fb.files["orgs/someone-else/nodes/x/p"] = 9999
	// Deleted by the node: hidden, kept 30 days, and not the customer's to pay.
	fb.hidden[pre+"old"] = 7000
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := store.New(db).LatestBackupStorageUsage(ctx, pgtype.UUID{Bytes: org, Valid: true})
	if err != nil || u.Bytes != 1500 {
		t.Fatalf("usage: %+v, %v", u, err)
	}
	listed := countCalls(fb, "b2_list_file_versions")
	if err := s.Tick(ctx); err != nil || countCalls(fb, "b2_list_file_versions") != listed {
		t.Fatalf("measured twice in a day: %v", err)
	}

	// Turning it off: the key goes now, the files 30 days later.
	if err := s.Disable(ctx, n1); err != nil {
		t.Fatal(err)
	}
	if len(fb.keys) != 0 {
		t.Fatalf("keys after turning off: %v", fb.keys)
	}
	now = now.Add(29 * 24 * time.Hour)
	_ = s.Tick(ctx)
	if len(fb.files) != 6 || len(fb.hidden) != 1 {
		t.Fatalf("deleted before 30 days: %d files, %d hidden", len(fb.files), len(fb.hidden))
	}
	now = now.Add(2 * 24 * time.Hour)
	if err := s.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(fb.files) != 1 || fb.files["orgs/someone-else/nodes/x/p"] != 9999 || len(fb.hidden) != 0 {
		t.Fatalf("after 30 days: %v, hidden %v", fb.files, fb.hidden)
	}
	if err := s.Disable(ctx, n1); err != nil {
		t.Fatalf("turning off twice: %v", err)
	}
}

func countCalls(f *fakeB2, name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == name {
			n++
		}
	}
	return n
}

// hostedFor is Wings' check, for a node.
func hostedFor(d backup.Destination, nodeID string) bool {
	return backup.IsHostedFor(d, nodeID)
}

func TestEstimate(t *testing.T) {
	for _, c := range []struct {
		used  int64
		nodes int
		cents int64
	}{
		{5 << 30, 1, 0},
		{10 << 30, 1, 0},
		{10<<30 + 1e9, 1, 2}, // 1 GB over: 1.2 cents, rounded up
		{1e12 + 20<<30, 2, 1200},
		{0, 0, 0},
	} {
		if got := Estimate(c.used, c.nodes); got != c.cents {
			t.Errorf("Estimate(%d, %d) = %d, want %d", c.used, c.nodes, got, c.cents)
		}
	}
}
