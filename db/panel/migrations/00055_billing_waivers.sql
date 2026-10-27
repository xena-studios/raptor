-- +goose Up
-- Billing waivers (docs/PANEL.md#billing-polar, DECISIONS #334): staff give
-- an org some nodes free, or all of them, and optionally its Raptor Backup
-- Storage past what's included. One in force per org; ended ones are kept.
CREATE TABLE billing_waivers (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id     uuid        NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    -- Every node, or free_nodes of them.
    all_nodes  boolean     NOT NULL DEFAULT false,
    free_nodes integer     NOT NULL DEFAULT 0 CHECK (free_nodes >= 0),
    storage    boolean     NOT NULL DEFAULT false,
    -- Staff's reason: staff see it, the org doesn't.
    reason     text        NOT NULL,
    -- When it runs out by itself (NULL: until ended). The sweep ends it
    -- then (ended_at), emailing the owners a few days before and then.
    ends_at    timestamptz,
    notified   text[]      NOT NULL DEFAULT '{}',
    created_by uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    ended_at   timestamptz,
    CHECK (all_nodes OR free_nodes > 0 OR storage)
);
CREATE UNIQUE INDEX billing_waivers_active ON billing_waivers (org_id) WHERE ended_at IS NULL;

-- Only the Panel's own pool reads it: owners see what the billing page
-- says, never the reason.
ALTER TABLE billing_waivers ENABLE ROW LEVEL SECURITY;

-- +goose Down
DROP TABLE billing_waivers;
