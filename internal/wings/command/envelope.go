// Package command receives commands from the Panel and decides whether to run
// them (docs/SECURITY-MODEL.md#passkey-signed-commands):
//
//   - Every command carries the Panel's grant: an Ed25519 signature by the
//     Panel's key (pinned at enrollment) saying user U may do this exact
//     command. It's bound to the command ID, so it can't be reused.
//   - Dangerous commands also carry the user's passkey signature (a WebAuthn
//     assertion) over the exact command, from a key this node trusts. Wings
//     verifies it itself, so the Panel can't forge these commands.
//   - Every command ID runs at most once; a retry returns the stored result.
package command

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/gowebpki/jcs"
)

// Envelope is a command as the Panel sends it.
type Envelope struct {
	CommandID string          `json:"command_id"` // UUIDv7
	NodeID    string          `json:"node_id"`
	UserID    string          `json:"user_id"`
	Action    string          `json:"action"` // e.g. "server.delete"
	ServerID  string          `json:"server_id,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	ExpiresAt int64           `json:"expires_at"` // unix seconds

	Grant     Grant             `json:"grant"`
	Signature *PasskeySignature `json:"signature,omitempty"`
}

// Grant is the Panel's authorization of one command for one user.
type Grant struct {
	UserID    string `json:"user_id"`
	NodeID    string `json:"node_id"`
	CommandID string `json:"command_id"`
	Action    string `json:"action"`
	ServerID  string `json:"server_id,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
	// Signature is Ed25519 by the Panel over the canonical JSON of the
	// fields above.
	Signature []byte `json:"signature"`
}

// PasskeySignature is a WebAuthn assertion over the command.
type PasskeySignature struct {
	CredentialID      []byte `json:"credential_id"`
	AuthenticatorData []byte `json:"authenticator_data"`
	ClientDataJSON    []byte `json:"client_data_json"`
	Signature         []byte `json:"signature"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// signedFields is exactly what a passkey signs and what identifies a command
// for idempotency. Everything that changes the command's meaning is in it.
type signedFields struct {
	Action    string          `json:"action"`
	CommandID string          `json:"command_id"`
	ExpiresAt int64           `json:"expires_at"`
	NodeID    string          `json:"node_id"`
	Params    json.RawMessage `json:"params"`
	ServerID  string          `json:"server_id"`
	UserID    string          `json:"user_id"`
}

// Canonical returns the command's canonical form: its signed fields as JSON
// under the JSON Canonicalization Scheme (RFC 8785), so the browser and Wings
// produce the same bytes. (Params must not rely on integers above 2^53,
// which JSON numbers can't represent exactly in browsers.)
func (e Envelope) Canonical() ([]byte, error) {
	params := e.Params
	if len(params) == 0 {
		params = json.RawMessage("{}")
	}
	raw, err := json.Marshal(signedFields{
		Action: e.Action, CommandID: e.CommandID, ExpiresAt: e.ExpiresAt, NodeID: e.NodeID,
		Params: params, ServerID: e.ServerID, UserID: e.UserID,
	})
	if err != nil {
		return nil, fmt.Errorf("command params: %w", err)
	}
	return jcs.Transform(raw)
}

// Hash is SHA-256 of the canonical form. It's the passkey challenge and the
// idempotency fingerprint.
func (e Envelope) Hash() ([]byte, error) {
	c, err := e.Canonical()
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(c)
	return h[:], nil
}

// Payload is what the Panel signs for a grant (canonical JSON of every field
// but the signature). The Panel uses this same function to sign.
func (g Grant) Payload() ([]byte, error) {
	raw, err := json.Marshal(struct {
		UserID    string `json:"user_id"`
		NodeID    string `json:"node_id"`
		CommandID string `json:"command_id"`
		Action    string `json:"action"`
		ServerID  string `json:"server_id"`
		ExpiresAt int64  `json:"expires_at"`
	}{g.UserID, g.NodeID, g.CommandID, g.Action, g.ServerID, g.ExpiresAt})
	if err != nil {
		return nil, err
	}
	return jcs.Transform(raw)
}

func (e Envelope) validate(nodeID string) error {
	switch {
	case !uuidPattern.MatchString(e.CommandID):
		return errors.New("command_id must be a lowercase UUID")
	case e.NodeID != nodeID:
		return errors.New("command is for a different node")
	case e.UserID == "":
		return errors.New("command has no user")
	case e.Action == "":
		return errors.New("command has no action")
	case len(e.Params) > 0 && !json.Valid(e.Params):
		return errors.New("params aren't valid JSON")
	}
	return nil
}
