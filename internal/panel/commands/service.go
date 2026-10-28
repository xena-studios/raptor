// Package commands serves CommandService: users' commands to their nodes
// (docs/PANEL.md#permissions). The Panel decides who may run what (org
// roles, and members' server grants) and signs a grant for exactly that
// command; Wings checks the grant, and the user's passkey signature for
// dangerous actions, which the Panel can't forge.
package commands

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"maps"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/billing"
	"github.com/xena-studios/raptor/internal/panel/perms"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/telemetry"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Service is the CommandService handler.
type Service struct {
	Auth     *auth.Service
	Sender   Sender
	Consoles Consoles
	PanelKey ed25519.PrivateKey
	Now      func() time.Time
	// Billing refuses changes to an unpaid org's nodes (nil: off).
	Billing *billing.Service
}

var _ panelv1connect.CommandServiceHandler = (*Service)(nil)

// Limits.
const (
	// DefaultLifetime is how long a command the Panel names itself is good
	// for: long enough to reach a node that's reconnecting.
	DefaultLifetime  = 2 * time.Minute
	commandsPerMin   = 300
	executeTimeout   = time.Minute
	maxParamsJSONLen = 256 << 10
	// files.write carries a whole file for the editor: up to Wings' 4 MiB,
	// base64 in JSON.
	maxWriteJSONLen = 6 << 20
)

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Commands sent to nodes (raptor.commands), by action and outcome: ok, failed
// (ran and failed), or unreached. Only known actions get this far.
var executed, _ = telemetry.Meter.Int64Counter("raptor.commands", metric.WithDescription("Commands sent to nodes, by action and outcome"))

var (
	errNotFound = connect.NewError(connect.CodeNotFound, errors.New("no such server"))
	errDenied   = connect.NewError(connect.CodePermissionDenied, errors.New("you don't have permission to do that on this server"))
	uuidV7      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

// authorize checks the user may run action on the server, as them under
// row-level security: nodes of other orgs aren't visible at all.
func (s *Service) authorize(ctx context.Context, q *store.Queries, sess *auth.Session, node pgtype.UUID, action, server string) (pgtype.UUID, error) {
	perm, ok := perms.For(action)
	if !ok {
		return pgtype.UUID{}, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown action "+action))
	}
	org, err := q.NodeOrg(ctx, node)
	if errors.Is(err, pgx.ErrNoRows) {
		return org, errNotFound
	}
	if err != nil {
		return org, err
	}
	m, err := q.OrgMember(ctx, store.OrgMemberParams{OrgID: org, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return org, errNotFound
	}
	if err != nil {
		return org, err
	}
	if err := auth.OrgUsable(ctx, q, org); err != nil {
		return org, err
	}
	if m.Role == "owner" || m.Role == "admin" {
		return org, nil
	}
	// Members: only server actions their grant allows.
	if perms.AdminOnly(action) || server == "" {
		return org, errDenied
	}
	g, err := q.ServerGrant(ctx, store.ServerGrantParams{NodeID: node, ServerID: server, UserID: sess.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return org, errNotFound
	}
	if err != nil {
		return org, err
	}
	if !perms.Allows(g.Permissions, perm) {
		return org, errDenied
	}
	return org, nil
}

// Execute implements CommandService.
func (s *Service) Execute(ctx context.Context, req *panelv1.ExecuteRequest) (*panelv1.ExecuteResponse, error) {
	res, record, err := s.execute(ctx, req)
	if record != nil {
		record(nil)
	}
	return res, err
}

// ExecuteThenRecord runs a command like Execute, but leaves its entry in
// the activity to the caller: record writes it, with more about how it
// went (a streamed query's rows_changed, once it's done). It's nil when
// there's nothing to record (a read, or the command failed and was
// recorded already).
func (s *Service) ExecuteThenRecord(ctx context.Context, req *panelv1.ExecuteRequest) (*panelv1.ExecuteResponse, func(extra map[string]any), error) {
	res, record, err := s.execute(ctx, req)
	if err != nil && record != nil {
		record(nil)
		record = nil
	}
	return res, record, err
}

// execute runs a command, returning the function that records it in the
// activity (nil when it isn't recorded).
func (s *Service) execute(ctx context.Context, req *panelv1.ExecuteRequest) (*panelv1.ExecuteResponse, func(extra map[string]any), error) {
	node, err := uuid.Parse(req.GetNodeId())
	if err != nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad node ID"))
	}
	env := nodecmd.Envelope{NodeID: node.String(), Action: req.GetAction(), ServerID: req.GetServerId()}
	if p := req.GetParamsJson(); p != "" {
		limit := maxParamsJSONLen
		if req.GetAction() == "files.write" {
			limit = maxWriteJSONLen
		}
		if len(p) > limit || !json.Valid([]byte(p)) {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("params must be a JSON object"))
		}
		env.Params = json.RawMessage(p)
	}
	// The ID and expiry: the browser's, if it signed them, otherwise ours.
	now := s.now()
	switch {
	case req.GetCommandId() != "":
		if !uuidV7.MatchString(req.GetCommandId()) {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("command_id must be a lowercase UUIDv7"))
		}
		exp := time.Unix(req.GetExpiresAt(), 0)
		if !exp.After(now) || exp.After(now.Add(nodecmd.MaxLifetime)) {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("expires_at must be within the next 10 minutes"))
		}
		env.CommandID, env.ExpiresAt = req.GetCommandId(), req.GetExpiresAt()
	case req.GetSignature() != nil:
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a signed command needs the command_id and expires_at that were signed"))
	default:
		id, err := uuid.NewV7()
		if err != nil {
			return nil, nil, err
		}
		env.CommandID, env.ExpiresAt = id.String(), now.Add(DefaultLifetime).Unix()
	}
	if sig := req.GetSignature(); sig != nil {
		env.Signature = &nodecmd.PasskeySignature{
			CredentialID: sig.GetCredentialId(), AuthenticatorData: sig.GetAuthenticatorData(),
			ClientDataJSON: sig.GetClientDataJson(), Signature: sig.GetSignature(),
		}
	}

	var sess *auth.Session
	var org pgtype.UUID
	nodeID := pgtype.UUID{Bytes: node, Valid: true}
	support := req.GetSupport()
	if support != "" {
		// Raptor staff, under support access the owner approved.
		sess, org, err = s.authorizeSupport(ctx, nodeID, support, &env)
	} else {
		err = s.Auth.AsUser(ctx, func(se *auth.Session, q *store.Queries) error {
			sess = se
			var err error
			org, err = s.authorize(ctx, q, se, nodeID, env.Action, env.ServerID)
			return err
		})
	}
	if err != nil {
		return nil, nil, err
	}
	// An unpaid org's nodes are read-only; taking support access away still
	// works. Staff helping under support access aren't stopped.
	if support == "" && !perms.Read(env.Action) && env.Action != "support.revoke" && env.Action != "support.disable" {
		if err := s.Billing.CheckWrite(ctx, uuid.UUID(org.Bytes)); err != nil {
			return nil, nil, err
		}
	}
	var granted grantParams
	if env.Action == "support.grant" && support == "" {
		if granted, err = s.checkSupportGrant(ctx, nodeID, env); err != nil {
			return nil, nil, err
		}
	}
	if err := s.Auth.RateLimit(ctx, "cmd:user:"+uuid.UUID(sess.UserID.Bytes).String(), commandsPerMin, time.Minute); err != nil {
		return nil, nil, err
	}
	env.UserID = uuid.UUID(sess.UserID.Bytes).String()
	if err := nodecmd.SignGrant(s.PanelKey, &env); err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, executeTimeout)
	defer cancel()
	res, err := s.Sender.Execute(cctx, env.NodeID, raw)
	outcome := "ok"
	switch {
	case err != nil:
		outcome = "unreached" // offline, or the node refused it
	case res.GetError() != "":
		outcome = "failed"
	}
	executed.Add(ctx, 1, metric.WithAttributes(attribute.String("action", env.Action), attribute.String("outcome", outcome)))

	var record func(extra map[string]any)
	if !perms.Read(env.Action) || support != "" { // staff are logged even when they only look
		meta := map[string]any{"action": env.Action, "node": env.NodeID, "command_id": env.CommandID, "signed": env.Signature != nil}
		// Params aren't kept (they can hold file contents); whether a delete
		// skipped the trash is worth knowing.
		if env.Action == "files.delete" {
			var p struct {
				Permanent bool `json:"permanent"`
			}
			if json.Unmarshal(env.Params, &p) == nil && p.Permanent {
				meta["permanent"] = true
			}
		}
		switch {
		case err != nil:
			meta["error"] = err.Error()
		case res.GetError() != "":
			meta["error"] = res.GetError()
		case strings.HasPrefix(env.Action, "db."):
			// A database change is kept with how many rows it changed, never
			// its SQL.
			var r struct {
				Affected *int64 `json:"affected"`
			}
			if json.Unmarshal(res.GetResult(), &r) == nil && r.Affected != nil && *r.Affected >= 0 {
				meta["rows_changed"] = *r.Affected
			}
		}
		record = func(extra map[string]any) {
			maps.Copy(meta, extra)
			actx := context.WithoutCancel(ctx)
			if support != "" {
				s.auditStaff(actx, sess, org, support, env, meta)
			} else {
				_ = s.Auth.Audit(actx, nil, auth.Event{Org: org, Actor: sess.UserID, Action: "command", Target: auditTarget(env), Meta: meta})
			}
		}
	}
	if err != nil {
		return nil, record, err
	}
	if res.GetError() == "" && support == "" {
		s.afterSupport(ctx, sess, nodeID, env, granted)
	}
	if res.GetError() == "" {
		s.afterMachine(ctx, nodeID, env, res.GetResult())
	}
	if res.GetError() != "" {
		// It ran on the node and failed: the node's words, for the user.
		return nil, record, connect.NewError(connect.CodeFailedPrecondition, errors.New(res.GetError()))
	}
	return &panelv1.ExecuteResponse{CommandId: env.CommandID, ResultJson: string(res.GetResult()), Duplicate: res.GetDuplicate()}, record, nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
