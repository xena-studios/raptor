package command

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// ActionKeysPair completes `raptor keys reset`: the owner enters the pairing
// code shown on the box in the Panel and signs the command with a new
// passkey. docs/SECURITY-MODEL.md#passkey-signed-commands
const ActionKeysPair = "keys.pair"

// Pairing limits.
const (
	PairingTTL      = 15 * time.Minute
	maxPairingTries = 5
	// pairingAlphabet has no look-alike characters (0/O, 1/I/L).
	pairingAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	pairingLen      = 10
)

// Pairing errors.
var (
	ErrNoPairing    = errors.New("no key reset is in progress on the node (run raptor keys reset there)")
	ErrPairingCode  = errors.New("wrong pairing code")
	ErrPairingState = errors.New("the key reset isn't waiting for this")
)

// PairParams are the params of keys.pair: the code shown on the box and the
// new owner key.
type PairParams struct {
	Code         string `json:"code"`
	CredentialID []byte `json:"credential_id"`
	PublicKey    []byte `json:"public_key"` // COSE_Key
	UserID       string `json:"user_id"`
	Name         string `json:"name,omitempty"`
}

// PairingState is where a key reset is.
type PairingState string

// Pairing states.
const (
	PairingWaiting PairingState = "waiting" // for keys.pair from the Panel
	PairingPending PairingState = "pending" // a key paired; root must confirm its fingerprint
	PairingDone    PairingState = "done"
	PairingExpired PairingState = "expired" // or cancelled, or too many wrong codes
)

// PairingStatus is a key reset's state, for the CLI waiting on the box.
type PairingStatus struct {
	State       PairingState
	Expires     time.Time
	Fingerprint string // pending: the key waiting for confirmation
	UserID      string
	Name        string
}

// Pairing is the key reset in progress on the node: at most one. Root on
// the box starts it and gets a one-time code; the owner completes it from
// the Panel with a new passkey; root confirms that key's fingerprint on the
// box before it's pinned. The code alone isn't enough: a compromised Panel
// sees it when the owner types it in and could pair its own key, and the
// fingerprint check on the box catches that.
type Pairing struct {
	mu      sync.Mutex
	session *pairSession
}

type pairSession struct {
	id      string
	code    [32]byte // SHA-256 of the normalized code
	expires time.Time
	tries   int
	state   PairingState
	key     *KeyParams // pending
	actor   string     // who started it on the box
}

func normalizeCode(c string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToUpper(c))
}

// Start begins a key reset (replacing any other) and returns its ID and the
// code to show on the box, formatted "XXXXX-XXXXX".
func (p *Pairing) Start(actor string, now time.Time) (id, code string, expires time.Time, err error) {
	b := make([]byte, pairingLen)
	for i := range b {
		n, err := randIndex(len(pairingAlphabet))
		if err != nil {
			return "", "", time.Time{}, err
		}
		b[i] = pairingAlphabet[n]
	}
	s := &pairSession{
		id: uuid.NewString(), code: sha256.Sum256(b), expires: now.Add(PairingTTL),
		state: PairingWaiting, actor: actor,
	}
	p.mu.Lock()
	p.session = s
	p.mu.Unlock()
	return s.id, string(b[:pairingLen/2]) + "-" + string(b[pairingLen/2:]), s.expires, nil
}

// randIndex returns a uniform random number in [0, n) (n ≤ 256).
func randIndex(n int) (int, error) {
	limit := 256 - 256%n
	var b [1]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if int(b[0]) < limit {
			return int(b[0]) % n, nil
		}
	}
}

// get returns the session with this ID, marking it expired if it is.
func (p *Pairing) get(id string, now time.Time) (*pairSession, error) {
	s := p.session
	if s == nil || (id != "" && s.id != id) {
		return nil, ErrNoPairing
	}
	if s.state != PairingDone && now.After(s.expires) {
		s.state = PairingExpired
	}
	return s, nil
}

// Status returns a key reset's state.
func (p *Pairing) Status(id string, now time.Time) (PairingStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.get(id, now)
	if err != nil {
		return PairingStatus{}, err
	}
	st := PairingStatus{State: s.state, Expires: s.expires}
	if s.key != nil {
		st.Fingerprint, st.UserID, st.Name = KeyFingerprint(s.key.PublicKey), s.key.UserID, s.key.Name
	}
	return st, nil
}

// Cancel ends a key reset without changing any key.
func (p *Pairing) Cancel(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil && p.session.id == id && p.session.state != PairingDone {
		p.session.state = PairingExpired
	}
}

// Confirm pins the pending key as the node's only owner key, removing every
// other trusted key and delegation. fingerprint must be the one root was
// shown, so a key swapped in meanwhile isn't pinned by mistake.
func (x *Executor) ConfirmPairing(ctx context.Context, id, fingerprint string) error {
	p := x.Pairing
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.get(id, x.now())
	if err != nil {
		return err
	}
	if s.state != PairingPending || s.key == nil {
		return fmt.Errorf("%w (%s)", ErrPairingState, s.state)
	}
	if KeyFingerprint(s.key.PublicKey) != fingerprint {
		return fmt.Errorf("%w: the key waiting has a different fingerprint", ErrPairingState)
	}
	k := *s.key
	k.Role = "owner"
	var removed int64
	err = x.DB.WriteTx(ctx, func(q *store.Queries) error {
		if removed, err = q.DeleteAllTrustedKeys(ctx); err != nil {
			return err
		}
		return addKey(ctx, q, k, nil, x.now())
	})
	if err != nil {
		return err
	}
	s.state = PairingDone
	x.localAudit(ctx, "keys.reset", s.actor, k.CredentialID, k.Name, AuditOK,
		fmt.Sprintf("pinned %s for %s; removed %d key(s) and delegations", KeyFingerprint(k.PublicKey), k.UserID, removed))
	return nil
}

// localAudit records an action taken on the box (not a Panel command).
func (x *Executor) localAudit(ctx context.Context, action, actor string, cred []byte, keyName, outcome, detail string) {
	id, err := x.DB.Write.InsertAudit(context.WithoutCancel(ctx), store.InsertAuditParams{
		At: x.now().UnixMilli(), Action: action, UserID: actor, CredentialID: cred, KeyName: keyName, Outcome: outcome, Detail: detail,
	})
	if err != nil {
		x.log().Error("recording in the audit log failed", "action", action, "err", err)
		return
	}
	x.notify(ctx, id)
}

// pair handles keys.pair. The new key isn't trusted yet, so instead of the
// usual signature check the command must be signed by the key it pairs
// (proving the owner holds it) and carry the code shown on the box.
func (x *Executor) pair(ctx context.Context, e Envelope) (any, error) {
	var p PairParams
	if err := json.Unmarshal(e.Params, &p); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	hash, err := e.Hash()
	if err != nil {
		return nil, err
	}
	reject := func(err error) (any, error) {
		x.audit(ctx, e, hash, AuditRejected, err.Error())
		return nil, err
	}
	if x.Pairing == nil {
		return reject(ErrNoPairing)
	}
	x.Pairing.mu.Lock()
	defer x.Pairing.mu.Unlock()
	s, err := x.Pairing.get("", x.now())
	if err != nil {
		return reject(err)
	}
	if s.state != PairingWaiting {
		return reject(fmt.Errorf("%w (%s)", ErrPairingState, s.state))
	}
	sum := sha256.Sum256([]byte(normalizeCode(p.Code)))
	if subtle.ConstantTimeCompare(sum[:], s.code[:]) != 1 {
		s.tries++
		if s.tries >= maxPairingTries {
			s.state = PairingExpired
			return reject(fmt.Errorf("%w; too many tries, so the reset was cancelled (run raptor keys reset again)", ErrPairingCode))
		}
		return reject(ErrPairingCode)
	}
	k := KeyParams{CredentialID: p.CredentialID, UserID: p.UserID, PublicKey: p.PublicKey, Role: "owner", Name: p.Name}
	if err := validateKey(k); err != nil {
		return reject(err)
	}
	if e.Signature == nil || string(e.Signature.CredentialID) != string(p.CredentialID) {
		return reject(fmt.Errorf("%w: the pairing must be signed by the key being paired", ErrSignatureNeeded))
	}
	if p.UserID != e.UserID {
		return reject(errors.New("the key must belong to the user pairing it"))
	}
	if _, err := verifyAssertion(p.PublicKey, *e.Signature, hash, x.RP); err != nil {
		return reject(err)
	}
	s.state, s.key = PairingPending, &k
	x.audit(ctx, e, hash, AuditOK, "waiting for root to confirm fingerprint "+KeyFingerprint(p.PublicKey)+" on the node")
	return map[string]string{"status": string(PairingPending), "fingerprint": KeyFingerprint(p.PublicKey)}, nil
}
