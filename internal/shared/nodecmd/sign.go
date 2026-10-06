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
	return e, SignGrant(panelKey, &e)
}

// SignGrant adds the Panel's grant to a command whose fields are already
// set: by New, or by the browser, which picks the ID and expiry itself so
// its passkey can sign the command before sending it.
func SignGrant(panelKey ed25519.PrivateKey, e *Envelope) error {
	e.Grant = Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, ExpiresAt: e.ExpiresAt}
	p, err := e.Grant.Payload()
	if err != nil {
		return err
	}
	e.Grant.Signature = ed25519.Sign(panelKey, p)
	return nil
}
