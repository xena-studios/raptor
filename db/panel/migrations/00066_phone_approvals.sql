-- +goose Up
-- Approving sign-ins from Raptor's phone app (docs/PANEL.md#phone-approvals):
-- instead of typing an authenticator app's code, the person taps the number
-- the browser shows on their phone, behind Face ID.
--
-- An approver is a signed-in app with a secret only it holds (behind Face ID
-- in the phone's Keychain or Keystore); the Panel keeps it sealed with
-- PANEL_DATA_KEY. It belongs to the app's session: signing the phone out
-- ends it.
CREATE TABLE phone_approvers (
    session_id   uuid        PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    secret       bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);
CREATE INDEX phone_approvers_user ON phone_approvers (user_id);

-- A request to approve: a sign-in waiting for its second factor, or a
-- browser session confirming it's the person. The browser shows number; the
-- phone offers choices, one of them number. challenge is what the phone's
-- answer is computed over.
CREATE TABLE phone_approvals (
    id                uuid        PRIMARY KEY DEFAULT uuidv7(),
    user_id           uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose           text        NOT NULL CHECK (purpose IN ('signin', 'reauth')),
    pending_signin_id uuid        REFERENCES pending_signins (id) ON DELETE CASCADE,
    session_id        uuid        REFERENCES sessions (id) ON DELETE CASCADE,
    number            int         NOT NULL,
    choices           int[]       NOT NULL,
    challenge         bytea       NOT NULL,
    ip                inet,
    user_agent        text        NOT NULL DEFAULT '',
    status            text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'denied', 'used')),
    expires_at        timestamptz NOT NULL,
    decided_at        timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CHECK ((purpose = 'signin') = (pending_signin_id IS NOT NULL)),
    CHECK ((purpose = 'reauth') = (session_id IS NOT NULL))
);
CREATE INDEX phone_approvals_user ON phone_approvals (user_id, created_at DESC);

-- Signing a phone out ends it as an approver, as it ends its alerts.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION raptor_drop_phone_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM alert_channels WHERE session_id = NEW.id;
    DELETE FROM phone_approvers WHERE session_id = NEW.id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION raptor_drop_phone_channel() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM alert_channels WHERE session_id = NEW.id;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
DROP TABLE phone_approvals;
DROP TABLE phone_approvers;
