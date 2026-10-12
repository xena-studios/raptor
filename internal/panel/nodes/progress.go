package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Bootstrap progress (docs/ARCHITECTURE.md#enrollment): `raptor bootstrap`
// reports its steps with the join token, for the Add Node page.

// JoinStep is one step as stored and shown.
type JoinStep struct {
	Name   string    `json:"name"`
	State  string    `json:"state"`
	Detail string    `json:"detail,omitempty"`
	At     time.Time `json:"at"`
}

const (
	maxJoinSteps  = 20
	maxStepName   = 80
	maxStepDetail = 500
)

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}

// ReportProgress implements EnrollmentService.
func (r *Registry) ReportProgress(ctx context.Context, req *nodev1.ReportProgressRequest) (*nodev1.ReportProgressResponse, error) {
	if !strings.HasPrefix(req.GetToken(), nodelink.JoinTokenPrefix) || len(req.GetSteps()) > maxJoinSteps {
		return nil, connect.NewError(connect.CodePermissionDenied, ErrBadToken)
	}
	now := time.Now()
	steps := make([]JoinStep, 0, len(req.GetSteps()))
	for _, s := range req.GetSteps() {
		state := s.GetState()
		if state != "running" && state != "done" && state != "failed" {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("a step is running, done, or failed"))
		}
		steps = append(steps, JoinStep{Name: clip(s.GetName(), maxStepName), State: state, Detail: clip(s.GetDetail(), maxStepDetail), At: now})
	}
	err := pgx.BeginFunc(ctx, r.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		tok, err := q.GetJoinTokenForUpdate(ctx, hashToken(req.GetToken()))
		if errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodePermissionDenied, ErrBadToken)
		}
		if err != nil {
			return err
		}
		// A step that hasn't changed keeps the time it got there.
		var before []JoinStep
		_ = json.Unmarshal(tok.Progress, &before)
		for i := range steps {
			if i < len(before) && before[i].Name == steps[i].Name && before[i].State == steps[i].State {
				steps[i].At = before[i].At
			}
		}
		b, err := json.Marshal(steps)
		if err != nil {
			return err
		}
		n, err := q.SetJoinProgress(ctx, store.SetJoinProgressParams{TokenHash: hashToken(req.GetToken()), Progress: b})
		if err == nil && n == 0 {
			return connect.NewError(connect.CodePermissionDenied, ErrBadToken)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &nodev1.ReportProgressResponse{}, nil
}

// JoinProgress returns a join token's progress, and its node once it has
// linked. The caller checked the user may see the org's tokens.
func (r *Registry) JoinProgress(ctx context.Context, org pgtype.UUID, token string) ([]JoinStep, string, error) {
	row, err := r.q().GetJoinProgress(ctx, store.GetJoinProgressParams{TokenHash: hashToken(token), OrgID: org})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", connect.NewError(connect.CodeNotFound, errors.New("no such join token in this org"))
	}
	if err != nil {
		return nil, "", err
	}
	var steps []JoinStep
	if err := json.Unmarshal(row.Progress, &steps); err != nil {
		return nil, "", err
	}
	node := ""
	if row.NodeID.Valid {
		node = UUIDString(row.NodeID)
	}
	return steps, node, nil
}
