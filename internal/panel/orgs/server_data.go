package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// serverWith is serverAccess, needing perm (admins and owners have every
// one).
func serverWith(ctx context.Context, q *store.Queries, sess *auth.Session, orgID, nodeID, serverID, perm string) (pgtype.UUID, error) {
	_, node, perms, err := serverAccess(ctx, q, sess, orgID, nodeID, serverID)
	if err != nil {
		return node, err
	}
	if !slices.Contains(perms, "*") && !slices.Contains(perms, perm) {
		return node, errDenied
	}
	return node, nil
}

func ts(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

// ListSchedules implements OrgService.
func (s *Service) ListSchedules(ctx context.Context, req *panelv1.ListSchedulesRequest) (*panelv1.ListSchedulesResponse, error) {
	out := &panelv1.ListSchedulesResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		node, err := serverWith(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId(), "schedules")
		if err != nil {
			return err
		}
		rows, err := q.ListMirrorSchedules(ctx, store.ListMirrorSchedulesParams{NodeID: node, ServerID: req.GetServerId()})
		for _, r := range rows {
			out.Schedules = append(out.Schedules, &panelv1.Schedule{
				Id: r.ScheduleID, Name: r.Name, Enabled: r.Enabled, NextRun: ts(r.NextRun), LastRun: ts(r.LastRun),
				DefinitionJson: string(r.Definition),
			})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// runsPage is how many runs ListScheduleRuns returns at a time.
const runsPage = 25

// ListScheduleRuns implements OrgService.
func (s *Service) ListScheduleRuns(ctx context.Context, req *panelv1.ListScheduleRunsRequest) (*panelv1.ListScheduleRunsResponse, error) {
	var before int64
	if t := req.GetPageToken(); t != "" {
		var err error
		if before, err = strconv.ParseInt(t, 10, 64); err != nil || before <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bad page token"))
		}
	}
	out := &panelv1.ListScheduleRunsResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		node, err := serverWith(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId(), "schedules")
		if err != nil {
			return err
		}
		rows, err := q.ListScheduleRuns(ctx, store.ListScheduleRunsParams{
			NodeID: node, ServerID: req.GetServerId(), ScheduleID: req.GetScheduleId(), Before: before, Lim: runsPage,
		})
		for _, r := range rows {
			var steps []*panelv1.ScheduleRunStep
			var raw []struct {
				Type  string `json:"type"`
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			_ = json.Unmarshal(r.Steps, &raw)
			for _, st := range raw {
				steps = append(steps, &panelv1.ScheduleRunStep{Type: st.Type, Ok: st.OK, Error: st.Error})
			}
			out.Runs = append(out.Runs, &panelv1.ScheduleRun{
				Id: strconv.FormatInt(r.ID, 10), ScheduleId: r.ScheduleID, Status: r.Status, Reason: r.Reason,
				SkipReason: r.SkipReason, Steps: steps, ScheduledFor: ts(r.ScheduledFor), StartedAt: ts(r.StartedAt),
				FinishedAt: ts(r.FinishedAt),
			})
		}
		if len(rows) == runsPage {
			out.NextPageToken = strconv.FormatInt(rows[len(rows)-1].ID, 10)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListBackups implements OrgService.
func (s *Service) ListBackups(ctx context.Context, req *panelv1.ListBackupsRequest) (*panelv1.ListBackupsResponse, error) {
	out := &panelv1.ListBackupsResponse{}
	err := s.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		node, err := serverWith(ctx, q, sess, req.GetOrgId(), req.GetNodeId(), req.GetServerId(), "backups")
		if err != nil {
			return err
		}
		rows, err := q.ListMirrorBackups(ctx, store.ListMirrorBackupsParams{NodeID: node, ServerID: req.GetServerId()})
		for _, r := range rows {
			out.Backups = append(out.Backups, &panelv1.Backup{
				Id: r.BackupID, Kind: r.Kind, Status: r.Status, Locked: r.Locked, Size: r.Size, Files: r.Files,
				Error: r.Error, Warning: r.Warning, CreatedAt: ts(r.CreatedAt), FinishedAt: ts(r.FinishedAt), ExpiresAt: ts(r.ExpiresAt),
			})
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
