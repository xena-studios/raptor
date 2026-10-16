-- +goose Up
-- A theme's style (docs/PANEL.md#auth), beside its mode: standard, high
-- contrast, or calm (neutral greys, red only where it means something).
ALTER TABLE users ADD COLUMN theme_style text NOT NULL DEFAULT 'standard'
    CHECK (theme_style IN ('standard', 'contrast', 'calm'));

-- +goose Down
ALTER TABLE users DROP COLUMN theme_style;
