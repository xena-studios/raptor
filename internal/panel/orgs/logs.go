package orgs

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/mclogs"
	"github.com/xena-studios/raptor/internal/panel/perms"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// How many logs a user shares a minute.
const sharesPerMinute = 10

// ShareLog implements OrgService (#429).
func (s *Service) ShareLog(ctx context.Context, req *panelv1.ShareLogRequest) (*panelv1.ShareLogResponse, error) {
	content := req.GetContent()
	name := strings.TrimSpace(req.GetName())
	switch {
	case strings.TrimSpace(content) == "":
		return nil, invalid(errors.New("the log is empty"))
	case len(content) > mclogs.MaxBytes:
		return nil, invalid(errors.New("share at most 1 MiB of a log"))
	case !utf8.ValidString(content):
		return nil, invalid(errors.New("that isn't a text log"))
	case name == "" || len(name) > 512:
		return nil, invalid(errors.New("name the log"))
	}
	var org pgtype.UUID
	var sess *auth.Session
	err := s.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		sess = se
		o, nodeID, granted, err := serverAccess(ctx, q, se, req.GetOrgId(), req.GetNodeId(), req.GetServerId())
		if err != nil {
			return err
		}
		if _, err := q.NodeServer(ctx, store.NodeServerParams{NodeID: nodeID, ServerID: req.GetServerId()}); err != nil {
			return errNoServer
		}
		org = o
		// Sharing logs to mclo.gs is off in the demo.
		if err := auth.RefuseDemo(ctx, q, se, o); err != nil {
			return err
		}
		// Whoever can read the console or the files can read the log
		// (admins and owners: "*").
		if !slices.Contains(granted, "*") && !perms.Allows(granted, perms.ConsoleRead) && !perms.Allows(granted, perms.FilesRead) {
			return errNoServer
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.Auth.RateLimit(ctx, "logshare:user:"+uuid.UUID(sess.UserID.Bytes).String(), sharesPerMinute, time.Minute); err != nil {
		return nil, err
	}
	url, err := s.logs().Share(ctx, mclogs.Redact(content))
	if err != nil {
		if errors.Is(err, mclogs.ErrUnavailable) {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
		return nil, invalid(err)
	}
	_ = s.audit(ctx, nil, sess, org, "server.log.share", req.GetServerId(), nil, map[string]any{
		"node": req.GetNodeId(), "name": name, "url": url, "bytes": len(content),
	})
	return &panelv1.ShareLogResponse{Url: url}, nil
}

func (s *Service) logs() *mclogs.Client {
	if s.Logs != nil {
		return s.Logs
	}
	return &mclogs.Client{UserAgent: "xena-studios/raptor (raptorpanel.net)"}
}
