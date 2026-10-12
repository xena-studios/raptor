-- +goose Up
-- What Raptor tells everyone signed in (docs/PANEL.md#announcements):
-- maintenance, incidents, news. Plain text and an optional https link,
-- shown as a banner between starts_at and ends_at.
CREATE TABLE announcements (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    level      text        NOT NULL CHECK (level IN ('info', 'warning', 'maintenance')),
    title      text        NOT NULL CHECK (length(title) BETWEEN 1 AND 120),
    body       text        NOT NULL DEFAULT '' CHECK (length(body) <= 1000),
    link       text        NOT NULL DEFAULT '' CHECK (link = '' OR link LIKE 'https://%'),
    starts_at  timestamptz NOT NULL DEFAULT now(),
    ends_at    timestamptz,
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX announcements_active ON announcements (starts_at, ends_at);

-- +goose Down
DROP TABLE announcements;
