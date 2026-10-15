-- Alerts (docs/PANEL.md#alerts).

-- name: OrgAlertChannels :many
-- An org's channels, for its admins (row-level security).
SELECT id, type, name, config, hint, failing_since, last_error, last_sent_at, disabled_at, created_at
FROM alert_channels WHERE org_id = $1 ORDER BY created_at;

-- name: OrgAlertRules :many
-- An org's rules, for its admins (row-level security).
SELECT id, name, condition, node_id, server_id, threshold, for_minutes, channel_ids, notify_resolved, enabled, created_at
FROM alert_rules WHERE org_id = $1 ORDER BY created_at;

-- name: OrgAlertHistory :many
-- An org's latest alerts, for its admins (row-level security).
SELECT d.id, d.rule_id, d.channel_id, d.message, d.status, d.attempts, d.error, d.created_at, d.sent_at
FROM alert_deliveries d WHERE d.org_id = $1 ORDER BY d.created_at DESC LIMIT $2;

-- name: CountOrgAlertChannels :one
SELECT count(*) FROM alert_channels WHERE org_id = $1;

-- name: CountOrgAlertRules :one
SELECT count(*) FROM alert_rules WHERE org_id = $1;

-- name: GetAlertChannel :one
SELECT * FROM alert_channels WHERE id = $1 AND org_id = $2;

-- name: InsertAlertChannel :one
INSERT INTO alert_channels (org_id, type, name, config, secret, hint, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: UpdateAlertChannel :one
-- Renames a channel, replaces its settings or secret (NULL keeps them), and
-- turns it back on (its failures forgotten) or off.
UPDATE alert_channels SET
    name = $3,
    config = coalesce(sqlc.narg(config), config),
    secret = coalesce(sqlc.narg(secret), secret),
    hint = CASE WHEN sqlc.narg(secret)::bytea IS NULL THEN hint ELSE sqlc.arg(hint)::text END,
    disabled_at = CASE WHEN sqlc.arg(enabled)::bool THEN NULL ELSE coalesce(disabled_at, now()) END,
    failing_since = CASE WHEN sqlc.arg(enabled)::bool THEN NULL ELSE failing_since END,
    last_error = CASE WHEN sqlc.arg(enabled)::bool AND disabled_at IS NOT NULL THEN '' ELSE last_error END
WHERE id = $1 AND org_id = $2
RETURNING id;

-- name: DeleteAlertChannel :one
DELETE FROM alert_channels WHERE id = $1 AND org_id = $2 RETURNING id;

-- name: RemoveChannelFromRules :exec
UPDATE alert_rules SET channel_ids = array_remove(channel_ids, sqlc.arg(channel_id)::uuid), updated_at = now()
WHERE org_id = $1 AND sqlc.arg(channel_id)::uuid = ANY (channel_ids);

-- name: OrgChannelsExist :one
-- How many of these channels are the org's.
SELECT count(*) FROM alert_channels WHERE org_id = $1 AND id = ANY (sqlc.arg(ids)::uuid[]);

-- name: InsertAlertRule :one
INSERT INTO alert_rules (org_id, name, condition, node_id, server_id, threshold, for_minutes, channel_ids, notify_resolved, enabled, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING id;

-- name: UpdateAlertRule :one
UPDATE alert_rules SET name = $3, condition = $4, node_id = $5, server_id = $6, threshold = $7, for_minutes = $8,
    channel_ids = $9, notify_resolved = $10, enabled = $11, updated_at = now()
WHERE id = $1 AND org_id = $2
RETURNING id;

-- name: DeleteAlertRule :one
DELETE FROM alert_rules WHERE id = $1 AND org_id = $2 RETURNING id;

-- name: CreateAlertDefaults :exec
-- A new org's starting alerts (the migration's raptor_alert_defaults).
SELECT raptor_alert_defaults($1);

-- name: AlertNode :one
-- A node's org and name, for alerts about it.
SELECT org_id, name FROM nodes WHERE id = $1;

-- name: AlertRulesForNode :many
-- The enabled rules that may match a node's events: the org's, the node's,
-- and its servers'.
SELECT r.id, r.name, r.condition, r.server_id, r.threshold, r.for_minutes, r.channel_ids, r.notify_resolved
FROM alert_rules r JOIN nodes n ON n.org_id = r.org_id
WHERE n.id = $1 AND r.enabled AND (r.node_id IS NULL OR r.node_id = n.id);

-- name: AlertServerNames :many
SELECT server_id, name FROM m_servers WHERE node_id = $1;

-- name: ActiveAlertChannels :many
-- Of these channels, the ones that are on.
SELECT id FROM alert_channels WHERE id = ANY (sqlc.arg(ids)::uuid[]) AND disabled_at IS NULL;

-- name: InsertAlertDelivery :exec
INSERT INTO alert_deliveries (org_id, rule_id, channel_id, message) VALUES ($1, $2, $3, $4);

-- name: DueAlertDeliveries :many
-- Alerts to send now, with their channels, oldest first.
SELECT d.id, d.org_id, d.channel_id, d.message, d.attempts,
       c.type, c.name AS channel_name, c.config, c.secret, c.disabled_at
FROM alert_deliveries d JOIN alert_channels c ON c.id = d.channel_id
WHERE d.status = 'pending' AND d.next_at <= now()
ORDER BY d.created_at LIMIT $1;

-- name: AlertDeliverySent :exec
UPDATE alert_deliveries SET status = 'sent', attempts = attempts + 1, sent_at = now(), error = '' WHERE id = $1;

-- name: AlertDeliveryRetry :exec
UPDATE alert_deliveries SET attempts = attempts + 1, next_at = $2, error = $3 WHERE id = $1;

-- name: AlertDeliveryDone :exec
-- A delivery that failed for good, or was dropped (rate limit, channel off).
UPDATE alert_deliveries SET status = $2, attempts = attempts + sqlc.arg(tried)::int, error = $3 WHERE id = $1;

-- name: AlertChannelWorked :exec
UPDATE alert_channels SET failing_since = NULL, last_error = '', last_sent_at = now() WHERE id = $1;

-- name: AlertChannelFailed :one
UPDATE alert_channels SET failing_since = coalesce(failing_since, now()), last_error = $2
WHERE id = $1
RETURNING org_id, name, failing_since, disabled_at;

-- name: DisableAlertChannel :exec
UPDATE alert_channels SET disabled_at = now() WHERE id = $1 AND disabled_at IS NULL;

-- name: PruneAlertDeliveries :exec
DELETE FROM alert_deliveries WHERE status <> 'pending' AND created_at < now() - interval '30 days';

-- name: AlertRecipients :many
-- Who an email channel reaches: the org's members with one of the roles,
-- or the one member named.
SELECT u.email FROM org_members m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 AND (m.role = ANY (sqlc.arg(roles)::text[]) OR m.user_id = ANY (sqlc.arg(users)::uuid[]))
ORDER BY u.email;

-- name: AlertOfflineNodes :many
-- Nodes with a node_offline rule that it hasn't fired for, that no live
-- Panel instance holds (an instance that stopped saying it's alive doesn't
-- count). Nodes that never connected aren't offline, just not set up.
SELECT r.id AS rule_id, r.org_id, r.channel_ids, r.for_minutes, n.id AS node_id, n.name AS node_name, n.last_seen_at
FROM alert_rules r
JOIN nodes n ON n.org_id = r.org_id AND (r.node_id IS NULL OR r.node_id = n.id)
WHERE r.enabled AND r.condition = 'node_offline'
  AND n.deleted_at IS NULL AND n.key_revoked_at IS NULL AND n.last_seen_at IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM node_connections c JOIN panel_instances i ON i.id = c.instance_id
                  WHERE c.node_id = n.id AND i.seen_at > now() - interval '20 seconds')
  AND NOT EXISTS (SELECT 1 FROM alert_states s WHERE s.rule_id = r.id AND s.target = n.id::text);

-- name: NodesBackOnline :many
-- node_offline alerts that are over: a live instance holds the node again,
-- or it was removed, or the rule was turned off.
SELECT s.rule_id, s.target, s.since, r.org_id, r.channel_ids, r.notify_resolved, r.enabled,
       n.name AS node_name, (n.deleted_at IS NOT NULL)::bool AS node_removed
FROM alert_states s
JOIN alert_rules r ON r.id = s.rule_id
JOIN nodes n ON n.id::text = s.target
WHERE r.condition = 'node_offline'
  AND (NOT r.enabled OR n.deleted_at IS NOT NULL
       OR EXISTS (SELECT 1 FROM node_connections c JOIN panel_instances i ON i.id = c.instance_id
                  WHERE c.node_id = n.id AND i.seen_at > now() - interval '20 seconds'));

-- name: InsertAlertState :exec
INSERT INTO alert_states (rule_id, target, since) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING;

-- name: DeleteAlertState :exec
DELETE FROM alert_states WHERE rule_id = $1 AND target = $2;

-- name: NodeThresholdRules :many
-- The threshold rules a node evaluates.
SELECT r.id, r.condition, r.server_id, r.threshold, r.for_minutes
FROM alert_rules r JOIN nodes n ON n.org_id = r.org_id
WHERE n.id = $1 AND r.enabled AND r.condition = ANY (sqlc.arg(conditions)::text[])
  AND (r.node_id IS NULL OR r.node_id = n.id)
ORDER BY r.id;

-- name: ConnectedNodesForAlerts :many
-- Connected nodes, with the rules they last accepted.
SELECT n.id, n.wings_version, n.alerts_hash FROM nodes n
WHERE n.deleted_at IS NULL AND n.key_revoked_at IS NULL
  AND EXISTS (SELECT 1 FROM node_connections c JOIN panel_instances i ON i.id = c.instance_id
              WHERE c.node_id = n.id AND i.seen_at > now() - interval '20 seconds');

-- name: SetNodeAlerts :exec
UPDATE nodes SET alerts_hash = $2, alerts_error = $3 WHERE id = $1;

-- name: InsertTelegramLink :exec
INSERT INTO alert_telegram_links (token_hash, org_id, user_id, expires_at) VALUES ($1, $2, $3, $4);

-- name: UseTelegramLink :one
-- Uses up a link (once, before it expires).
UPDATE alert_telegram_links SET expires_at = now()
WHERE token_hash = $1 AND expires_at > now() AND channel_id IS NULL
RETURNING org_id, user_id;

-- name: SetTelegramLinkChannel :exec
UPDATE alert_telegram_links SET channel_id = $2 WHERE token_hash = $1;

-- name: PruneTelegramLinks :exec
DELETE FROM alert_telegram_links WHERE expires_at < now() - interval '1 day';

-- name: UserNames :many
-- Users' names (or emails) for alert text.
SELECT id, coalesce(nullif(name, ''), email)::text AS name FROM users WHERE id = ANY (sqlc.arg(ids)::uuid[]);
