-- +goose Up
-- Support tickets on the Discord server (docs/PANEL.md#discord): a private
-- thread each, opened from a button. Kept so each person has one open at a
-- time; the threads themselves stay on Discord.
CREATE TABLE discord_tickets (
    thread_id  text        PRIMARY KEY,
    user_id    text        NOT NULL,
    topic      text        NOT NULL,
    opened_at  timestamptz NOT NULL DEFAULT now(),
    closed_at  timestamptz,
    closed_by  text        NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX discord_tickets_open ON discord_tickets (user_id) WHERE closed_at IS NULL;
CREATE INDEX discord_tickets_user ON discord_tickets (user_id, opened_at);

-- The bot's settings, one row. It only adds the Customer role until staff
-- turn removals on (panel discord roles enforce on), after checking what a
-- pass would take away (panel discord roles preview); last_preview is the
-- would-remove list it last posted to the staff log, so it isn't repeated.
CREATE TABLE discord_settings (
    id           bool    PRIMARY KEY DEFAULT true CHECK (id),
    remove_roles bool    NOT NULL DEFAULT false,
    last_preview text    NOT NULL DEFAULT ''
);
INSERT INTO discord_settings DEFAULT VALUES;

-- +goose Down
DROP TABLE discord_settings;
DROP TABLE discord_tickets;
