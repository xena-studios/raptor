// Package nodelink is the node connection's wire protocol, shared by Wings
// and the Panel (docs/ARCHITECTURE.md#node-connection): a WebSocket that
// Wings opens to the Panel, a handshake in which each side signs the other's
// fresh nonce, then yamux, with Connect RPCs on its streams in both
// directions.
package nodelink

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gowebpki/jcs"
)

// Version is the protocol version. Each side states its own in the
// handshake; the Panel refuses versions it no longer supports.
const Version = 1

// Path is where the Panel accepts node connections.
const Path = "/nodes/connect"

// routePattern is a Panel instance's route through the proxy (/i/panel-a).
var routePattern = regexp.MustCompile(`^/i/[a-z0-9-]{1,32}$`)

// ValidRoute reports whether r is a Panel instance's route.
func ValidRoute(r string) bool { return routePattern.MatchString(r) }

// WithRoute is the Panel URL that reaches one instance: route goes before
// the connection's path.
func WithRoute(panelURL, route string) string {
	if route == "" {
		return panelURL
	}
	return strings.TrimSuffix(panelURL, "/") + route
}

// MaxSkew is how far the two clocks may disagree. The nonces make every
// signature fresh on their own; the timestamps catch a badly set clock early,
// since it breaks grants and schedules too.
const MaxSkew = 5 * time.Minute

// Purposes. A transfer connection carries one file transfer.
const (
	PurposeControl  = "control"
	purposeTransfer = "transfer:"
)

// TransferPurpose is the purpose of the connection for one transfer.
func TransferPurpose(id string) string { return purposeTransfer + id }

// TransferID returns the transfer a purpose is for, if it's a transfer.
func TransferID(purpose string) (string, bool) {
	return strings.CutPrefix(purpose, purposeTransfer)
}

const nonceSize = 32

// The handshake, as WebSocket text messages before yamux starts:
//
//	Wings → Panel  Hello      node ID, version, purpose, nonce N
//	Panel → Wings  Challenge  version, nonce P, time, Panel's signature (over N, P, …)
//	Wings → Panel  Proof      time, node's signature (over N, P, …)
//	Panel → Wings  Welcome    (or an error)
//
// Each side signs the other's nonce, so neither signature can be replayed on
// another connection, and both cover the node ID and the purpose. A Panel
// that turns the node away sends a Welcome with the reason in place of the
// Challenge or the final Welcome.

// Hello opens the handshake.
type Hello struct {
	NodeID       string   `json:"node_id"`
	Version      int      `json:"version"`
	Purpose      string   `json:"purpose"`
	Nonce        []byte   `json:"nonce"`
	Capabilities []string `json:"capabilities,omitempty"`
	// Software is the Wings version, for the Panel's records.
	Software string `json:"software,omitempty"`
}

// Challenge is the Panel's answer: its signature proves it's the Panel this
// node was linked to, and its nonce is what the node signs.
type Challenge struct {
	Version      int      `json:"version"`
	Nonce        []byte   `json:"nonce"`
	Time         int64    `json:"time"` // unix seconds
	Signature    []byte   `json:"signature"`
	Capabilities []string `json:"capabilities,omitempty"`
	// Error and Retry: the Panel refused the node before challenging it
	// (as in Welcome).
	Error string `json:"error,omitempty"`
	Retry int    `json:"retry,omitempty"`
}

// Proof is the node's signature.
type Proof struct {
	Time      int64  `json:"time"`
	Signature []byte `json:"signature"`
}

// Welcome ends the handshake. If Error is set, the Panel closes the
// connection.
type Welcome struct {
	Error string `json:"error,omitempty"`
	// Retry is how long to wait before reconnecting after an error, in
	// seconds (0: the usual backoff).
	Retry int `json:"retry,omitempty"`
}

// Signers.
const (
	signerPanel = "panel"
	signerNode  = "node"
)

// signed is what each side signs: the same fields, with who's signing, so a
// signature from one side never verifies as the other's.
type signed struct {
	Context    string `json:"context"`
	Signer     string `json:"signer"`
	NodeID     string `json:"node_id"`
	Purpose    string `json:"purpose"`
	NodeNonce  []byte `json:"node_nonce"`
	PanelNonce []byte `json:"panel_nonce"`
	Time       int64  `json:"time"`
}

func payload(signer string, h Hello, panelNonce []byte, t int64) ([]byte, error) {
	raw, err := json.Marshal(signed{
		Context: "raptor node connection v1", Signer: signer, NodeID: h.NodeID, Purpose: h.Purpose,
		NodeNonce: h.Nonce, PanelNonce: panelNonce, Time: t,
	})
	if err != nil {
		return nil, err
	}
	return jcs.Transform(raw)
}

// Handshake errors.
var (
	ErrBadSignature = errors.New("handshake signature doesn't verify")
	ErrClockSkew    = errors.New("clocks disagree")
)

// NewHello starts a handshake for a node.
func NewHello(nodeID, purpose, software string) (Hello, error) {
	n := make([]byte, nonceSize)
	if _, err := rand.Read(n); err != nil {
		return Hello{}, err
	}
	return Hello{NodeID: nodeID, Version: Version, Purpose: purpose, Nonce: n, Software: software}, nil
}

// Validate checks a Hello's shape (the Panel's side).
func (h Hello) Validate() error {
	switch {
	case h.NodeID == "" || len(h.NodeID) > 64:
		return errors.New("bad node ID")
	case len(h.Nonce) != nonceSize:
		return errors.New("bad nonce")
	case h.Purpose != PurposeControl && !validTransfer(h.Purpose):
		return fmt.Errorf("unknown purpose %q", h.Purpose)
	}
	return nil
}

func validTransfer(p string) bool {
	id, ok := TransferID(p)
	return ok && id != "" && len(id) <= 64
}

// NewChallenge answers a Hello, signed with the Panel's key.
func NewChallenge(h Hello, panelKey ed25519.PrivateKey, now time.Time) (Challenge, error) {
	n := make([]byte, nonceSize)
	if _, err := rand.Read(n); err != nil {
		return Challenge{}, err
	}
	c := Challenge{Version: Version, Nonce: n, Time: now.Unix()}
	p, err := payload(signerPanel, h, n, c.Time)
	if err != nil {
		return Challenge{}, err
	}
	c.Signature = ed25519.Sign(panelKey, p)
	return c, nil
}

// VerifyChallenge checks the Panel's signature against the key pinned on the
// node (the node's side).
func VerifyChallenge(h Hello, c Challenge, panelKey ed25519.PublicKey, now time.Time) error {
	if len(c.Nonce) != nonceSize {
		return errors.New("bad Panel nonce")
	}
	p, err := payload(signerPanel, h, c.Nonce, c.Time)
	if err != nil {
		return err
	}
	if len(panelKey) != ed25519.PublicKeySize || !ed25519.Verify(panelKey, p, c.Signature) {
		return fmt.Errorf("%w: this isn't the Panel the node was linked to", ErrBadSignature)
	}
	return checkSkew(c.Time, now)
}

// NewProof signs the Panel's challenge with the node's key.
func NewProof(h Hello, c Challenge, nodeKey ed25519.PrivateKey, now time.Time) (Proof, error) {
	pr := Proof{Time: now.Unix()}
	p, err := payload(signerNode, h, c.Nonce, pr.Time)
	if err != nil {
		return Proof{}, err
	}
	pr.Signature = ed25519.Sign(nodeKey, p)
	return pr, nil
}

// VerifyProof checks the node's signature against its enrolled key (the
// Panel's side).
func VerifyProof(h Hello, c Challenge, pr Proof, nodeKey ed25519.PublicKey, now time.Time) error {
	p, err := payload(signerNode, h, c.Nonce, pr.Time)
	if err != nil {
		return err
	}
	if len(nodeKey) != ed25519.PublicKeySize || !ed25519.Verify(nodeKey, p, pr.Signature) {
		return ErrBadSignature
	}
	return checkSkew(pr.Time, now)
}

func checkSkew(t int64, now time.Time) error {
	d := now.Sub(time.Unix(t, 0))
	if d > MaxSkew || d < -MaxSkew {
		return fmt.Errorf("%w by %s (more than %s): check the node's time sync", ErrClockSkew, d.Round(time.Second), MaxSkew)
	}
	return nil
}
