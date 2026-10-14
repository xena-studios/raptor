-- +goose Up
-- Billing through Polar (docs/PANEL.md#billing-polar): one seat-based
-- subscription per org, seats = linked nodes. Polar is the source of truth;
-- this is the Panel's copy of what it last fetched, and its own ledger.
CREATE TABLE billing_accounts (
    org_id               uuid        PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    -- The org's current (or last) subscription, as Polar last said.
    subscription_id      text        NOT NULL DEFAULT '',
    status               text        NOT NULL DEFAULT '',
    seats                integer     NOT NULL DEFAULT 0,
    current_period_end   timestamptz,
    trial_end            timestamptz,
    cancel_at_period_end boolean     NOT NULL DEFAULT false,
    past_due_at          timestamptz,
    -- The first-node trial is once per org.
    trial_used           boolean     NOT NULL DEFAULT false,
    -- When the org first had nodes and no paid subscription; read-only 7
    -- days later. NULL while it's paid (or has no nodes).
    unpaid_since         timestamptz,
    -- Which unpaid emails went out (reminder, read_only), for this spell.
    unpaid_notified      text[]      NOT NULL DEFAULT '{}',
    -- Nodes the sweep last counted, and whether it set the plan to end
    -- because the last one went: it only takes back its own cancellation,
    -- never an owner's.
    nodes                integer     NOT NULL DEFAULT 0,
    auto_canceled        boolean     NOT NULL DEFAULT false,
    fetched_at           timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE billing_accounts ENABLE ROW LEVEL SECURITY;
GRANT SELECT ON billing_accounts TO raptor_app;
CREATE POLICY billing_accounts_read ON billing_accounts FOR SELECT TO raptor_app
    USING (raptor_org_role(org_id) IS NOT NULL);

-- Every billing change, as it happened: subscription states, orders, and
-- the seat changes the Panel asked for. For reconciling with Polar.
CREATE TABLE billing_ledger (
    id           uuid        PRIMARY KEY DEFAULT uuidv7(),
    org_id       uuid        REFERENCES orgs (id) ON DELETE SET NULL,
    kind         text        NOT NULL,
    quantity     integer     NOT NULL DEFAULT 0,
    amount_cents bigint      NOT NULL DEFAULT 0,
    currency     text        NOT NULL DEFAULT '',
    polar_ref    text        NOT NULL DEFAULT '',
    data         jsonb       NOT NULL DEFAULT '{}',
    at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX billing_ledger_org ON billing_ledger (org_id, at);

-- Webhook deliveries already handled (Polar retries, and may repeat).
CREATE TABLE billing_webhooks (
    id          text        PRIMARY KEY,
    type        text        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE billing_webhooks;
DROP TABLE billing_ledger;
DROP TABLE billing_accounts;
