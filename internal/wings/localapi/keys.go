package localapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/wings/command"
)

// SetCommands makes trusted keys, the audit log, and key resets available.
func (s *Service) SetCommands(x *command.Executor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = x
}

func (s *Service) executor() (*command.Executor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.commands == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("wings is still starting"))
	}
	return s.commands, nil
}

func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// ListKeys lists trusted keys. The raptor group may look.
func (s *Service) ListKeys(ctx context.Context, _ *localv1.ListKeysRequest) (*localv1.ListKeysResponse, error) {
	x, err := s.executor()
	if err != nil {
		return nil, err
	}
	keys, err := command.ListKeys(ctx, x.DB)
	if err != nil {
		return nil, err
	}
	resp := &localv1.ListKeysResponse{}
	for _, k := range keys {
		var actions []string
		_ = json.Unmarshal([]byte(k.Actions), &actions)
		resp.Keys = append(resp.Keys, &localv1.TrustedKey{
			Fingerprint: k.Fingerprint, UserId: k.UserID, Name: k.Name, Role: k.Role, ServerId: k.ServerID,
			Actions: actions, ExpiresAt: ts(k.ExpiresAt), AddedBy: k.AddedBy, AddedAt: ts(k.AddedAt),
		})
	}
	return resp, nil
}

// ListAudit lists the audit log. The raptor group may look.
func (s *Service) ListAudit(ctx context.Context, req *localv1.ListAuditRequest) (*localv1.ListAuditResponse, error) {
	x, err := s.executor()
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 10000)
	var since time.Time
	if req.GetSince() != nil {
		since = req.GetSince().AsTime()
	}
	list, err := command.ListAudit(ctx, x.DB, since, limit)
	if err != nil {
		return nil, err
	}
	resp := &localv1.ListAuditResponse{}
	for _, a := range list {
		resp.Entries = append(resp.Entries, &localv1.AuditEntry{
			Id: a.ID, At: ts(a.At), Action: a.Action, ServerId: a.ServerID, UserId: a.UserID,
			KeyName: a.KeyName, KeyFingerprint: a.Fingerprint, Outcome: a.Outcome, Detail: a.Detail,
			CommandId: a.CommandID, CommandHash: hex.EncodeToString(a.CommandHash),
		})
	}
	return resp, nil
}

func (s *Service) pairing(ctx context.Context) (*command.Executor, error) {
	if err := requireRoot(ctx, "reset the node's passkeys"); err != nil {
		return nil, err
	}
	x, err := s.executor()
	if err != nil {
		return nil, err
	}
	if x.Pairing == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("key resets aren't available"))
	}
	return x, nil
}

func pairingErr(err error) error {
	switch {
	case errors.Is(err, command.ErrNoPairing):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, command.ErrPairingState):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return err
}

// StartKeyReset begins a key reset. Root only.
func (s *Service) StartKeyReset(ctx context.Context, _ *localv1.StartKeyResetRequest) (*localv1.StartKeyResetResponse, error) {
	x, err := s.pairing(ctx)
	if err != nil {
		return nil, err
	}
	id, code, exp, err := x.Pairing.Start(Caller(ctx), time.Now())
	if err != nil {
		return nil, err
	}
	return &localv1.StartKeyResetResponse{ResetId: id, Code: code, ExpiresAt: ts(exp)}, nil
}

// GetKeyReset reports a key reset's state. Root only.
func (s *Service) GetKeyReset(ctx context.Context, req *localv1.GetKeyResetRequest) (*localv1.GetKeyResetResponse, error) {
	x, err := s.pairing(ctx)
	if err != nil {
		return nil, err
	}
	st, err := x.Pairing.Status(req.GetResetId(), time.Now())
	if err != nil {
		return nil, pairingErr(err)
	}
	return &localv1.GetKeyResetResponse{
		State: string(st.State), ExpiresAt: ts(st.Expires), Fingerprint: st.Fingerprint, UserId: st.UserID, Name: st.Name,
	}, nil
}

// ConfirmKeyReset pins the key that paired. Root only.
func (s *Service) ConfirmKeyReset(ctx context.Context, req *localv1.ConfirmKeyResetRequest) (*localv1.ConfirmKeyResetResponse, error) {
	x, err := s.pairing(ctx)
	if err != nil {
		return nil, err
	}
	if err := x.ConfirmPairing(ctx, req.GetResetId(), req.GetFingerprint()); err != nil {
		return nil, pairingErr(err)
	}
	return &localv1.ConfirmKeyResetResponse{}, nil
}

// CancelKeyReset ends a key reset. Root only.
func (s *Service) CancelKeyReset(ctx context.Context, req *localv1.CancelKeyResetRequest) (*localv1.CancelKeyResetResponse, error) {
	x, err := s.pairing(ctx)
	if err != nil {
		return nil, err
	}
	x.Pairing.Cancel(req.GetResetId())
	return &localv1.CancelKeyResetResponse{}, nil
}

// PinOwnerKey implements LocalService. Root only.
func (s *Service) PinOwnerKey(ctx context.Context, req *localv1.PinOwnerKeyRequest) (*localv1.PinOwnerKeyResponse, error) {
	if err := requireRoot(ctx, "pin the owner's passkey"); err != nil {
		return nil, err
	}
	x, err := s.executor()
	if err != nil {
		return nil, err
	}
	var pin nodecmd.OwnerPin
	if err := json.Unmarshal(req.GetPin(), &pin); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("owner key: %w", err))
	}
	fp, err := x.PinOwner(ctx, pin, req.GetJoinToken())
	if errors.Is(err, command.ErrAlreadyOwned) {
		return nil, connect.NewError(connect.CodeAlreadyExists, err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	return &localv1.PinOwnerKeyResponse{Fingerprint: fp, Name: pin.Name}, nil
}
