package command

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

func (f *fixture) auditLog() []AuditEntry {
	f.t.Helper()
	l, err := ListAudit(context.Background(), f.db, time.Time{}, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	return l
}

// Every signed command is in the audit log with its outcome, including
// rejected ones; unsigned commands aren't.
func TestAuditLog(t *testing.T) {
	f := newFixture(t)
	var notified []string
	f.x.OnAudit = func(a AuditEntry) { notified = append(notified, a.Action+":"+a.Outcome) }
	owner := newAuthenticator(t, "ES256")
	f.trust(owner, KeyParams{UserID: "alice", Name: "alice's phone"})

	if _, err := f.exec(f.cmd("alice", "server.start", "s1", nil)); err != nil {
		t.Fatal(err)
	}
	e := f.sign(f.cmd("alice", "server.delete", "s1", nil), owner)
	if _, err := f.exec(e); err != nil {
		t.Fatal(err)
	}
	if _, err := f.exec(e); err != nil { // a retry isn't a second action
		t.Fatal(err)
	}
	if _, err := f.exec(f.cmd("mallory", "server.delete", "s1", nil)); !errors.Is(err, ErrSignatureNeeded) {
		t.Fatalf("unsigned: %v", err)
	}
	stranger := newAuthenticator(t, "ES256")
	if _, err := f.exec(f.sign(f.cmd("alice", "server.reinstall", "s2", nil), stranger)); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("untrusted key: %v", err)
	}

	l := f.auditLog()
	if len(l) != 3 {
		t.Fatalf("%d entries: %+v", len(l), l)
	}
	del := l[2]
	h, _ := e.Hash()
	if del.Action != "server.delete" || del.Outcome != AuditOK || del.UserID != "alice" || del.KeyName != "alice's phone" ||
		del.Fingerprint != KeyFingerprint(owner.COSE) || string(del.CommandHash) != string(h) || del.CommandID != e.CommandID {
		t.Errorf("delete: %+v", del)
	}
	if l[1].Outcome != AuditRejected || !strings.Contains(l[1].Detail, "must be signed") {
		t.Errorf("unsigned attempt: %+v", l[1])
	}
	if l[0].Outcome != AuditRejected || l[0].Action != "server.reinstall" || l[0].Fingerprint != "" {
		t.Errorf("untrusted key: %+v", l[0])
	}
	if strings.Join(notified, " ") != "server.delete:ok server.delete:rejected server.reinstall:rejected" {
		t.Errorf("notified: %v", notified)
	}

	// Kept a year.
	f.now = f.now.Add(366 * 24 * time.Hour)
	if _, err := f.x.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(f.auditLog()); n != 0 {
		t.Errorf("%d entries after a year", n)
	}
}

func TestKeyFingerprint(t *testing.T) {
	a := newAuthenticator(t, "ES256")
	fp := KeyFingerprint(a.COSE)
	if !regexp.MustCompile(`^[A-Z2-7]{4}(-[A-Z2-7]{4}){4}$`).MatchString(fp) || fp != KeyFingerprint(a.COSE) {
		t.Errorf("fingerprint %q", fp)
	}
	if fp == KeyFingerprint(newAuthenticator(t, "ES256").COSE) {
		t.Error("two keys share a fingerprint")
	}
}

func (f *fixture) pairCmd(user, code string, key *authenticator) Envelope {
	e := f.cmd(user, ActionKeysPair, "", PairParams{Code: code, CredentialID: key.CredentialID, PublicKey: key.COSE, UserID: user, Name: "new laptop"})
	return f.sign(e, key)
}

// `raptor keys reset`: a code on the box, a pairing from the Panel signed by
// the new key, and root confirming its fingerprint on the box.
func TestPairing(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.x.Pairing = &Pairing{}
	lost := newAuthenticator(t, "ES256")
	f.trust(lost, KeyParams{UserID: "alice"})
	helper := newAuthenticator(t, "ES256")
	f.trust(helper, KeyParams{UserID: "bob", Role: "delegate", Actions: []string{"server.reinstall"}})
	fresh := newAuthenticator(t, "ES256")

	if _, err := f.exec(f.pairCmd("alice", "AAAAA-AAAAA", fresh)); !errors.Is(err, ErrNoPairing) {
		t.Fatalf("pairing with no reset: %v", err)
	}
	id, code, _, err := f.x.Pairing.Start("local:root", f.now)
	if err != nil || !regexp.MustCompile(`^[A-Z2-9]{5}-[A-Z2-9]{5}$`).MatchString(code) {
		t.Fatalf("start: %q, %v", code, err)
	}

	// The code alone isn't enough: the command must be signed by the key
	// being paired, for the user pairing it.
	e := f.cmd("alice", ActionKeysPair, "", PairParams{Code: code, CredentialID: fresh.CredentialID, PublicKey: fresh.COSE, UserID: "alice"})
	if _, err := f.exec(f.sign(e, newAuthenticator(t, "ES256"))); !errors.Is(err, ErrSignatureNeeded) {
		t.Fatalf("signed by another key: %v", err)
	}
	if _, err := f.exec(f.pairCmd("mallory", strings.ToLower(code), fresh)); err != nil {
		t.Fatalf("pairing (lowercase code): %v", err)
	}
	st, _ := f.x.Pairing.Status(id, f.now)
	if st.State != PairingPending || st.Fingerprint != KeyFingerprint(fresh.COSE) || st.UserID != "mallory" {
		t.Fatalf("status: %+v", st)
	}
	// Not trusted until root confirms, and only with the fingerprint shown.
	if _, err := f.exec(f.sign(f.cmd("mallory", "server.delete", "s1", nil), fresh)); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("before confirming: %v", err)
	}
	if err := f.x.ConfirmPairing(ctx, id, "AAAA-BBBB-CCCC-DDDD-EEEE"); !errors.Is(err, ErrPairingState) {
		t.Fatalf("wrong fingerprint: %v", err)
	}
	if err := f.x.ConfirmPairing(ctx, id, st.Fingerprint); err != nil {
		t.Fatal(err)
	}
	keys, _ := ListKeys(ctx, f.db)
	if len(keys) != 1 || keys[0].Fingerprint != st.Fingerprint || keys[0].Role != "owner" || keys[0].AddedBy != "" || keys[0].Name != "new laptop" {
		t.Fatalf("keys after the reset: %+v", keys)
	}
	if _, err := f.exec(f.sign(f.cmd("mallory", "server.delete", "s1", nil), fresh)); err != nil {
		t.Fatalf("new key: %v", err)
	}
	if _, err := f.exec(f.sign(f.cmd("alice", "server.delete", "s1", nil), lost)); !errors.Is(err, ErrUntrustedKey) {
		t.Fatalf("the lost key still works: %v", err)
	}
	if err := f.x.ConfirmPairing(ctx, id, st.Fingerprint); !errors.Is(err, ErrPairingState) {
		t.Fatalf("confirming twice: %v", err)
	}
	var reset *AuditEntry
	for _, a := range f.auditLog() {
		if a.Action == "keys.reset" {
			reset = &a
		}
	}
	if reset == nil || reset.UserID != "local:root" || !strings.Contains(reset.Detail, "removed 2 key(s)") {
		t.Fatalf("reset audit: %+v", reset)
	}
}

func TestPairingLimits(t *testing.T) {
	f := newFixture(t)
	f.x.Pairing = &Pairing{}
	fresh := newAuthenticator(t, "ES256")
	id, code, _, _ := f.x.Pairing.Start("local:root", f.now)

	// Wrong codes: after five, the reset is cancelled.
	for i := range maxPairingTries {
		if _, err := f.exec(f.pairCmd("alice", "WRONG-CODE"+string(rune('A'+i)), fresh)); !errors.Is(err, ErrPairingCode) {
			t.Fatalf("wrong code %d: %v", i, err)
		}
	}
	if _, err := f.exec(f.pairCmd("alice", code, fresh)); !errors.Is(err, ErrPairingState) {
		t.Fatalf("right code after too many tries: %v", err)
	}
	if st, _ := f.x.Pairing.Status(id, f.now); st.State != PairingExpired {
		t.Errorf("state: %s", st.State)
	}

	// Codes expire.
	id, code, _, _ = f.x.Pairing.Start("local:root", f.now)
	f.now = f.now.Add(PairingTTL + time.Second)
	if _, err := f.exec(f.pairCmd("alice", code, fresh)); !errors.Is(err, ErrPairingState) {
		t.Fatalf("expired code: %v", err)
	}
	// A cancelled reset changes nothing.
	id2, code2, _, _ := f.x.Pairing.Start("local:root", f.now)
	f.x.Pairing.Cancel(id2)
	if _, err := f.exec(f.pairCmd("alice", code2, fresh)); !errors.Is(err, ErrPairingState) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := f.x.Pairing.Status(id, f.now); !errors.Is(err, ErrNoPairing) {
		t.Errorf("an older reset: %v", err)
	}
}
