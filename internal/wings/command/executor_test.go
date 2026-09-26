package command

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/store"
)

var testRP = RelyingParty{Origin: "https://raptorpanel.net", ID: "raptorpanel.net"}

const nodeID = "node-1"

type fixture struct {
	t        *testing.T
	x        *Executor
	db       *store.DB
	panelKey ed25519.PrivateKey
	runs     atomic.Int32
	now      time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fixture{t: t, db: db, panelKey: priv, now: time.Unix(1_800_000_000, 0)}
	f.x = &Executor{DB: db, NodeID: nodeID, RP: testRP, PanelKey: pub, Now: func() time.Time { return f.now }}
	run := func(_ context.Context, e Envelope) (any, error) {
		f.runs.Add(1)
		return map[string]string{"did": e.Action}, nil
	}
	f.x.Register("server.start", Handler{Signed: Never, Run: run})
	f.x.Register("server.delete", Handler{Signed: Always, Run: run})
	f.x.Register("server.reinstall", Handler{Signed: Always, Run: run})
	f.x.Register("server.explode", Handler{Signed: Never, Run: func(context.Context, Envelope) (any, error) { panic("boom") }})
	return f
}

// cmd builds a command with a valid Panel grant.
func (f *fixture) cmd(user, action, server string, params any) Envelope {
	id, _ := uuid.NewV7()
	raw, _ := json.Marshal(params)
	e := Envelope{CommandID: id.String(), NodeID: nodeID, UserID: user, Action: action, ServerID: server, Params: raw, ExpiresAt: f.now.Add(5 * time.Minute).Unix()}
	e.Grant = f.grant(e)
	return e
}

func (f *fixture) grant(e Envelope) Grant {
	g := Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, ExpiresAt: e.ExpiresAt}
	p, err := g.payload()
	if err != nil {
		f.t.Fatal(err)
	}
	g.Signature = ed25519.Sign(f.panelKey, p)
	return g
}

func (f *fixture) sign(e Envelope, a *authenticator) Envelope {
	h, err := e.Hash()
	if err != nil {
		f.t.Fatal(err)
	}
	e.Signature = a.assert(h)
	return e
}

func (f *fixture) trust(a *authenticator, p KeyParams) {
	p.CredentialID, p.PublicKey = a.credID, a.cose
	if p.Role == "" {
		p.Role = "owner"
	}
	if err := AddKey(context.Background(), f.db, p, nil, f.now); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) exec(e Envelope) (Result, error) { return f.x.Execute(context.Background(), e) }

func TestUnsignedCommandAndIdempotency(t *testing.T) {
	f := newFixture(t)
	e := f.cmd("alice", "server.start", "s1", nil)
	res, err := f.exec(e)
	if err != nil || res.Duplicate || string(res.Value) != `{"did":"server.start"}` {
		t.Fatalf("first: %+v %v", res, err)
	}
	// A retry (e.g. after the connection dropped) returns the stored result
	// and doesn't run again, even after the command expired.
	f.now = f.now.Add(time.Hour)
	res, err = f.exec(e)
	if err != nil || !res.Duplicate || f.runs.Load() != 1 {
		t.Fatalf("retry: %+v %v runs=%d", res, err, f.runs.Load())
	}
	// Reusing the ID for different content is refused.
	e2 := e
	e2.ServerID = "s2"
	e2.Grant = f.grant(e2)
	if _, err := f.exec(e2); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused ID: %v", err)
	}
}

func TestExpiryAndGrants(t *testing.T) {
	f := newFixture(t)
	e := f.cmd("alice", "server.start", "s1", nil)
	cases := map[string]func(*Envelope){
		"expired":              func(e *Envelope) { e.ExpiresAt = f.now.Add(-time.Second).Unix(); e.Grant = f.grant(*e) },
		"too far out":          func(e *Envelope) { e.ExpiresAt = f.now.Add(time.Hour).Unix(); e.Grant = f.grant(*e) },
		"forged grant":         func(e *Envelope) { e.Grant.Signature = make([]byte, ed25519.SignatureSize) },
		"grant for other user": func(e *Envelope) { g := f.grant(*e); e.UserID = "mallory"; e.Grant = g },
		"grant for other cmd":  func(e *Envelope) { g := f.grant(*e); e.Action = "server.delete"; e.Grant = g },
		"other node":           func(e *Envelope) { e.NodeID = "node-2"; e.Grant = f.grant(*e) },
		"bad id":               func(e *Envelope) { e.CommandID = "not-a-uuid"; e.Grant = f.grant(*e) },
		"unknown action":       func(e *Envelope) { e.Action = "server.teleport"; e.Grant = f.grant(*e) },
	}
	for name, mutate := range cases {
		c := e
		mutate(&c)
		if _, err := f.exec(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	f.x.PanelKey = nil
	if _, err := f.exec(f.cmd("alice", "server.start", "s1", nil)); !errors.Is(err, ErrNotEnrolled) {
		t.Errorf("no Panel key: %v", err)
	}
	if f.runs.Load() != 0 {
		t.Fatalf("a rejected command ran")
	}
}

func TestSignedCommand(t *testing.T) {
	for _, alg := range []string{"ES256", "EdDSA", "RS256"} {
		t.Run(alg, func(t *testing.T) {
			f := newFixture(t)
			a := newAuthenticator(t, alg)
			e := f.cmd("alice", "server.delete", "s1", map[string]bool{"final_backup": true})

			if _, err := f.exec(e); !errors.Is(err, ErrSignatureNeeded) {
				t.Fatalf("unsigned: %v", err)
			}
			if _, err := f.exec(f.sign(e, a)); !errors.Is(err, ErrNoTrustedKeys) {
				t.Fatalf("no keys pinned: %v", err)
			}
			f.trust(a, KeyParams{UserID: "alice"})
			res, err := f.exec(f.sign(e, a))
			if err != nil || f.runs.Load() != 1 {
				t.Fatalf("signed: %+v %v", res, err)
			}
			// Replaying the signed command returns the stored result only.
			if res, err := f.exec(f.sign(e, a)); err != nil || !res.Duplicate || f.runs.Load() != 1 {
				t.Fatalf("replay: %+v %v runs=%d", res, err, f.runs.Load())
			}
		})
	}
}

// Every way a compromised Panel (or anyone else) could try to get a
// dangerous command through without the user's real signature.
func TestSignatureAttacks(t *testing.T) {
	f := newFixture(t)
	a := newAuthenticator(t, "ES256")
	f.trust(a, KeyParams{UserID: "alice"})
	other := newAuthenticator(t, "ES256")
	f.trust(other, KeyParams{UserID: "bob"})

	attacks := map[string]func() Envelope{
		"params changed after signing": func() Envelope {
			e := f.sign(f.cmd("alice", "server.delete", "s1", nil), a)
			e.ServerID = "s2"
			e.Grant = f.grant(e)
			return e
		},
		"signature moved to another command": func() Envelope {
			s := f.sign(f.cmd("alice", "server.delete", "s1", nil), a).Signature
			e := f.cmd("alice", "server.delete", "s2", nil)
			e.Signature = s
			return e
		},
		"another user's key": func() Envelope { return f.sign(f.cmd("alice", "server.delete", "s1", nil), other) },
		"untrusted key": func() Envelope {
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), newAuthenticator(t, "ES256"))
		},
		"phishing origin": func() Envelope {
			b := *a
			b.origin = "https://raptorpanel.net.evil.example"
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), &b)
		},
		"other RP ID": func() Envelope {
			b := *a
			b.rpID = "evil.example"
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), &b)
		},
		"no user verification": func() Envelope {
			b := *a
			b.flags = flagUserPresent
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), &b)
		},
		"no user presence": func() Envelope {
			b := *a
			b.flags = flagUserVerified
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), &b)
		},
		"registration, not assertion": func() Envelope {
			b := *a
			b.typ = "webauthn.create"
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), &b)
		},
		"cross-origin iframe": func() Envelope {
			b := *a
			b.crossOrigin = true
			return f.sign(f.cmd("alice", "server.delete", "s1", nil), &b)
		},
		"garbage signature": func() Envelope {
			e := f.sign(f.cmd("alice", "server.delete", "s1", nil), a)
			e.Signature.Signature = []byte("nope")
			return e
		},
	}
	for name, attack := range attacks {
		_, err := f.exec(attack())
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		t.Logf("%-36s → %v", name, err)
	}
	if f.runs.Load() != 0 {
		t.Fatalf("%d dangerous commands ran", f.runs.Load())
	}
}

func TestSignCounter(t *testing.T) {
	f := newFixture(t)
	a := newAuthenticator(t, "EdDSA")
	f.trust(a, KeyParams{UserID: "alice"})
	a.counter = 5
	if _, err := f.exec(f.sign(f.cmd("alice", "server.delete", "s1", nil), a)); err != nil {
		t.Fatal(err)
	}
	a.counter = 5 // a clone replaying the same counter
	if _, err := f.exec(f.sign(f.cmd("alice", "server.delete", "s2", nil), a)); err == nil || !strings.Contains(err.Error(), "counter") {
		t.Fatalf("non-increasing counter: %v", err)
	}
	a.counter = 6
	if _, err := f.exec(f.sign(f.cmd("alice", "server.delete", "s3", nil), a)); err != nil {
		t.Fatal(err)
	}
}

func TestDelegation(t *testing.T) {
	f := newFixture(t)
	owner := newAuthenticator(t, "ES256")
	f.trust(owner, KeyParams{UserID: "alice"})
	helper := newAuthenticator(t, "ES256")
	f.trust(helper, KeyParams{UserID: "bob", Role: "delegate", ServerID: "s1", Actions: []string{"server.reinstall"}, ExpiresAt: f.now.Add(time.Hour).Unix()})

	if _, err := f.exec(f.sign(f.cmd("bob", "server.reinstall", "s1", nil), helper)); err != nil {
		t.Fatalf("delegated action: %v", err)
	}
	for name, e := range map[string]Envelope{
		"other action":   f.cmd("bob", "server.delete", "s1", nil),
		"other server":   f.cmd("bob", "server.reinstall", "s2", nil),
		"key management": f.cmd("bob", ActionKeysAdd, "", KeyParams{CredentialID: []byte("x"), UserID: "bob", PublicKey: helper.cose, Role: "owner"}),
	} {
		if _, err := f.exec(f.sign(e, helper)); !errors.Is(err, ErrUntrustedKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
	f.now = f.now.Add(2 * time.Hour)
	if _, err := f.exec(f.sign(f.cmd("bob", "server.reinstall", "s1", nil), helper)); !errors.Is(err, ErrUntrustedKey) {
		t.Errorf("expired delegation: %v", err)
	}
}

func TestKeyManagement(t *testing.T) {
	f := newFixture(t)
	owner := newAuthenticator(t, "ES256")
	f.trust(owner, KeyParams{UserID: "alice", Name: "phone"})
	laptop := newAuthenticator(t, "EdDSA")

	add := f.cmd("alice", ActionKeysAdd, "", KeyParams{CredentialID: laptop.credID, UserID: "alice", PublicKey: laptop.cose, Role: "owner", Name: "laptop"})
	if _, err := f.exec(add); !errors.Is(err, ErrSignatureNeeded) {
		t.Fatalf("unsigned key add: %v", err)
	}
	if _, err := f.exec(f.sign(add, owner)); err != nil {
		t.Fatalf("signed key add: %v", err)
	}
	// The new key works…
	if _, err := f.exec(f.sign(f.cmd("alice", "server.delete", "s1", nil), laptop)); err != nil {
		t.Fatalf("new key: %v", err)
	}
	// …and records who added it.
	k, _ := f.db.Read.GetTrustedKey(context.Background(), laptop.credID)
	if string(k.AddedBy) != string(owner.credID) {
		t.Error("added_by not recorded")
	}

	rm := func(k *authenticator, by *authenticator) error {
		_, err := f.exec(f.sign(f.cmd("alice", ActionKeysRemove, "", RemoveKeyParams{CredentialID: k.credID}), by))
		return err
	}
	if err := rm(owner, laptop); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := rm(laptop, laptop); err == nil || !strings.Contains(err.Error(), "last owner") {
		t.Fatalf("removing the last owner key: %v", err)
	}
}

func TestPanicAndInterrupted(t *testing.T) {
	f := newFixture(t)
	e := f.cmd("alice", "server.explode", "s1", nil)
	if _, err := f.exec(e); err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic: %v", err)
	}
	// The ID isn't stuck "running".
	if _, err := f.exec(e); err == nil || errors.Is(err, ErrInProgress) {
		t.Fatalf("after panic: %v", err)
	}

	// A command left running by a Wings crash is marked failed on start.
	id, _ := uuid.NewV7()
	_, _ = f.db.Write.ClaimCommand(context.Background(), store.ClaimCommandParams{CommandID: id.String(), PayloadHash: []byte("h"), Action: "server.start", ReceivedAt: f.now.UnixMilli()})
	if err := f.x.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	row, _ := f.db.Read.GetCommand(context.Background(), id.String())
	if row.Status != "failed" || !strings.Contains(row.Error, "interrupted") {
		t.Fatalf("interrupted command: %+v", row)
	}
}

// The canonical form is RFC 8785: key order and whitespace don't matter.
func TestCanonical(t *testing.T) {
	a := Envelope{CommandID: "c", NodeID: "n", UserID: "u", Action: "a", Params: json.RawMessage(`{"b": 1, "a": [true, "x"]}`), ExpiresAt: 5}
	b := a
	b.Params = json.RawMessage(`{"a":[true,"x"],"b":1.0}`)
	ca, _ := a.Canonical()
	cb, _ := b.Canonical()
	want := `{"action":"a","command_id":"c","expires_at":5,"node_id":"n","params":{"a":[true,"x"],"b":1},"server_id":"","user_id":"u"}`
	if string(ca) != want || string(cb) != want {
		t.Fatalf("canonical:\n%s\n%s", ca, cb)
	}
}
