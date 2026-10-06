package nodecmd

import (
	"crypto/ed25519"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// MaxLifetime is how far in the future a command may expire; Wings refuses
// later expiries.
const MaxLifetime = 10 * time.Minute

// New builds a command with a fresh UUIDv7 ID, expiring after ttl (at most
// MaxLifetime), and signs its grant with the Panel's key. Dangerous actions
// also need the user's passkey signature, which only the browser can add.
func New(panelKey ed25519.PrivateKey, nodeID, userID, action, serverID string, params any, ttl time.Duration) (Envelope, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Envelope{}, err
	}
	var raw json.RawMessage
	if params != nil {
		if raw, err = json.Marshal(params); err != nil {
			return Envelope{}, err
		}
	}
	e := Envelope{
		CommandID: id.String(), NodeID: nodeID, UserID: userID, Action: action, ServerID: serverID,
		Params: raw, ExpiresAt: time.Now().Add(min(ttl, MaxLifetime)).Unix(),
	}
	e.Grant = Grant{UserID: userID, NodeID: nodeID, CommandID: e.CommandID, Action: action, ServerID: serverID, ExpiresAt: e.ExpiresAt}
	p, err := e.Grant.Payload()
	if err != nil {
		return Envelope{}, err
	}
	e.Grant.Signature = ed25519.Sign(panelKey, p)
	return e, nil
}
