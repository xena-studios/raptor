package orgs

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/alerts"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Alerts implements AlertService (docs/PANEL.md#alerts).
type Alerts struct {
	Orgs   *Service
	Alerts *alerts.Service
}

var _ panelv1connect.AlertServiceHandler = (*Alerts)(nil)

// Limits.
const (
	alertTestsPerMinute = 10
	alertChangesPerHour = 120
	maxAlertName        = 60
)

// admin checks the signed-in user is an admin or owner of the org.
func (a *Alerts) admin(ctx context.Context, orgID string) (*auth.Session, pgtype.UUID, error) {
	var sess *auth.Session
	var org pgtype.UUID
	err := a.Orgs.asUser(ctx, func(se *auth.Session, q *store.Queries) error {
		sess = se
		var err error
		org, _, err = member(ctx, q, se, orgID, "admin")
		return err
	})
	return sess, org, err
}

// changing checks the user may change the org's alerts, and how often.
func (a *Alerts) changing(ctx context.Context, orgID string) (*auth.Session, pgtype.UUID, error) {
	sess, org, err := a.admin(ctx, orgID)
	if err != nil {
		return nil, org, err
	}
	if err := a.Orgs.Auth.RateLimit(ctx, "alerts:user:"+idString(sess.UserID), alertChangesPerHour, time.Hour); err != nil {
		return nil, org, err
	}
	return sess, org, nil
}

func alertInput(err error) error {
	var ie alerts.InputError
	if errors.As(err, &ie) {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	return err
}

func alertName(name, fallback string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = fallback
	}
	if utf8.RuneCountInString(name) > maxAlertName || !utf8.ValidString(name) {
		return "", connect.NewError(connect.CodeInvalidArgument, errors.New("a name is up to 60 characters"))
	}
	return name, nil
}

func stampOf(t pgtype.Timestamptz) *timestamppb.Timestamp {
	if !t.Valid {
		return nil
	}
	return timestamppb.New(t.Time)
}

// channelTarget says what a channel sends to, without its secret.
func (a *Alerts) channelTarget(ctx context.Context, org pgtype.UUID, typ string, raw []byte, hint string) (target, emailTo string) {
	var c struct {
		To        string `json:"to"`
		ChatTitle string `json:"chat_title"`
	}
	_ = json.Unmarshal(raw, &c)
	switch typ {
	case alerts.Email:
		switch c.To {
		case "owners":
			return "The org's owners", c.To
		case "admins":
			return "The org's owners and admins", c.To
		}
		if id, err := uuid.Parse(c.To); err == nil {
			emails, _ := store.New(a.Orgs.DB).AlertRecipients(ctx, store.AlertRecipientsParams{OrgID: org, Users: []pgtype.UUID{{Bytes: id, Valid: true}}})
			if len(emails) > 0 {
				return emails[0], c.To
			}
			return "A former member (nobody gets these)", c.To
		}
	case alerts.Telegram:
		return c.ChatTitle, ""
	}
	return hint, ""
}

func (a *Alerts) channel(ctx context.Context, org pgtype.UUID, ch store.AlertChannel) *panelv1.AlertChannel {
	target, to := a.channelTarget(ctx, org, ch.Type, ch.Config, ch.Hint)
	return &panelv1.AlertChannel{
		Id: idString(ch.ID), Type: ch.Type, Name: ch.Name, Target: target, Enabled: !ch.DisabledAt.Valid,
		FailingSince: stampOf(ch.FailingSince), Error: ch.LastError, LastSentAt: stampOf(ch.LastSentAt),
		CreatedAt: timestamppb.New(ch.CreatedAt.Time), EmailTo: to,
	}
}

// ListAlertChannels implements AlertService.
func (a *Alerts) ListAlertChannels(ctx context.Context, req *panelv1.ListAlertChannelsRequest) (*panelv1.ListAlertChannelsResponse, error) {
	var org pgtype.UUID
	var rows []store.OrgAlertChannelsRow
	err := a.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		var err error
		if org, _, err = member(ctx, q, sess, req.GetOrgId(), "admin"); err != nil {
			return err
		}
		rows, err = q.OrgAlertChannels(ctx, org)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := &panelv1.ListAlertChannelsResponse{Limit: alerts.ChannelsPerOrg, Types: []string{alerts.Email}}
	if len(a.Alerts.DataKey) == 32 {
		out.Types = append(out.Types, alerts.Discord, alerts.Slack, alerts.Ntfy, alerts.Webhook)
	}
	if a.Alerts.Telegram != nil && a.Alerts.Telegram.Username() != "" {
		out.Types = append(out.Types, alerts.Telegram)
	}
	for _, r := range rows {
		out.Channels = append(out.Channels, a.channel(ctx, org, store.AlertChannel{
			ID: r.ID, Type: r.Type, Name: r.Name, Config: r.Config, Hint: r.Hint, FailingSince: r.FailingSince,
			LastError: r.LastError, LastSentAt: r.LastSentAt, DisabledAt: r.DisabledAt, CreatedAt: r.CreatedAt,
		}))
	}
	return out, nil
}

var channelLabels = map[string]string{
	alerts.Email: "Email", alerts.Discord: "Discord", alerts.Slack: "Slack",
	alerts.Ntfy: "ntfy", alerts.Webhook: "Webhook", alerts.Telegram: "Telegram",
}

// CreateAlertChannel implements AlertService.
func (a *Alerts) CreateAlertChannel(ctx context.Context, req *panelv1.CreateAlertChannelRequest) (*panelv1.CreateAlertChannelResponse, error) {
	sess, org, err := a.changing(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	name, err := alertName(req.GetName(), channelLabels[req.GetType()])
	if err != nil {
		return nil, err
	}
	in := alerts.ChannelInput{
		Type: req.GetType(), URL: req.GetUrl(), Secret: req.GetSecret(), EmailTo: req.GetEmailTo(),
		NtfyServer: req.GetNtfyServer(), NtfyTopic: req.GetNtfyTopic(), NtfyToken: req.GetNtfyToken(),
	}
	if err := a.emailMember(ctx, org, in.Type, in.EmailTo); err != nil {
		return nil, err
	}
	p, err := a.Alerts.Prepare(org, in)
	if err != nil {
		return nil, alertInput(err)
	}
	var ch store.AlertChannel
	err = pgx.BeginFunc(ctx, a.Orgs.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		n, err := q.CountOrgAlertChannels(ctx, org)
		if err != nil {
			return err
		}
		if n >= alerts.ChannelsPerOrg {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("an org can have 20 alert channels"))
		}
		id, err := q.InsertAlertChannel(ctx, store.InsertAlertChannelParams{
			OrgID: org, Type: in.Type, Name: name, Config: p.Config, Secret: p.Secret, Hint: p.Hint, CreatedBy: sess.UserID,
		})
		if err != nil {
			return err
		}
		if ch, err = q.GetAlertChannel(ctx, store.GetAlertChannelParams{ID: id, OrgID: org}); err != nil {
			return err
		}
		return a.Orgs.audit(ctx, q, sess, org, "alert.channel.create", name, nil, map[string]any{"type": in.Type})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.CreateAlertChannelResponse{Channel: a.channel(ctx, org, ch)}, nil
}

// emailMember checks an email channel names someone in the org.
func (a *Alerts) emailMember(ctx context.Context, org pgtype.UUID, typ, to string) error {
	if typ != alerts.Email || to == "owners" || to == "admins" {
		return nil
	}
	id, err := uuid.Parse(to)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("pick who gets the emails"))
	}
	emails, err := store.New(a.Orgs.DB).AlertRecipients(ctx, store.AlertRecipientsParams{OrgID: org, Users: []pgtype.UUID{{Bytes: id, Valid: true}}})
	if err != nil {
		return err
	}
	if len(emails) == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("emails go to members of the org only"))
	}
	return nil
}

func (a *Alerts) getChannel(ctx context.Context, q *store.Queries, org pgtype.UUID, id string) (store.AlertChannel, error) {
	cid, err := parseID(id, "channel")
	if err != nil {
		return store.AlertChannel{}, err
	}
	ch, err := q.GetAlertChannel(ctx, store.GetAlertChannelParams{ID: cid, OrgID: org})
	if errors.Is(err, pgx.ErrNoRows) {
		return ch, connect.NewError(connect.CodeNotFound, errors.New("no such channel"))
	}
	return ch, err
}

// UpdateAlertChannel implements AlertService.
func (a *Alerts) UpdateAlertChannel(ctx context.Context, req *panelv1.UpdateAlertChannelRequest) (*panelv1.UpdateAlertChannelResponse, error) {
	sess, org, err := a.changing(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	var ch store.AlertChannel
	err = pgx.BeginFunc(ctx, a.Orgs.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		cur, err := a.getChannel(ctx, q, org, req.GetId())
		if err != nil {
			return err
		}
		name, err := alertName(req.GetName(), cur.Name)
		if err != nil {
			return err
		}
		params := store.UpdateAlertChannelParams{ID: cur.ID, OrgID: org, Name: name, Enabled: req.GetEnabled()}
		in := alerts.ChannelInput{
			Type: cur.Type, URL: req.GetUrl(), Secret: req.GetSecret(), EmailTo: req.GetEmailTo(),
			NtfyServer: req.GetNtfyServer(), NtfyTopic: req.GetNtfyTopic(), NtfyToken: req.GetNtfyToken(),
		}
		change := false
		switch cur.Type {
		case alerts.Email:
			change = in.EmailTo != ""
		case alerts.Discord, alerts.Slack, alerts.Webhook:
			change = in.URL != ""
		case alerts.Ntfy:
			change = in.NtfyTopic != ""
		}
		if change {
			if err := a.emailMember(ctx, org, in.Type, in.EmailTo); err != nil {
				return err
			}
			p, err := a.Alerts.Prepare(org, in)
			if err != nil {
				return alertInput(err)
			}
			params.Config, params.Secret, params.Hint = p.Config, p.Secret, p.Hint
		}
		if _, err := q.UpdateAlertChannel(ctx, params); err != nil {
			return err
		}
		if ch, err = q.GetAlertChannel(ctx, store.GetAlertChannelParams{ID: cur.ID, OrgID: org}); err != nil {
			return err
		}
		return a.Orgs.audit(ctx, q, sess, org, "alert.channel.update", name, nil, map[string]any{"enabled": req.GetEnabled(), "target_changed": change})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.UpdateAlertChannelResponse{Channel: a.channel(ctx, org, ch)}, nil
}

// DeleteAlertChannel implements AlertService.
func (a *Alerts) DeleteAlertChannel(ctx context.Context, req *panelv1.DeleteAlertChannelRequest) (*panelv1.DeleteAlertChannelResponse, error) {
	sess, org, err := a.changing(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, a.Orgs.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		ch, err := a.getChannel(ctx, q, org, req.GetId())
		if err != nil {
			return err
		}
		if err := q.RemoveChannelFromRules(ctx, store.RemoveChannelFromRulesParams{OrgID: org, ChannelID: ch.ID}); err != nil {
			return err
		}
		if _, err := q.DeleteAlertChannel(ctx, store.DeleteAlertChannelParams{ID: ch.ID, OrgID: org}); err != nil {
			return err
		}
		return a.Orgs.audit(ctx, q, sess, org, "alert.channel.delete", ch.Name, nil, map[string]any{"type": ch.Type})
	})
	if err != nil {
		return nil, err
	}
	return &panelv1.DeleteAlertChannelResponse{}, nil
}

// TestAlertChannel implements AlertService.
func (a *Alerts) TestAlertChannel(ctx context.Context, req *panelv1.TestAlertChannelRequest) (*panelv1.TestAlertChannelResponse, error) {
	sess, org, err := a.admin(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err := a.Orgs.Auth.RateLimit(ctx, "alerttest:user:"+idString(sess.UserID), alertTestsPerMinute, time.Minute); err != nil {
		return nil, err
	}
	q := store.New(a.Orgs.DB)
	ch, err := a.getChannel(ctx, q, org, req.GetId())
	if err != nil {
		return nil, err
	}
	name, _ := q.OrgName(ctx, org)
	if err := a.Alerts.Test(ctx, org, ch, name); err != nil {
		return &panelv1.TestAlertChannelResponse{Error: err.Error()}, nil //nolint:nilerr // a failed test is an answer
	}
	return &panelv1.TestAlertChannelResponse{Ok: true}, nil
}

// ConnectTelegram implements AlertService.
func (a *Alerts) ConnectTelegram(ctx context.Context, req *panelv1.ConnectTelegramRequest) (*panelv1.ConnectTelegramResponse, error) {
	sess, org, err := a.changing(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	chat, group, expires, err := a.Alerts.TelegramLink(ctx, org, sess.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return &panelv1.ConnectTelegramResponse{ChatUrl: chat, GroupUrl: group, ExpiresAt: timestamppb.New(expires)}, nil
}

func conditionsProto() []*panelv1.AlertCondition {
	out := make([]*panelv1.AlertCondition, 0, len(alerts.Conditions))
	for _, c := range alerts.Conditions {
		out = append(out, &panelv1.AlertCondition{Id: c.ID, Label: c.Label, Scope: c.Scope, Threshold: c.Threshold, Unit: c.Unit, Resolves: c.Resolves})
	}
	return out
}

func ruleProto(id pgtype.UUID, name, condition string, node pgtype.UUID, server string, threshold float64, forMinutes int32,
	channels []pgtype.UUID, resolved, enabled bool, created pgtype.Timestamptz,
) *panelv1.AlertRule {
	r := &panelv1.AlertRule{
		Id: idString(id), Name: name, Condition: condition, ServerId: server, Threshold: threshold, ForMinutes: forMinutes,
		NotifyResolved: resolved, Enabled: enabled, CreatedAt: timestamppb.New(created.Time),
	}
	if node.Valid {
		r.NodeId = idString(node)
	}
	for _, c := range channels {
		r.ChannelIds = append(r.ChannelIds, idString(c))
	}
	return r
}

// ListAlertRules implements AlertService.
func (a *Alerts) ListAlertRules(ctx context.Context, req *panelv1.ListAlertRulesRequest) (*panelv1.ListAlertRulesResponse, error) {
	out := &panelv1.ListAlertRulesResponse{Conditions: conditionsProto(), Limit: alerts.RulesPerOrg}
	err := a.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		rows, err := q.OrgAlertRules(ctx, org)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out.Rules = append(out.Rules, ruleProto(r.ID, r.Name, r.Condition, r.NodeID, r.ServerID, r.Threshold, r.ForMinutes, r.ChannelIds, r.NotifyResolved, r.Enabled, r.CreatedAt))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func invalidRule(msg string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

// SaveAlertRule implements AlertService.
func (a *Alerts) SaveAlertRule(ctx context.Context, req *panelv1.SaveAlertRuleRequest) (*panelv1.SaveAlertRuleResponse, error) {
	sess, org, err := a.changing(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	in := req.GetRule()
	if in == nil {
		return nil, invalidRule("no rule")
	}
	c, ok := alerts.ConditionByID(in.GetCondition())
	if !ok {
		return nil, invalidRule("pick what the rule watches for")
	}
	name, err := alertName(in.GetName(), "")
	if err != nil {
		return nil, err
	}
	threshold, forMinutes := in.GetThreshold(), in.GetForMinutes()
	switch {
	case c.Threshold:
		if threshold <= 0 || threshold > 100 {
			return nil, invalidRule("the threshold is a percent, from 1 to 100")
		}
		if forMinutes < 1 || forMinutes > 1440 {
			return nil, invalidRule("how long it must last is 1 minute to 24 hours")
		}
	case c.ID == "node_offline":
		threshold = 0
		if forMinutes < 1 || forMinutes > 1440 {
			return nil, invalidRule("how long a node is offline before it counts is 1 minute to 24 hours")
		}
	default:
		threshold, forMinutes = 0, 0
	}
	if c.Scope == "node" && in.GetServerId() != "" {
		return nil, invalidRule("that applies to nodes, not one server")
	}
	if in.GetServerId() != "" && in.GetNodeId() == "" {
		return nil, invalidRule("a server is picked with its node")
	}
	var node pgtype.UUID
	if in.GetNodeId() != "" {
		if _, _, node, err = a.Orgs.nodeOf(ctx, req.GetOrgId(), in.GetNodeId()); err != nil {
			return nil, err
		}
	}
	if len(in.GetChannelIds()) == 0 {
		return nil, invalidRule("pick at least one channel")
	}
	var channels []pgtype.UUID
	for _, id := range in.GetChannelIds() {
		cid, err := parseID(id, "channel")
		if err != nil {
			return nil, err
		}
		if !slices.Contains(channels, cid) {
			channels = append(channels, cid)
		}
	}
	var saved *panelv1.AlertRule
	err = pgx.BeginFunc(ctx, a.Orgs.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if in.GetServerId() != "" {
			exists, err := q.MirrorServerExists(ctx, store.MirrorServerExistsParams{NodeID: node, ServerID: in.GetServerId()})
			if err != nil {
				return err
			}
			if !exists {
				return connect.NewError(connect.CodeNotFound, errors.New("no such server"))
			}
		}
		n, err := q.OrgChannelsExist(ctx, store.OrgChannelsExistParams{OrgID: org, Ids: channels})
		if err != nil {
			return err
		}
		if int(n) != len(channels) {
			return connect.NewError(connect.CodeNotFound, errors.New("no such channel"))
		}
		var id pgtype.UUID
		action := "alert.rule.update"
		if in.GetId() == "" {
			action = "alert.rule.create"
			count, err := q.CountOrgAlertRules(ctx, org)
			if err != nil {
				return err
			}
			if count >= alerts.RulesPerOrg {
				return connect.NewError(connect.CodeResourceExhausted, errors.New("an org can have 100 alert rules"))
			}
			id, err = q.InsertAlertRule(ctx, store.InsertAlertRuleParams{
				OrgID: org, Name: name, Condition: c.ID, NodeID: node, ServerID: in.GetServerId(), Threshold: threshold,
				ForMinutes: forMinutes, ChannelIds: channels, NotifyResolved: in.GetNotifyResolved(), Enabled: in.GetEnabled(), CreatedBy: sess.UserID,
			})
			if err != nil {
				return err
			}
		} else {
			rid, err := parseID(in.GetId(), "rule")
			if err != nil {
				return err
			}
			id, err = q.UpdateAlertRule(ctx, store.UpdateAlertRuleParams{
				ID: rid, OrgID: org, Name: name, Condition: c.ID, NodeID: node, ServerID: in.GetServerId(), Threshold: threshold,
				ForMinutes: forMinutes, ChannelIds: channels, NotifyResolved: in.GetNotifyResolved(), Enabled: in.GetEnabled(),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("no such rule"))
			}
			if err != nil {
				return err
			}
		}
		saved = ruleProto(id, name, c.ID, node, in.GetServerId(), threshold, forMinutes, channels, in.GetNotifyResolved(), in.GetEnabled(),
			pgtype.Timestamptz{Time: time.Now(), Valid: true})
		return a.Orgs.audit(ctx, q, sess, org, action, c.ID, nil, map[string]any{"rule": idString(id), "enabled": in.GetEnabled()})
	})
	if err != nil {
		return nil, err
	}
	a.Alerts.RulesChanged()
	return &panelv1.SaveAlertRuleResponse{Rule: saved}, nil
}

// DeleteAlertRule implements AlertService.
func (a *Alerts) DeleteAlertRule(ctx context.Context, req *panelv1.DeleteAlertRuleRequest) (*panelv1.DeleteAlertRuleResponse, error) {
	sess, org, err := a.changing(ctx, req.GetOrgId())
	if err != nil {
		return nil, err
	}
	rid, err := parseID(req.GetId(), "rule")
	if err != nil {
		return nil, err
	}
	err = pgx.BeginFunc(ctx, a.Orgs.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.DeleteAlertRule(ctx, store.DeleteAlertRuleParams{ID: rid, OrgID: org}); errors.Is(err, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeNotFound, errors.New("no such rule"))
		} else if err != nil {
			return err
		}
		return a.Orgs.audit(ctx, q, sess, org, "alert.rule.delete", "", nil, map[string]any{"rule": idString(rid)})
	})
	if err != nil {
		return nil, err
	}
	a.Alerts.RulesChanged()
	return &panelv1.DeleteAlertRuleResponse{}, nil
}

// ListAlertHistory implements AlertService.
func (a *Alerts) ListAlertHistory(ctx context.Context, req *panelv1.ListAlertHistoryRequest) (*panelv1.ListAlertHistoryResponse, error) {
	limit := req.GetLimit()
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	out := &panelv1.ListAlertHistoryResponse{}
	err := a.Orgs.asUser(ctx, func(sess *auth.Session, q *store.Queries) error {
		org, _, err := member(ctx, q, sess, req.GetOrgId(), "admin")
		if err != nil {
			return err
		}
		rows, err := q.OrgAlertHistory(ctx, store.OrgAlertHistoryParams{OrgID: org, Limit: limit})
		if err != nil {
			return err
		}
		for _, r := range rows {
			var m alerts.Message
			_ = json.Unmarshal(r.Message, &m)
			e := &panelv1.AlertHistoryEntry{
				Id: idString(r.ID), ChannelId: idString(r.ChannelID), Condition: m.Condition, Level: m.Level, Title: m.Title,
				Text: m.Text, Resolved: m.Resolved, NodeId: m.NodeID, NodeName: m.Node, ServerId: m.ServerID, ServerName: m.Server,
				Status: r.Status, Error: r.Error, At: timestamppb.New(r.CreatedAt.Time), SentAt: stampOf(r.SentAt),
			}
			if r.RuleID.Valid {
				e.RuleId = idString(r.RuleID)
			}
			out.Entries = append(out.Entries, e)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
