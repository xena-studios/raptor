package command

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// demoFixture is a node and the Panel's demo key for it. The node trusts
// the key only when on is set (`raptor demo on`).
func demoFixture(t *testing.T, on bool) (*fixture, ed25519.PrivateKey) {
	t.Helper()
	f := newFixture(t)
	key, err := nodecmd.DemoKey(f.panelKey, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if on {
		f.x.DemoKey = key.Public().(ed25519.PublicKey)
	}
	run := func(_ context.Context, e Envelope) (any, error) {
		f.runs.Add(1)
		return nil, nil
	}
	// server.create takes the demo key for small servers; server.delete
	// (registered by newFixture) doesn't take it at all here.
	f.x.Register("server.create", Handler{Signed: Always, Run: run, Demo: func(_ context.Context, e Envelope) error {
		if string(e.Params) != `{"small":true}` {
			return errors.New("not a sandbox")
		}
		return nil
	}})
	return f, key
}

func (f *fixture) demoSign(e Envelope, key ed25519.PrivateKey) Envelope {
	if err := nodecmd.SignDemo(key, &e); err != nil {
		f.t.Fatal(err)
	}
	return e
}

// A node that didn't run `raptor demo on` refuses the demo key for
// everything, even an action whose handler would take it on a demo node:
// every normal node behaves exactly as before.
func TestDemoKeyRefusedOffDemoNodes(t *testing.T) {
	f, key := demoFixture(t, false)
	for _, action := range []string{"server.create", "server.delete", "backup.restore", "server.reinstall"} {
		params := map[string]bool{"small": true}
		_, err := f.exec(f.demoSign(f.cmd("panel:demo", action, "s1", params), key))
		if !errors.Is(err, ErrDemoOff) {
			t.Errorf("%s with the demo key on a normal node: %v", action, err)
		}
	}
	// Unsigned actions don't care: the demo signature is ignored.
	if _, err := f.exec(f.demoSign(f.cmd("panel:demo", "server.start", "s1", nil), key)); err != nil {
		t.Errorf("unsigned action: %v", err)
	}
	if f.runs.Load() != 1 {
		t.Fatalf("%d signed commands ran", f.runs.Load()-1)
	}
	// And each refusal is in the audit log.
	entries, err := ListAudit(context.Background(), f.db, f.now.Add(-1), 10)
	if err != nil || len(entries) != 4 {
		t.Fatalf("audit: %d entries, %v", len(entries), err)
	}
	if entries[0].Outcome != AuditRejected || entries[0].KeyName != "Panel's demo key" {
		t.Errorf("audit entry: %+v", entries[0])
	}
}

// A demo node takes the demo key only for actions that accept it, and
// only when their check passes; everything else still needs a passkey.
func TestDemoKeyOnDemoNode(t *testing.T) {
	f, key := demoFixture(t, true)
	small := map[string]bool{"small": true}

	// The one action here that takes it, within its limits.
	if _, err := f.exec(f.demoSign(f.cmd("panel:demo", "server.create", "", small), key)); err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	// Outside its limits.
	if _, err := f.exec(f.demoSign(f.cmd("panel:demo", "server.create", "", map[string]bool{"small": false}), key)); !errors.Is(err, ErrDemoRefused) {
		t.Errorf("big create: %v", err)
	}
	// Actions that don't take it, signed or not.
	for _, action := range []string{"server.delete", "server.reinstall", "backup.restore"} {
		if _, err := f.exec(f.demoSign(f.cmd("panel:demo", action, "s1", small), key)); !errors.Is(err, ErrDemoRefused) {
			t.Errorf("%s: %v", action, err)
		}
	}
	// Another node's demo key (or any other key) doesn't verify.
	other, _ := nodecmd.DemoKey(f.panelKey, "node-2")
	if _, err := f.exec(f.demoSign(f.cmd("panel:demo", "server.create", "", small), other)); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("another node's demo key: %v", err)
	}
	_, random, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := f.exec(f.demoSign(f.cmd("panel:demo", "server.create", "", small), random)); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("a random key: %v", err)
	}
	// The signature covers the whole command: changing the params after
	// signing (a bigger server) breaks it.
	e := f.demoSign(f.cmd("panel:demo", "server.create", "", small), key)
	e.Params = []byte(`{"small":true,"memory":99999}`)
	e.Grant = f.grant(e)
	if _, err := f.exec(e); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("params changed after signing: %v", err)
	}
	// Without any signature it's still a passkey action.
	if _, err := f.exec(f.cmd("panel:demo", "server.create", "", small)); !errors.Is(err, ErrSignatureNeeded) {
		t.Errorf("unsigned create: %v", err)
	}
	// Staff under support access can't use it either.
	s := f.cmd("staff", "server.create", "", small)
	s.Grant.Support = "grant-1"
	p, _ := s.Grant.Payload()
	s.Grant.Signature = ed25519.Sign(f.panelKey, p)
	if _, err := f.exec(f.demoSign(s, key)); err == nil {
		t.Error("support access with the demo key ran")
	}
	if f.runs.Load() != 1 {
		t.Fatalf("%d commands ran, want 1", f.runs.Load())
	}
}

func TestDemoKeyDerivation(t *testing.T) {
	_, panel, _ := ed25519.GenerateKey(rand.Reader)
	a, err := nodecmd.DemoKey(panel, "n1")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := nodecmd.DemoKey(panel, "n1")
	b, _ := nodecmd.DemoKey(panel, "n2")
	if !a.Equal(again) || a.Equal(b) || a.Equal(panel) {
		t.Error("a demo key is the same every time for a node, and differs per node and from the Panel's key")
	}
	if _, err := nodecmd.DemoKey(nil, "n1"); err == nil {
		t.Error("no Panel key")
	}
}
