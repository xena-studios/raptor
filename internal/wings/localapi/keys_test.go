package localapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// A key reset end to end: started on the box, paired from the "Panel" with
// a new passkey, confirmed on the box.
func TestKeyReset(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rp := command.RelyingParty{Origin: "https://app.raptorpanel.net", ID: "app.raptorpanel.net"}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	x := &command.Executor{DB: db, NodeID: "node-1", RP: rp, PanelKey: pub, Pairing: &command.Pairing{}}
	s := &Service{}
	if _, err := s.ListKeys(userCtx, &localv1.ListKeysRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("before the executor: %v", err)
	}
	s.SetCommands(x)

	lost, _ := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	if err := command.AddKey(ctx, db, command.KeyParams{CredentialID: lost.CredentialID, UserID: "alice", PublicKey: lost.COSE, Role: "owner"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	keys, err := s.ListKeys(userCtx, &localv1.ListKeysRequest{})
	if err != nil || len(keys.GetKeys()) != 1 || keys.GetKeys()[0].GetFingerprint() != command.KeyFingerprint(lost.COSE) {
		t.Fatalf("list (raptor group): %v, %v", keys, err)
	}

	if _, err := s.StartKeyReset(userCtx, &localv1.StartKeyResetRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("reset as non-root: %v", err)
	}
	start, err := s.StartKeyReset(rootCtx, &localv1.StartKeyResetRequest{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.GetKeyReset(rootCtx, &localv1.GetKeyResetRequest{ResetId: start.GetResetId()})
	if err != nil || st.GetState() != "waiting" {
		t.Fatalf("status: %v, %v", st, err)
	}

	// The Panel relays the pairing, signed by the new passkey.
	fresh, _ := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	params, _ := json.Marshal(command.PairParams{Code: start.GetCode(), CredentialID: fresh.CredentialID, PublicKey: fresh.COSE, UserID: "alice", Name: "laptop"})
	e := command.Envelope{CommandID: uuid.NewString(), NodeID: "node-1", UserID: "alice", Action: command.ActionKeysPair, Params: params, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ExpiresAt: e.ExpiresAt}
	payload, _ := e.Grant.Payload()
	e.Grant.Signature = ed25519.Sign(priv, payload)
	h, _ := e.Hash()
	ad, cd, sig := fresh.Assert(h)
	e.Signature = &command.PasskeySignature{CredentialID: fresh.CredentialID, AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
	if _, err := x.Execute(ctx, e); err != nil {
		t.Fatalf("pair: %v", err)
	}

	st, err = s.GetKeyReset(rootCtx, &localv1.GetKeyResetRequest{ResetId: start.GetResetId()})
	if err != nil || st.GetState() != "pending" || st.GetFingerprint() != command.KeyFingerprint(fresh.COSE) || st.GetName() != "laptop" {
		t.Fatalf("pending: %v, %v", st, err)
	}
	if _, err := s.ConfirmKeyReset(rootCtx, &localv1.ConfirmKeyResetRequest{ResetId: start.GetResetId(), Fingerprint: "WRONG"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("confirm with the wrong fingerprint: %v", err)
	}
	if _, err := s.ConfirmKeyReset(rootCtx, &localv1.ConfirmKeyResetRequest{ResetId: start.GetResetId(), Fingerprint: st.GetFingerprint()}); err != nil {
		t.Fatal(err)
	}
	keys, _ = s.ListKeys(userCtx, &localv1.ListKeysRequest{})
	if len(keys.GetKeys()) != 1 || keys.GetKeys()[0].GetFingerprint() != st.GetFingerprint() || keys.GetKeys()[0].GetAddedBy() != "" {
		t.Fatalf("keys after the reset: %v", keys)
	}

	audit, err := s.ListAudit(userCtx, &localv1.ListAuditRequest{})
	if err != nil || len(audit.GetEntries()) != 2 {
		t.Fatalf("audit: %v, %v", audit, err)
	}
	reset, pair := audit.GetEntries()[0], audit.GetEntries()[1]
	if reset.GetAction() != "keys.reset" || reset.GetUserId() != "local:root" || reset.GetKeyFingerprint() != st.GetFingerprint() {
		t.Errorf("reset entry: %v", reset)
	}
	if pair.GetAction() != command.ActionKeysPair || pair.GetOutcome() != "ok" || pair.GetCommandId() != e.CommandID || len(pair.GetCommandHash()) != 64 {
		t.Errorf("pair entry: %v", pair)
	}
	if l, _ := s.ListAudit(userCtx, &localv1.ListAuditRequest{Limit: 1}); len(l.GetEntries()) != 1 {
		t.Errorf("limit: %v", l)
	}
	if l, _ := s.ListAudit(userCtx, &localv1.ListAuditRequest{Since: nil}); len(l.GetEntries()) != 2 {
		t.Errorf("since: %v", l)
	}
}
