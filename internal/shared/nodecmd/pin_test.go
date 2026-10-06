package nodecmd

import (
	"encoding/hex"
	"testing"
)

// The same vector is in web/src/lib/canonical.test.ts. (Not a secret: a
// hash of the test token "rpt_join_test".)
const pinVector = `{"credential_id":"AQID","join_token_hash":"5RLhqpfOiOoOUEV5qGPFsGQt3QQrqEBf1MO+ceGZul8=","name":"MacBook \"1P\"","public_key":"BAUG","purpose":"raptor.owner_pin.v1","user_id":"01a112a3-ad95-7cb7-a446-b83928bfafa6"}` // gitleaks:allow

func TestOwnerPinVector(t *testing.T) {
	p := OwnerPin{
		JoinTokenHash: JoinTokenHash("rpt_join_test"), CredentialID: []byte{1, 2, 3}, PublicKey: []byte{4, 5, 6},
		UserID: "01a112a3-ad95-7cb7-a446-b83928bfafa6", Name: `MacBook "1P"`,
	}
	c, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != pinVector {
		t.Fatalf("canonical pin changed:\n got %s\nwant %s", c, pinVector)
	}
	h, _ := p.Hash()
	t.Logf("hash %s", hex.EncodeToString(h))

	p.Signature = &PasskeySignature{CredentialID: []byte{1, 2, 3}}
	if err := p.Check("rpt_join_test"); err != nil {
		t.Error(err)
	}
	if err := p.Check("rpt_join_other"); err == nil {
		t.Error("a pin for another token passed")
	}
	p.Signature.CredentialID = []byte{9}
	if err := p.Check("rpt_join_test"); err == nil {
		t.Error("a pin signed by another key passed")
	}
}
