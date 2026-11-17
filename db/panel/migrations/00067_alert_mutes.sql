-- +goose Up
-- Muting alerts about one server or node for a while (docs/PANEL.md#alerts):
-- a person's own, so a crash loop stops buzzing their phone while they fix
-- it. Muted alerts still land in their inbox, and team channels still get
-- them; only the person's own channels (their phone, email, Telegram, ...)
-- stay quiet until it runs out. target is "server:<id>" or "node:<uuid>".
CREATE TABLE alert_mutes (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    org_id     uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    target     text        NOT NULL CHECK (target ~ '^(server|node):.+$'),
    until      timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, org_id, target)
);

-- +goose Down
DROP TABLE alert_mutes;
