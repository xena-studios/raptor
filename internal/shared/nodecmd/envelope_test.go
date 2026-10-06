package nodecmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// The browser builds the same canonical form to sign (web/src/lib/canonical.ts),
// and web/src/lib/canonical.test.ts checks this same vector: if either side
// changes, signatures stop verifying, so both tests fail first.
const (
	vectorCanonical = `{"action":"server.delete","command_id":"01a1125e-1097-7b27-a780-c4fc2b1e415f","expires_at":1791313200,"node_id":"01a10f2a-865c-74ec-9578-3fe2ea243894","params":{"final_backup":true,"note":"é \"x\" <y>","z":[1,2.5,"a"]},"server_id":"srv","user_id":"01a1128b-8f10-7828-94ea-ff8c157dca3d"}`
)

func TestCanonicalVector(t *testing.T) {
	e := Envelope{
		CommandID: "01a1125e-1097-7b27-a780-c4fc2b1e415f", NodeID: "01a10f2a-865c-74ec-9578-3fe2ea243894",
		UserID: "01a1128b-8f10-7828-94ea-ff8c157dca3d", Action: "server.delete", ServerID: "srv", ExpiresAt: 1791313200,
		Params: json.RawMessage(`{"z": [1, 2.50, "a"], "note": "é \"x\" <y>", "final_backup": true}`),
	}
	c, err := e.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if string(c) != vectorCanonical {
		t.Fatalf("canonical form changed:\n got %s\nwant %s", c, vectorCanonical)
	}
	sum := sha256.Sum256(c)
	t.Logf("hash %s", hex.EncodeToString(sum[:]))
	// No params is {}.
	e.Params = nil
	if c, _ := e.Canonical(); string(c) != `{"action":"server.delete","command_id":"01a1125e-1097-7b27-a780-c4fc2b1e415f","expires_at":1791313200,"node_id":"01a10f2a-865c-74ec-9578-3fe2ea243894","params":{},"server_id":"srv","user_id":"01a1128b-8f10-7828-94ea-ff8c157dca3d"}` {
		t.Errorf("no params: %s", c)
	}
}
