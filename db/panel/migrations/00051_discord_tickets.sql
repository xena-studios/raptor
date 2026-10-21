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

-- +goose Down
DROP TABLE discord_tickets;
