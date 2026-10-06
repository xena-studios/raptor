package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// ErrAlreadyOwned is PinOwner's answer when the node already trusts an
// owner key: a pin only ever sets the first one, so a Panel can't use one
// to add keys later (that's keys.add, signed by an owner).
var ErrAlreadyOwned = errors.New("this node already trusts an owner passkey; add others from the Panel, or reset with raptor keys reset")

// PinOwner trusts the owner passkey the Panel handed over at enrollment,
// after checking its signature (with this node's relying party) and that
// it names the join token this node used. It returns the key's
// fingerprint, for root to compare with the one the browser shows.
func (x *Executor) PinOwner(ctx context.Context, pin nodecmd.OwnerPin, token string) (string, error) {
	if err := pin.Check(token); err != nil {
		return "", err
	}
	k := KeyParams{CredentialID: pin.CredentialID, UserID: pin.UserID, PublicKey: pin.PublicKey, Role: "owner", Name: pin.Name}
	if err := validateKey(k); err != nil {
		return "", err
	}
	hash, err := pin.Hash()
	if err != nil {
		return "", err
	}
	if _, err := verifyAssertion(pin.PublicKey, *pin.Signature, hash, x.RP); err != nil {
		return "", fmt.Errorf("the owner key's signature doesn't verify: %w", err)
	}
	fp := KeyFingerprint(pin.PublicKey)
	err = x.DB.WriteTx(ctx, func(q *store.Queries) error {
		n, err := q.CountOwnerKeys(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrAlreadyOwned
		}
		return addKey(ctx, q, k, nil, x.now())
	})
	if err != nil {
		return "", err
	}
	x.localAudit(ctx, "keys.pin", "enrollment", k.CredentialID, k.Name, AuditOK, fmt.Sprintf("pinned %s for %s at enrollment", fp, k.UserID))
	return fp, nil
}
