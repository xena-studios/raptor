package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
	"github.com/xena-studios/raptor/internal/wings/store"
	"github.com/xena-studios/raptor/internal/wings/support"
)

// Support access end to end on the node: the owner's passkey grants it;
// staff commands are checked against it; staff can't send signed actions.
func TestSupportAccess(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pub, panelKey, _ := ed25519.GenerateKey(rand.Reader)
	rp := command.RelyingParty{Origin: "https://app.raptorpanel.net", ID: "app.raptorpanel.net"}
	x := &command.Executor{DB: db, NodeID: "node-1", RP: rp, PanelKey: pub}
	now := time.Now()
	m := &support.Manager{DB: db, Now: func() time.Time { return now }}
	x.Support = m
	RegisterSupport(x, m)
	ran := map[string]int{}
	stub := func(signed bool, readOnly bool) command.Handler {
		s := command.Never
		if signed {
			s = command.Always
		}
		return command.Handler{Signed: s, ReadOnly: readOnly, Run: func(_ context.Context, e command.Envelope) (any, error) {
			ran[e.Action]++
			return map[string]bool{"ok": true}, nil
		}}
	}
	x.Register("node.health", stub(false, true))
	x.Register("server.stop", stub(false, false))
	x.Register("server.delete", stub(true, false))

	owner, _ := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	if err := command.AddKey(ctx, db, command.KeyParams{CredentialID: owner.CredentialID, PublicKey: owner.COSE, UserID: "owner-1", Role: "owner"}, nil, now); err != nil {
		t.Fatal(err)
	}
	delegate, _ := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	if err := command.AddKey(ctx, db, command.KeyParams{CredentialID: delegate.CredentialID, PublicKey: delegate.COSE, UserID: "admin-1", Role: "delegate", Actions: []string{SupportGrant}}, nil, now); err != nil {
		t.Fatal(err)
	}

	send := func(user, action, server, grantID string, params any, signer *commandtest.Authenticator) error {
		t.Helper()
		id, _ := uuid.NewV7()
		raw, _ := json.Marshal(params)
		e := command.Envelope{CommandID: id.String(), NodeID: "node-1", UserID: user, Action: action, ServerID: server, Params: raw, ExpiresAt: time.Now().Add(time.Minute).Unix()}
		e.Grant = command.Grant{UserID: user, NodeID: "node-1", CommandID: e.CommandID, Action: action, ServerID: server, ExpiresAt: e.ExpiresAt, Support: grantID}
		p, _ := e.Grant.Payload()
		e.Grant.Signature = ed25519.Sign(panelKey, p)
		if signer != nil {
			h, _ := e.Hash()
			ad, cd, sig := signer.Assert(h)
			e.Signature = &command.PasskeySignature{CredentialID: signer.CredentialID, AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
		}
		_, err := x.Execute(ctx, e)
		return err
	}

	staff := uuid.NewString()
	grantID := uuid.NewString()
	grant := support.Grant{
		ID: grantID, StaffID: staff, StaffEmail: "support@raptor.example", Level: support.Diagnostics,
		Reason: "server won't start", Ticket: "T-123", ExpiresAt: now.Add(time.Hour).UnixMilli(),
	}
	// Unsigned, or signed by a delegate: refused. Only an owner grants it.
	if err := send("owner-1", SupportGrant, "", "", grant, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned grant: %v", err)
	}
	if err := send("admin-1", SupportGrant, "", "", grant, delegate); err == nil {
		t.Fatal("a delegate granted support access")
	}
	long := grant
	long.ExpiresAt = now.Add(8 * 24 * time.Hour).UnixMilli()
	if err := send("owner-1", SupportGrant, "", "", long, owner); !errors.Is(err, support.ErrInvalid) {
		t.Errorf("8 days: %v", err)
	}
	if err := send("owner-1", SupportGrant, "", "", grant, owner); err != nil {
		t.Fatal(err)
	}

	// Staff under the grant: what level 1 covers, and nothing else.
	if err := send(staff, "node.health", "", grantID, nil, nil); err != nil {
		t.Errorf("diagnostics: %v", err)
	}
	if err := send(staff, "server.stop", "s1", grantID, nil, nil); !errors.Is(err, support.ErrLevel) {
		t.Errorf("stopping a server at level 1: %v", err)
	}
	if err := send(staff, "server.delete", "s1", grantID, nil, owner); !errors.Is(err, command.ErrSupportSigned) && !errors.Is(err, support.ErrLevel) {
		t.Errorf("a signed action from staff: %v", err)
	}
	if err := send(uuid.NewString(), "node.health", "", grantID, nil, nil); !errors.Is(err, support.ErrNoGrant) {
		t.Errorf("another staff member on the grant: %v", err)
	}
	if err := send(staff, "node.health", "", uuid.NewString(), nil, nil); !errors.Is(err, support.ErrNoGrant) {
		t.Errorf("a made-up grant: %v", err)
	}
	if ran["server.stop"] != 0 || ran["server.delete"] != 0 || ran["node.health"] != 1 {
		t.Errorf("ran: %v", ran)
	}

	// Over time, or ended by the owner: gone.
	now = now.Add(2 * time.Hour)
	if err := send(staff, "node.health", "", grantID, nil, nil); !errors.Is(err, support.ErrNoGrant) {
		t.Errorf("after it ran out: %v", err)
	}
	now = time.Now()
	g2 := grant
	g2.ID, g2.Level = uuid.NewString(), support.Operate
	if err := send("owner-1", SupportGrant, "", "", g2, owner); err != nil {
		t.Fatal(err)
	}
	if err := send(staff, "server.stop", "s1", g2.ID, nil, nil); err != nil {
		t.Errorf("stopping at level 2: %v", err)
	}
	if err := send("owner-1", SupportRevoke, "", "", SupportRevokeParams{ID: g2.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := send(staff, "node.health", "", g2.ID, nil, nil); !errors.Is(err, support.ErrNoGrant) {
		t.Errorf("after revoking: %v", err)
	}

	// Turned off: no new grants, and none work.
	if err := send("owner-1", SupportDisable, "", "", SupportDisableParams{Off: true}, nil); err != nil {
		t.Fatal(err)
	}
	g3 := grant
	g3.ID = uuid.NewString()
	if err := send("owner-1", SupportGrant, "", "", g3, owner); !errors.Is(err, support.ErrDisabled) {
		t.Errorf("granting while off: %v", err)
	}
	list, _ := m.List(ctx)
	if len(list) != 2 || !m.Disabled(ctx) {
		t.Errorf("grants: %+v", list)
	}
}
