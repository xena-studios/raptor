-- +goose Up
-- Alerts as push notifications on Raptor's phone app (docs/PANEL.md#alerts):
-- a person's own channel of type phone, one per signed-in app. It belongs
-- to the app's session, so it goes when the session does: deleted with it,
-- and deleted as soon as it's signed out or revoked. The push token is
-- sealed like other channels' secrets.
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_type_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_type_check
    CHECK (type IN ('email', 'discord', 'slack', 'telegram', 'ntfy', 'webhook', 'phone'));
ALTER TABLE alert_channels ADD COLUMN session_id uuid UNIQUE REFERENCES sessions (id) ON DELETE CASCADE;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_phone
    CHECK ((type = 'phone') = (session_id IS NOT NULL AND user_id IS NOT NULL));

-- +goose StatementBegin
CREATE FUNCTION raptor_drop_phone_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM alert_channels WHERE session_id = NEW.id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER sessions_drop_phone_channel AFTER UPDATE OF revoked_at ON sessions
    FOR EACH ROW WHEN (NEW.revoked_at IS NOT NULL AND OLD.revoked_at IS NULL)
    EXECUTE FUNCTION raptor_drop_phone_channel();

-- +goose Down
DROP TRIGGER sessions_drop_phone_channel ON sessions;
DROP FUNCTION raptor_drop_phone_channel();
DELETE FROM alert_channels WHERE type = 'phone';
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_phone;
ALTER TABLE alert_channels DROP COLUMN session_id;
ALTER TABLE alert_channels DROP CONSTRAINT alert_channels_type_check;
ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_type_check
    CHECK (type IN ('email', 'discord', 'slack', 'telegram', 'ntfy', 'webhook'));
