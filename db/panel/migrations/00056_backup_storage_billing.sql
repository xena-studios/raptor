-- +goose Up
-- Raptor Backup Storage, charged (docs/PANEL.md#raptor-backup-storage,
-- DECISIONS #335): each org's use past what's included goes to Polar as one
-- event a day, metered by a price on the node product.

-- What the Panel did with each measured day: sent to Polar (sent_at, with
-- the GB-months it billed) or not, and why (skipped). One row per org per
-- day, so a day is never billed twice; Polar also drops a repeated event
-- by its external ID.
CREATE TABLE backup_storage_billing (
    org_id     uuid             NOT NULL,
    day        date             NOT NULL,
    used_bytes bigint           NOT NULL,
    over_bytes bigint           NOT NULL,
    gb_months  double precision NOT NULL DEFAULT 0,
    -- '' when sent; else no_plan, not_on_plan, waived, or included.
    skipped    text             NOT NULL DEFAULT '',
    sent_at    timestamptz,
    at         timestamptz      NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, day)
);
ALTER TABLE backup_storage_billing ENABLE ROW LEVEL SECURITY;

-- The subscription's storage meter, as Polar last said: whether its plan
-- has the metered price, and what's due for it so far this period.
ALTER TABLE billing_accounts
    ADD COLUMN storage_priced boolean NOT NULL DEFAULT false,
    ADD COLUMN storage_cents  bigint  NOT NULL DEFAULT 0;

-- Which reminders went out before a turned-off folder's data is deleted
-- (off, 7d, 1d). Folders already turned off had their turn-off long ago:
-- only the reminders still ahead go to them.
ALTER TABLE backup_storage ADD COLUMN notified text[] NOT NULL DEFAULT '{}';
UPDATE backup_storage SET notified = '{off}' WHERE disabled_at IS NOT NULL AND purged_at IS NULL;

-- +goose Down
ALTER TABLE backup_storage DROP COLUMN notified;
ALTER TABLE billing_accounts DROP COLUMN storage_cents, DROP COLUMN storage_priced;
DROP TABLE backup_storage_billing;
