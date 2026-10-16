-- +goose Up
-- Alerts go to people (docs/PANEL.md#alerts): a rule picks who hears about
-- it (roles, or members by name) and, as before, team channels (a Discord
-- or Slack channel, a webhook). Each person decides how they're told: every
-- alert is in their inbox in the app (the bell), and goes to the channels
-- of their own they've set up (their email, a Telegram chat with the bot,
-- an ntfy topic, …). Members only hear about servers they have access to.

-- A channel is an org's (a team channel) or a user's own.
ALTER TABLE alert_channels ALTER COLUMN org_id DROP NOT NULL;
ALTER TABLE alert_channels ADD COLUMN user_id uuid REFERENCES users (id) ON DELETE CASCADE;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_owner CHECK ((org_id IS NULL) <> (user_id IS NULL));
CREATE INDEX alert_channels_user ON alert_channels (user_id) WHERE user_id IS NOT NULL;
DROP POLICY alert_channels_read ON alert_channels;
CREATE POLICY alert_channels_read ON alert_channels FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner') OR user_id = raptor_uid());

-- Who a rule tells: everyone with one of the roles, and the members named.
ALTER TABLE alert_rules ADD COLUMN notify_roles text[] NOT NULL DEFAULT '{}'
    CHECK (notify_roles <@ ARRAY['owner', 'admin', 'member']::text[]);
ALTER TABLE alert_rules ADD COLUMN notify_user_ids uuid[] NOT NULL DEFAULT '{}';

-- A delivery to someone's own channel says whose, for the org's history
-- (which can't read other people's channels).
ALTER TABLE alert_deliveries ADD COLUMN label text NOT NULL DEFAULT '';

-- Email is now each person's: org email channels become the people they
-- reached, and everyone gets an email channel of their own (on).
UPDATE alert_rules r SET
    notify_roles = ARRAY(SELECT DISTINCT x FROM unnest(r.notify_roles || ARRAY(
        SELECT unnest(CASE c.config ->> 'to' WHEN 'owners' THEN ARRAY['owner'] WHEN 'admins' THEN ARRAY['owner', 'admin'] ELSE ARRAY[]::text[] END)
        FROM alert_channels c WHERE c.id = ANY (r.channel_ids) AND c.type = 'email')) AS x),
    notify_user_ids = ARRAY(SELECT DISTINCT x FROM unnest(r.notify_user_ids || ARRAY(
        SELECT (c.config ->> 'to')::uuid
        FROM alert_channels c WHERE c.id = ANY (r.channel_ids) AND c.type = 'email' AND c.config ->> 'to' NOT IN ('owners', 'admins'))) AS x),
    channel_ids = ARRAY(
        SELECT ch FROM unnest(r.channel_ids) AS ch
        WHERE NOT EXISTS (SELECT 1 FROM alert_channels c WHERE c.id = ch AND c.type = 'email'));
DELETE FROM alert_channels WHERE type = 'email' AND org_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION raptor_personal_email() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO alert_channels (user_id, type, name, config) VALUES (NEW.id, 'email', 'Email', '{"to": "me"}');
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER users_personal_email AFTER INSERT ON users
    FOR EACH ROW EXECUTE FUNCTION raptor_personal_email();
INSERT INTO alert_channels (user_id, type, name, config)
SELECT id, 'email', 'Email', '{"to": "me"}' FROM users;

-- A new org's starting rules tell its owners.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION raptor_alert_defaults(org uuid) RETURNS void LANGUAGE sql AS $$
    INSERT INTO alert_rules (org_id, condition, for_minutes, notify_roles)
    SELECT org, c.condition, c.for_minutes, ARRAY['owner']
    FROM (VALUES ('crash_loop', 0), ('node_offline', 5), ('node_disk_low', 0)) AS c (condition, for_minutes);
$$;
-- +goose StatementEnd

-- Connect Telegram for a user's own chat has no org.
ALTER TABLE alert_telegram_links ALTER COLUMN org_id DROP NOT NULL;

-- The inbox: every alert to every person it's for, kept 30 days. They see
-- their own, while they're in the org.
CREATE TABLE alert_notifications (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    org_id       uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    rule_id      uuid        REFERENCES alert_rules (id) ON DELETE SET NULL,
    message      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    seen_at      timestamptz,
    dismissed_at timestamptz
);
CREATE INDEX alert_notifications_user ON alert_notifications (user_id, created_at DESC) WHERE dismissed_at IS NULL;
CREATE INDEX alert_notifications_age ON alert_notifications (created_at);
ALTER TABLE alert_notifications ENABLE ROW LEVEL SECURITY;
GRANT SELECT, UPDATE ON alert_notifications TO raptor_app;
CREATE POLICY alert_notifications_own ON alert_notifications FOR ALL TO raptor_app
    USING (user_id = raptor_uid() AND raptor_org_role(org_id) IS NOT NULL);

-- +goose Down
DROP TABLE alert_notifications;
DELETE FROM alert_telegram_links WHERE org_id IS NULL;
ALTER TABLE alert_telegram_links ALTER COLUMN org_id SET NOT NULL;
DROP TRIGGER users_personal_email ON users;
DROP FUNCTION raptor_personal_email();
DELETE FROM alert_channels WHERE user_id IS NOT NULL;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION raptor_alert_defaults(org uuid) RETURNS void LANGUAGE sql AS $$
    WITH ch AS (
        INSERT INTO alert_channels (org_id, type, name, config)
        VALUES (org, 'email', 'Owners', '{"to": "owners"}')
        RETURNING id
    )
    INSERT INTO alert_rules (org_id, condition, for_minutes, channel_ids)
    SELECT org, c.condition, c.for_minutes, ARRAY[ch.id]
    FROM ch, (VALUES ('crash_loop', 0), ('node_offline', 5), ('node_disk_low', 0)) AS c (condition, for_minutes);
$$;
-- +goose StatementEnd
ALTER TABLE alert_deliveries DROP COLUMN label;
ALTER TABLE alert_rules DROP COLUMN notify_user_ids;
ALTER TABLE alert_rules DROP COLUMN notify_roles;
DROP POLICY alert_channels_read ON alert_channels;
CREATE POLICY alert_channels_read ON alert_channels FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'));
DROP INDEX alert_channels_user;
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_owner;
ALTER TABLE alert_channels DROP COLUMN user_id;
ALTER TABLE alert_channels ALTER COLUMN org_id SET NOT NULL;
