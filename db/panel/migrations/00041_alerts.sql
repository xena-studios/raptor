-- +goose Up
-- Alerts (docs/PANEL.md#alerts): an org's channels (where alerts go) and
-- rules (what's worth one). Admins and owners read them through row-level
-- security; the Panel writes them after checking the user's role, and sends
-- them from its own connection.
CREATE TABLE alert_channels (
    id            uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id        uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    type          text        NOT NULL CHECK (type IN ('email', 'discord', 'slack', 'telegram', 'ntfy', 'webhook')),
    name          text        NOT NULL,
    -- What isn't secret: email's recipients, a Telegram chat, an ntfy
    -- server and topic.
    config        jsonb       NOT NULL DEFAULT '{}',
    -- What is, sealed with PANEL_DATA_KEY: webhook URLs (anyone holding a
    -- Discord or Slack one can post to it), a webhook's signing secret, an
    -- ntfy token. Never shown again; hint is what the app shows instead.
    secret        bytea,
    hint          text        NOT NULL DEFAULT '',
    -- Delivery health: failing since the first send that failed every try
    -- (cleared by one that works); turned off after 3 days of it.
    failing_since timestamptz,
    last_error    text        NOT NULL DEFAULT '',
    last_sent_at  timestamptz,
    disabled_at   timestamptz,
    created_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX alert_channels_org ON alert_channels (org_id);

-- A rule: a condition, where it applies (the whole org, one node, or one
-- server), and the channels it goes to.
CREATE TABLE alert_rules (
    id              uuid             PRIMARY KEY DEFAULT uuidv7(),
    org_id          uuid             NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name            text             NOT NULL DEFAULT '',
    condition       text             NOT NULL,
    node_id         uuid             REFERENCES nodes (id) ON DELETE CASCADE,
    server_id       text             NOT NULL DEFAULT '',
    -- Thresholds: the value (a percent, or players) and how long it must
    -- hold; node_offline uses for_minutes as its grace.
    threshold       double precision NOT NULL DEFAULT 0,
    for_minutes     integer          NOT NULL DEFAULT 0 CHECK (for_minutes BETWEEN 0 AND 1440),
    channel_ids     uuid[]           NOT NULL DEFAULT '{}',
    notify_resolved boolean          NOT NULL DEFAULT true,
    enabled         boolean          NOT NULL DEFAULT true,
    created_by      uuid             REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz      NOT NULL DEFAULT now(),
    updated_at      timestamptz      NOT NULL DEFAULT now(),
    CHECK (server_id = '' OR node_id IS NOT NULL)
);
CREATE INDEX alert_rules_org ON alert_rules (org_id);
CREATE INDEX alert_rules_condition ON alert_rules (condition) WHERE enabled;

-- Conditions the Panel watches itself (a node offline) while they last, so
-- each fires once and resolves once.
CREATE TABLE alert_states (
    rule_id uuid        NOT NULL REFERENCES alert_rules (id) ON DELETE CASCADE,
    target  text        NOT NULL,
    since   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (rule_id, target)
);

-- Every alert to every channel: the queue while pending, then the org's
-- alert history (kept 30 days).
CREATE TABLE alert_deliveries (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id     uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    rule_id    uuid        REFERENCES alert_rules (id) ON DELETE SET NULL,
    channel_id uuid        NOT NULL REFERENCES alert_channels (id) ON DELETE CASCADE,
    message    jsonb       NOT NULL,
    status     text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed', 'dropped')),
    attempts   integer     NOT NULL DEFAULT 0,
    next_at    timestamptz NOT NULL DEFAULT now(),
    error      text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    sent_at    timestamptz
);
CREATE INDEX alert_deliveries_due ON alert_deliveries (next_at) WHERE status = 'pending';
CREATE INDEX alert_deliveries_org ON alert_deliveries (org_id, created_at DESC);

-- Telegram: "Connect Telegram" makes a one-time token the bot gets back
-- from /start, which adds the chat as a channel. Stored hashed.
CREATE TABLE alert_telegram_links (
    token_hash bytea       PRIMARY KEY,
    org_id     uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    channel_id uuid        REFERENCES alert_channels (id) ON DELETE SET NULL
);

-- The threshold rules a node evaluates (it has the metrics; the Panel
-- doesn't): a hash of the last set it accepted, and why the last send
-- failed (an older Wings doesn't know node.alerts).
ALTER TABLE nodes ADD COLUMN alerts_hash text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN alerts_error text NOT NULL DEFAULT '';

ALTER TABLE alert_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE alert_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE alert_deliveries ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON alert_channels, alert_rules, alert_deliveries TO raptor_app;
CREATE POLICY alert_channels_read ON alert_channels FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY alert_rules_read ON alert_rules FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'));
CREATE POLICY alert_deliveries_read ON alert_deliveries FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IN ('admin', 'owner'));

-- Every org starts with its owners emailed when a server is down after
-- repeated crashes, a node is offline, or a node's disk is low; existing
-- orgs too. Notifications set in Wings' config.yml before this are no
-- longer sent (DECISIONS #269).
-- +goose StatementBegin
CREATE FUNCTION raptor_alert_defaults(org uuid) RETURNS void LANGUAGE sql AS $$
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
SELECT raptor_alert_defaults(id) FROM orgs;

-- +goose Down
DROP FUNCTION raptor_alert_defaults(uuid);
ALTER TABLE nodes DROP COLUMN alerts_error;
ALTER TABLE nodes DROP COLUMN alerts_hash;
DROP TABLE alert_telegram_links;
DROP TABLE alert_deliveries;
DROP TABLE alert_states;
DROP TABLE alert_rules;
DROP TABLE alert_channels;
