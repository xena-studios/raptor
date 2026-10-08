package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/types/known/timestamppb"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// MaxSSHKeys bounds an account's SSH keys.
const MaxSSHKeys = 50

// sshKeyTypes are the keys SFTP accepts: modern ones, and RSA of at least
// 2048 bits (checked separately). No DSA.
var sshKeyTypes = []string{
	ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519,
	ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoSKECDSA256,
	ssh.KeyAlgoRSA,
}

func sshKeyProto(k store.SshKey) *panelv1.SSHKey {
	out := &panelv1.SSHKey{Id: uuid.UUID(k.ID.Bytes).String(), Name: k.Name, Fingerprint: k.Fingerprint, CreatedAt: timestamppb.New(k.CreatedAt.Time)}
	if pk, err := ssh.ParsePublicKey(k.PublicKey); err == nil {
		out.Type = pk.Type()
	}
	if k.LastUsedAt.Valid {
		out.LastUsedAt = timestamppb.New(k.LastUsedAt.Time)
	}
	return out
}

// ListSSHKeys implements AuthService.
func (s *Service) ListSSHKeys(ctx context.Context, _ *panelv1.ListSSHKeysRequest) (*panelv1.ListSSHKeysResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q().ListSSHKeys(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListSSHKeysResponse{SftpUsername: sess.User.SftpUsername.String}
	for _, r := range rows {
		out.Keys = append(out.Keys, sshKeyProto(r))
	}
	return out, nil
}

// parseSSHKey reads one authorized_keys line.
func parseSSHKey(line string) (ssh.PublicKey, string, error) {
	pk, comment, opts, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(line)))
	if err != nil {
		return nil, "", errors.New("that isn't an SSH public key; paste one line like \"ssh-ed25519 AAAA… you@laptop\"")
	}
	if len(opts) > 0 {
		return nil, "", errors.New("paste the key without authorized_keys options")
	}
	if !slices.Contains(sshKeyTypes, pk.Type()) {
		return nil, "", fmt.Errorf("%s keys aren't accepted; use Ed25519 (ssh-keygen -t ed25519)", pk.Type())
	}
	if ck, ok := pk.(ssh.CryptoPublicKey); ok && pk.Type() == ssh.KeyAlgoRSA {
		if bits := rsaBits(ck); bits < 2048 {
			return nil, "", fmt.Errorf("RSA keys need at least 2048 bits (this one has %d); better, use Ed25519", bits)
		}
	}
	return pk, comment, nil
}

// AddSSHKey implements AuthService.
func (s *Service) AddSSHKey(ctx context.Context, req *panelv1.AddSSHKeyRequest) (*panelv1.AddSSHKeyResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	pk, comment, err := parseSSHKey(req.GetPublicKey())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		name = strings.TrimSpace(comment)
	}
	if name == "" {
		name = pk.Type()
	}
	name = truncate(name, 64)
	fp := ssh.FingerprintSHA256(pk)
	var row store.SshKey
	username := sess.User.SftpUsername.String
	err = pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if n, err := q.CountSSHKeys(ctx, sess.UserID); err != nil {
			return err
		} else if n >= MaxSSHKeys {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("an account can have up to %d SSH keys", MaxSSHKeys))
		}
		if username == "" {
			if username, err = assignSFTPUsername(ctx, tx, sess.User); err != nil {
				return err
			}
		}
		row, err = q.AddSSHKey(ctx, store.AddSSHKeyParams{UserID: sess.UserID, Name: name, PublicKey: pk.Marshal(), Fingerprint: fp})
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return connect.NewError(connect.CodeAlreadyExists, errors.New("that key is already on your account"))
		}
		if err != nil {
			return err
		}
		return s.Audit(ctx, q, Event{User: sess.UserID, Action: "ssh_key.add", Target: uuid.UUID(row.ID.Bytes).String(), Meta: map[string]any{"name": name, "fingerprint": fp}})
	})
	if err != nil {
		return nil, err
	}
	s.notify(ctx, sess.User, "An SSH key was added to your Raptor account",
		fmt.Sprintf("The SSH key %q (%s) was added to your Raptor account. It can open the files of your servers over SFTP.\n\nIf this wasn't you, remove it from your account settings and sign out every device right away.\n", name, fp))
	return &panelv1.AddSSHKeyResponse{Key: sshKeyProto(row), SftpUsername: username}, nil
}

// assignSFTPUsername gives a user their SFTP username: their email's local
// part, made safe, with digits added if it's taken.
func assignSFTPUsername(ctx context.Context, tx pgx.Tx, u store.User) (string, error) {
	local, _, _ := strings.Cut(u.Email, "@")
	base := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, strings.ToLower(local))
	if len(base) > 24 {
		base = base[:24]
	}
	if len(base) < 3 {
		base = "user" + base
	}
	for i := range 20 {
		name := base
		if i > 0 {
			n, _ := rand.Int(rand.Reader, big.NewInt(10000))
			name = fmt.Sprintf("%s%04d", base, n.Int64())
		}
		// Each try in its own savepoint: a taken name aborts the statement.
		var set int64
		err := pgx.BeginFunc(ctx, tx, func(sp pgx.Tx) error {
			var err error
			set, err = store.New(sp).SetSFTPUsername(ctx, store.SetSFTPUsernameParams{ID: u.ID, SftpUsername: pgtype.Text{String: name, Valid: true}})
			return err
		})
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue
		}
		if err != nil {
			return "", err
		}
		if set == 0 {
			// Someone else set it meanwhile: use theirs.
			fresh, err := store.New(tx).GetUser(ctx, u.ID)
			return fresh.SftpUsername.String, err
		}
		return name, nil
	}
	return "", errors.New("couldn't find a free SFTP username")
}

// DeleteSSHKey implements AuthService.
func (s *Service) DeleteSSHKey(ctx context.Context, req *panelv1.DeleteSSHKeyRequest) (*panelv1.DeleteSSHKeyResponse, error) {
	sess, err := s.Current(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.requireReauth(sess); err != nil {
		return nil, err
	}
	id, err := parseID(req.GetId())
	if err != nil {
		return nil, err
	}
	row, err := s.q().DeleteSSHKey(ctx, store.DeleteSSHKeyParams{ID: id, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("no such SSH key"))
	}
	if err != nil {
		return nil, err
	}
	_ = s.Audit(ctx, nil, Event{User: sess.UserID, Action: "ssh_key.remove", Target: req.GetId(), Meta: map[string]any{"name": row.Name, "fingerprint": row.Fingerprint}})
	s.notify(ctx, sess.User, "An SSH key was removed from your Raptor account",
		fmt.Sprintf("The SSH key %q (%s) can no longer open your servers' files.\n\nIf this wasn't you, sign out every device from your account settings right away.\n", row.Name, row.Fingerprint))
	return &panelv1.DeleteSSHKeyResponse{}, nil
}

// SFTP permissions: what a login can carry.
var sftpPerms = []string{"sftp", "files.read", "files.write"}

var errSFTPDenied = connect.NewError(connect.CodePermissionDenied, errors.New("sftp login denied"))

// SFTPLogin answers a node asking whether an SSH key, or a temporary
// password, may log in to one of its servers (nodev1.PanelService). Org
// admins and owners may; members need the sftp permission on that server,
// and carry their file permissions. The answer never says which part
// failed.
func (s *Service) SFTPLogin(ctx context.Context, nodeID string, req *nodev1.SFTPLoginRequest) (*nodev1.SFTPLoginResponse, error) {
	node, err := uuid.Parse(nodeID)
	if err != nil {
		return nil, errSFTPDenied
	}
	nodeUUID := pgtype.UUID{Bytes: node, Valid: true}
	q := s.q()
	if req.GetPassword() != "" {
		return s.sftpPasswordLogin(ctx, q, nodeUUID, req)
	}
	pk, err := ssh.ParsePublicKey(req.GetPublicKey())
	if err != nil {
		return nil, errSFTPDenied
	}
	user, err := q.GetUserBySFTPUsername(ctx, pgtype.Text{String: strings.ToLower(req.GetUsername()), Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSFTPDenied
	}
	if err != nil {
		return nil, err
	}
	key, err := q.SSHKeyByFingerprint(ctx, store.SSHKeyByFingerprintParams{UserID: user.ID, Fingerprint: ssh.FingerprintSHA256(pk)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && string(key.PublicKey) != string(pk.Marshal())) {
		return nil, errSFTPDenied
	}
	if err != nil {
		return nil, err
	}
	perms, err := sftpGrant(ctx, q, nodeUUID, req.GetServerId(), user.ID)
	if err != nil {
		return nil, err
	}
	_ = q.UseSSHKey(ctx, key.ID)
	return &nodev1.SFTPLoginResponse{UserId: uuid.UUID(user.ID.Bytes).String(), Permissions: perms}, nil
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

func rsaBits(k ssh.CryptoPublicKey) int {
	if pub, ok := k.CryptoPublicKey().(*rsa.PublicKey); ok {
		return pub.N.BitLen()
	}
	return 0
}
