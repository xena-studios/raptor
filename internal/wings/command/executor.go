package command

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/store"
)

// Limits.
const (
	// MaxLifetime is how far in the future a command may expire. Signatures
	// are only useful for this long.
	MaxLifetime = 10 * time.Minute
	// keepCommands is how long executed command IDs (and results) are kept.
	keepCommands = 7 * 24 * time.Hour
)

// Errors a caller can distinguish.
var (
	ErrUnknownAction    = errors.New("unknown action")
	ErrExpired          = errors.New("command expired")
	ErrBadGrant         = errors.New("invalid Panel grant")
	ErrNotEnrolled      = errors.New("node isn't linked to a Panel (no Panel key)")
	ErrSignatureNeeded  = errors.New("this action must be signed with the user's passkey")
	ErrUntrustedKey     = errors.New("passkey isn't trusted by this node for this action")
	ErrConflict         = errors.New("command ID reused for a different command")
	ErrInProgress       = errors.New("command is already running")
	ErrLastOwnerKey     = errors.New("can't remove the last owner key")
	ErrNoTrustedKeys    = errors.New("no passkeys are trusted on this node yet; they're pinned when the node is linked, or with `raptor keys reset`")
	ErrSignatureInvalid = errBadAssertion
)

// Handler runs one action.
type Handler struct {
	// Signed reports whether this command must carry a passkey signature.
	// It may look at the command and the current state (e.g. an update that
	// changes the egg is signed; one that renames a server isn't).
	Signed func(ctx context.Context, e Envelope) (bool, error)
	Run    func(ctx context.Context, e Envelope) (result any, err error)
}

// Always and Never are Signed functions.
func Always(context.Context, Envelope) (bool, error) { return true, nil }

// Never is a Signed function for actions that don't need a passkey.
func Never(context.Context, Envelope) (bool, error) { return false, nil }

// Executor validates and runs commands.
type Executor struct {
	DB       *store.DB
	NodeID   string
	RP       RelyingParty
	PanelKey ed25519.PublicKey // nil until the node is linked
	Log      *slog.Logger
	Now      func() time.Time

	handlers map[string]Handler
}

// Result is what running (or re-running) a command returned.
type Result struct {
	Value     json.RawMessage
	Duplicate bool // the command had already run; this is the stored result
}

// Register adds an action. Key management actions are built in.
func (x *Executor) Register(action string, h Handler) {
	if x.handlers == nil {
		x.handlers = map[string]Handler{}
	}
	x.handlers[action] = h
}

func (x *Executor) now() time.Time {
	if x.Now != nil {
		return x.Now()
	}
	return time.Now()
}

func (x *Executor) log() *slog.Logger {
	if x.Log != nil {
		return x.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Start marks commands that were running when Wings stopped as failed.
func (x *Executor) Start(ctx context.Context) error {
	_, err := x.DB.Write.InterruptedCommands(ctx, sql.NullInt64{Int64: x.now().UnixMilli(), Valid: true})
	return err
}

// Prune forgets executed commands older than the retention window.
func (x *Executor) Prune(ctx context.Context) (int64, error) {
	return x.DB.Write.PruneCommands(ctx, x.now().Add(-keepCommands).UnixMilli())
}

// Execute runs a command at most once. Everything is checked before anything
// runs; a command that fails a check is rejected and logged, even though it
// came through the Panel.
func (x *Executor) Execute(ctx context.Context, e Envelope) (Result, error) {
	res, err := x.execute(ctx, e)
	if err != nil && !errors.Is(err, ErrInProgress) {
		x.log().Warn("command rejected or failed", "command", e.CommandID, "action", e.Action, "user", e.UserID, "server", e.ServerID, "err", err)
	}
	return res, err
}

func (x *Executor) execute(ctx context.Context, e Envelope) (Result, error) {
	if err := e.validate(x.NodeID); err != nil {
		return Result{}, err
	}
	h, ok := x.handlers[e.Action]
	if !ok {
		h, ok = x.keyHandlers()[e.Action]
	}
	if !ok {
		return Result{}, fmt.Errorf("%w %q", ErrUnknownAction, e.Action)
	}
	hash, err := e.Hash()
	if err != nil {
		return Result{}, err
	}

	// A retry of a command that already ran gets the stored result, even
	// after the command expired.
	if res, done, err := x.previous(ctx, e.CommandID, hash); done || err != nil {
		return res, err
	}

	now := x.now()
	exp := time.Unix(e.ExpiresAt, 0)
	if !exp.After(now) {
		return Result{}, ErrExpired
	}
	if exp.Sub(now) > MaxLifetime {
		return Result{}, fmt.Errorf("%w: expires more than %s from now", ErrExpired, MaxLifetime)
	}
	if err := x.checkGrant(e, now); err != nil {
		return Result{}, err
	}

	signed, err := h.Signed(ctx, e)
	if err != nil {
		return Result{}, err
	}
	var signer []byte
	if signed {
		if err := x.checkSignature(ctx, e, hash, now); err != nil {
			return Result{}, err
		}
		signer = e.Signature.CredentialID
	}

	// Claim the command ID; a concurrent duplicate loses the race and gets
	// the stored outcome instead.
	n, err := x.DB.Write.ClaimCommand(ctx, store.ClaimCommandParams{
		CommandID: e.CommandID, PayloadHash: hash, Action: e.Action, UserID: e.UserID, SignedBy: signer, ReceivedAt: now.UnixMilli(),
	})
	if err != nil {
		return Result{}, err
	}
	if n == 0 {
		res, _, err := x.previous(ctx, e.CommandID, hash)
		return res, err
	}

	value, runErr := safeRun(ctx, h, e)
	fin := store.FinishCommandParams{Status: "succeeded", FinishedAt: sql.NullInt64{Int64: x.now().UnixMilli(), Valid: true}, CommandID: e.CommandID}
	var raw json.RawMessage
	if runErr != nil {
		fin.Status, fin.Error = "failed", runErr.Error()
	} else if value != nil {
		if raw, err = json.Marshal(value); err != nil {
			fin.Status, fin.Error = "failed", err.Error()
			runErr = err
		}
		fin.Result = string(raw)
	}
	if err := x.DB.Write.FinishCommand(context.WithoutCancel(ctx), fin); err != nil {
		return Result{}, errors.Join(runErr, err)
	}
	if signed {
		x.log().Info("signed command executed", "command", e.CommandID, "action", e.Action, "user", e.UserID, "server", e.ServerID, "ok", runErr == nil)
	}
	return Result{Value: raw}, runErr
}

// previous returns the stored outcome of a command that was already seen.
func (x *Executor) previous(ctx context.Context, id string, hash []byte) (Result, bool, error) {
	row, err := x.DB.Write.GetCommand(ctx, id) // the writer sees the latest state
	if errors.Is(err, sql.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, true, err
	}
	if !bytes.Equal(row.PayloadHash, hash) {
		return Result{}, true, ErrConflict
	}
	switch row.Status {
	case "running":
		return Result{}, true, ErrInProgress
	case "failed":
		return Result{Duplicate: true}, true, errors.New(row.Error)
	}
	var raw json.RawMessage
	if row.Result != "" {
		raw = json.RawMessage(row.Result)
	}
	return Result{Value: raw, Duplicate: true}, true, nil
}

// checkGrant verifies the Panel's authorization of this exact command.
func (x *Executor) checkGrant(e Envelope, now time.Time) error {
	if len(x.PanelKey) != ed25519.PublicKeySize {
		return ErrNotEnrolled
	}
	g := e.Grant
	payload, err := g.Payload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(x.PanelKey, payload, g.Signature) {
		return fmt.Errorf("%w: signature doesn't match", ErrBadGrant)
	}
	if g.UserID != e.UserID || g.NodeID != e.NodeID || g.CommandID != e.CommandID || g.Action != e.Action || g.ServerID != e.ServerID {
		return fmt.Errorf("%w: issued for a different command", ErrBadGrant)
	}
	if !time.Unix(g.ExpiresAt, 0).After(now) {
		return fmt.Errorf("%w: expired", ErrBadGrant)
	}
	return nil
}

// checkSignature verifies the user's passkey signature over the command and
// that this node trusts that key for this action.
func (x *Executor) checkSignature(ctx context.Context, e Envelope, hash []byte, now time.Time) error {
	if e.Signature == nil {
		return ErrSignatureNeeded
	}
	k, err := x.DB.Write.GetTrustedKey(ctx, e.Signature.CredentialID)
	if errors.Is(err, sql.ErrNoRows) {
		if n, _ := x.DB.Write.CountOwnerKeys(ctx); n == 0 {
			return ErrNoTrustedKeys
		}
		return ErrUntrustedKey
	}
	if err != nil {
		return err
	}
	if k.UserID != e.UserID {
		return fmt.Errorf("%w: the key belongs to another user", ErrUntrustedKey)
	}
	if k.Role == "delegate" {
		var actions []string
		_ = json.Unmarshal([]byte(k.Actions), &actions)
		switch {
		case k.ExpiresAt.Valid && now.UnixMilli() >= k.ExpiresAt.Int64:
			return fmt.Errorf("%w: the delegation expired", ErrUntrustedKey)
		case !slices.Contains(actions, e.Action):
			return fmt.Errorf("%w: not delegated %s", ErrUntrustedKey, e.Action)
		case k.ServerID != "" && k.ServerID != e.ServerID:
			return fmt.Errorf("%w: delegated for another server", ErrUntrustedKey)
		case isKeyAction(e.Action):
			return fmt.Errorf("%w: only owners manage keys", ErrUntrustedKey)
		}
	}
	counter, err := verifyAssertion(k.PublicKey, *e.Signature, hash, x.RP)
	if err != nil {
		return err
	}
	// Authenticators that keep a counter must move it forward; otherwise the
	// key may have been cloned. Synced passkeys always report 0.
	if counter != 0 || k.SignCount != 0 {
		if int64(counter) <= k.SignCount {
			return fmt.Errorf("%w: signature counter went backwards (possible cloned key)", ErrSignatureInvalid)
		}
		if err := x.DB.Write.SetSignCount(ctx, store.SetSignCountParams{SignCount: int64(counter), CredentialID: k.CredentialID}); err != nil {
			return err
		}
	}
	return nil
}

// safeRun turns a handler panic into a failed command, so the command ID
// doesn't stay "running" and Wings keeps working.
func safeRun(ctx context.Context, h Handler, e Envelope) (value any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("command panicked: %v", p)
		}
	}()
	return h.Run(ctx, e)
}

// LoadPanelKey reads the Panel's pinned signing key (base64 Ed25519 public
// key), written at enrollment. A missing file means the node isn't linked
// yet: nil, and every remote command is refused.
func LoadPanelKey(path string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path from config
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%s: not a base64 Ed25519 public key", path)
	}
	return ed25519.PublicKey(k), nil
}

// RelyingPartyFor derives the passkey origin and RP ID from the web app's
// URL (panel.app_url; never hardcoded: docs/WINGS.md#config-file). The RP ID
// is the app's own hostname, not the registrable domain, so no other
// subdomain can obtain signatures (docs/DECISIONS.md #82).
func RelyingPartyFor(appURL string) (RelyingParty, error) {
	u, err := url.Parse(appURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
		return RelyingParty{}, fmt.Errorf("panel.app_url %q must be an https origin like https://app.raptorpanel.net", appURL)
	}
	return RelyingParty{Origin: "https://" + u.Host, ID: u.Hostname()}, nil
}
