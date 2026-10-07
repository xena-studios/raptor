package support_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/support"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/doctor"
)

type memStore struct {
	mu   sync.Mutex
	objs map[string]map[string]string // key → metadata
}

func (m *memStore) Put(_ context.Context, key string, _ []byte, meta map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[key] = meta
	return nil
}

func (m *memStore) find(code string) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range m.objs {
		if strings.HasSuffix(k, "/"+code+".tar.gz") {
			return v
		}
	}
	return nil
}

func bundle(t *testing.T, dir string) string {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	_, _ = gz.Write([]byte("doctor.txt"))
	_ = gz.Close()
	p := filepath.Join(dir, "bundle.tar.gz")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var codeRE = regexp.MustCompile(`^RPT-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

func TestUpload(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	st := &memStore{objs: map[string]map[string]string{}}
	now := time.Now()
	svc := &support.Service{DB: db, Store: st, Limiter: &auth.Service{DB: db}, ClientIPHeader: "CF-Connecting-IP", Now: func() time.Time { return now }}
	ip := "203.0.113.7"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("CF-Connecting-IP", ip)
		svc.ServeHTTP(w, r)
	}))
	defer srv.Close()
	path := bundle(t, t.TempDir())

	// A linked node signs its upload.
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	var org, node string
	if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('Acme') RETURNING id").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'abcd1234', $2) RETURNING id", org, []byte(pub)).Scan(&node); err != nil {
		t.Fatal(err)
	}
	code, err := doctor.Upload(ctx, srv.Client(), srv.URL, path, node, key, now)
	if err != nil || !codeRE.MatchString(code) {
		t.Fatalf("signed: %q, %v", code, err)
	}
	if m := st.find(code); m == nil || m["node"] != node || m["ip"] != ip || len(m["sha256"]) != 64 {
		t.Errorf("stored: %v", m)
	}

	// Signatures that don't check out are refused, not filed anonymously.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	for what, try := range map[string]func() (string, error){
		"another key": func() (string, error) { return doctor.Upload(ctx, srv.Client(), srv.URL, path, node, other, now) },
		"an old one": func() (string, error) {
			return doctor.Upload(ctx, srv.Client(), srv.URL, path, node, key, now.Add(-10*time.Minute))
		},
		"unknown node": func() (string, error) {
			return doctor.Upload(ctx, srv.Client(), srv.URL, path, "01a10f2a-865c-74ec-9578-3fe2ea243894", key, now)
		},
		"not a node ID": func() (string, error) { return doctor.Upload(ctx, srv.Client(), srv.URL, path, "box", key, now) },
	} {
		if code, err := try(); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Errorf("%s: %q, %v", what, code, err)
		}
	}
	if _, err := db.Exec(ctx, "UPDATE nodes SET key_revoked_at = now() WHERE id = $1", node); err != nil {
		t.Fatal(err)
	}
	if _, err := doctor.Upload(ctx, srv.Client(), srv.URL, path, node, key, now); err == nil {
		t.Error("a revoked node's upload was taken")
	}

	// Not a bundle, or too big.
	for what, body := range map[string][]byte{"not gzip": []byte("hello"), "too big": append([]byte{0x1f, 0x8b}, make([]byte, support.MaxBundle)...)} {
		res, err := srv.Client().Post(srv.URL+nodelink.BundlePath, "application/gzip", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: %s", what, res.Status)
		}
	}

	// Unlinked nodes upload anonymously, a few a day per address.
	for i := range support.LimitPerIP.N {
		code, err := doctor.Upload(ctx, srv.Client(), srv.URL, path, "", nil, now)
		if err != nil || st.find(code) == nil || st.find(code)["node"] != "" {
			t.Fatalf("anonymous %d: %q, %v", i, code, err)
		}
	}
	if _, err := doctor.Upload(ctx, srv.Client(), srv.URL, path, "", nil, now); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("past the limit: %v", err)
	}
	ip = "203.0.113.8"
	if _, err := doctor.Upload(ctx, srv.Client(), srv.URL, path, "", nil, now); err != nil {
		t.Errorf("another address: %v", err)
	}
}

func TestCode(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		c, err := support.NewCode()
		if err != nil || !codeRE.MatchString(c) || seen[c] {
			t.Fatalf("%q, %v", c, err)
		}
		seen[c] = true
	}
}
