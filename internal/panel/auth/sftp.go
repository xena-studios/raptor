package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"math/big"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// SFTP permissions: what a login can carry.
var sftpPerms = []string{"sftp", "files.read", "files.write"}

var errSFTPDenied = connect.NewError(connect.CodePermissionDenied, errors.New("sftp login denied"))

// SFTPLogin answers a node asking whether a temporary password may log in
// to one of its servers (nodev1.PanelService). Org admins and owners may;
// members need the sftp permission on that server, and carry their file
// permissions. The answer never says which part failed.
func (s *Service) SFTPLogin(ctx context.Context, nodeID string, req *nodev1.SFTPLoginRequest) (*nodev1.SFTPLoginResponse, error) {
	node, err := uuid.Parse(nodeID)
	if err != nil || req.GetPassword() == "" {
		return nil, errSFTPDenied
	}
	return s.sftpPasswordLogin(ctx, s.q(), pgtype.UUID{Bytes: node, Valid: true}, req)
}

// sftpPasswordLogin checks a temporary password: for this node and server,
// not run out, and its user still allowed in.
func (s *Service) sftpPasswordLogin(ctx context.Context, q *store.Queries, node pgtype.UUID, req *nodev1.SFTPLoginRequest) (*nodev1.SFTPLoginResponse, error) {
	row, err := q.SFTPPasswordByUsername(ctx, strings.ToLower(req.GetUsername()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSFTPDenied
	}
	if err != nil {
		return nil, err
	}
	match := subtle.ConstantTimeCompare(hash(req.GetPassword()), row.SecretHash) == 1
	if !match || row.NodeID != node || row.ServerID != req.GetServerId() || !row.ExpiresAt.Time.After(s.now()) {
		return nil, errSFTPDenied
	}
	perms, err := sftpGrant(ctx, q, node, req.GetServerId(), row.UserID)
	if err != nil {
		return nil, err
	}
	return &nodev1.SFTPLoginResponse{
		UserId: uuid.UUID(row.UserID.Bytes).String(), Permissions: perms, ExpiresAt: timestamppb.New(row.ExpiresAt.Time),
	}, nil
}

// sftpGrant is what a user may do over SFTP on a server, as of now.
func sftpGrant(ctx context.Context, q *store.Queries, node pgtype.UUID, server string, user pgtype.UUID) ([]string, error) {
	org, err := q.NodeOrg(ctx, node)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSFTPDenied
	}
	if err != nil {
		return nil, err
	}
	m, err := q.OrgMember(ctx, store.OrgMemberParams{OrgID: org, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSFTPDenied
	}
	if err != nil {
		return nil, err
	}
	perms := slices.Clone(sftpPerms)
	if m.Role == "member" {
		g, err := q.ServerGrant(ctx, store.ServerGrantParams{NodeID: node, ServerID: server, UserID: user})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !slices.Contains(g.Permissions, "sftp")) {
			return nil, errSFTPDenied
		}
		if err != nil {
			return nil, err
		}
		perms = slices.DeleteFunc(perms, func(p string) bool { return !slices.Contains(g.Permissions, p) })
	}
	return perms, nil
}

// SFTP password lifetimes.
const (
	SFTPPasswordDefault = 24 * time.Hour
	SFTPPasswordMin     = time.Hour
	SFTPPasswordMax     = 30 * 24 * time.Hour
)

// NewSFTPPassword makes a temporary SFTP login: its username, its password,
// and the password's hash to store.
func NewSFTPPassword() (username, password string, secretHash []byte) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	pick := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			v, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
			b[i] = alphabet[v.Int64()]
		}
		return string(b)
	}
	// 24 characters of 31: about 119 bits, so a plain hash is enough.
	password = pick(6) + "-" + pick(6) + "-" + pick(6) + "-" + pick(6)
	return "t-" + pick(10), password, hash(password)
}

var errNoSSHKeys = connect.NewError(connect.CodeUnimplemented, errors.New("SSH keys are gone: turn on SFTP on a server's Files tab for a temporary password"))

// ListSSHKeys implements AuthService. SSH keys are gone (DECISIONS #223).
func (s *Service) ListSSHKeys(context.Context, *panelv1.ListSSHKeysRequest) (*panelv1.ListSSHKeysResponse, error) {
	return nil, errNoSSHKeys
}

// AddSSHKey implements AuthService. SSH keys are gone (DECISIONS #223).
func (s *Service) AddSSHKey(context.Context, *panelv1.AddSSHKeyRequest) (*panelv1.AddSSHKeyResponse, error) {
	return nil, errNoSSHKeys
}

// DeleteSSHKey implements AuthService. SSH keys are gone (DECISIONS #223).
func (s *Service) DeleteSSHKey(context.Context, *panelv1.DeleteSSHKeyRequest) (*panelv1.DeleteSSHKeyResponse, error) {
	return nil, errNoSSHKeys
}
