-- Billing (docs/PANEL.md#billing-polar).

-- name: BillingAccount :one
SELECT * FROM billing_accounts WHERE org_id = $1;

-- name: OrgNodeCount :one
SELECT count(*)::int FROM nodes WHERE org_id = $1 AND deleted_at IS NULL;

-- name: SaveSubscription :exec
-- What Polar says about the org's subscription.
INSERT INTO billing_accounts (org_id, subscription_id, status, seats, current_period_end, trial_end,
    cancel_at_period_end, past_due_at, trial_used, fetched_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now())
ON CONFLICT (org_id) DO UPDATE SET
    subscription_id = EXCLUDED.subscription_id, status = EXCLUDED.status, seats = EXCLUDED.seats,
    current_period_end = EXCLUDED.current_period_end, trial_end = EXCLUDED.trial_end,
    cancel_at_period_end = EXCLUDED.cancel_at_period_end, past_due_at = EXCLUDED.past_due_at,
    trial_used = billing_accounts.trial_used OR EXCLUDED.trial_used,
    fetched_at = now(), updated_at = now();

-- name: BillingOrgs :many
-- Every org the sweep looks at: with nodes, or with a subscription.
SELECT o.id AS org_id,
       (SELECT count(*) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS nodes,
       coalesce(b.subscription_id, '') AS subscription_id, coalesce(b.status, '') AS status,
       coalesce(b.seats, 0) AS seats, coalesce(b.cancel_at_period_end, false) AS cancel_at_period_end,
       b.past_due_at, b.trial_end, b.unpaid_since, coalesce(b.unpaid_notified, '{}')::text[] AS unpaid_notified,
       b.fetched_at, coalesce(b.nodes, 0) AS last_nodes, coalesce(b.auto_canceled, false) AS auto_canceled
FROM orgs o
LEFT JOIN billing_accounts b ON b.org_id = o.id
WHERE EXISTS (SELECT 1 FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)
   OR coalesce(b.subscription_id, '') <> ''
ORDER BY o.id;

-- name: SetUnpaid :exec
INSERT INTO billing_accounts (org_id, unpaid_since, unpaid_notified) VALUES ($1, $2, $3)
ON CONFLICT (org_id) DO UPDATE SET unpaid_since = EXCLUDED.unpaid_since,
    unpaid_notified = EXCLUDED.unpaid_notified, updated_at = now();

-- name: AddLedger :exec
INSERT INTO billing_ledger (org_id, kind, quantity, amount_cents, currency, polar_ref, data)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: OrgLedger :many
SELECT kind, quantity, amount_cents, currency, polar_ref, at FROM billing_ledger
WHERE org_id = $1 ORDER BY at DESC LIMIT 50;

-- name: SeenWebhook :one
-- Records a delivery; false if it was already handled.
INSERT INTO billing_webhooks (id, type) VALUES ($1, $2)
ON CONFLICT (id) DO NOTHING
RETURNING true;

-- name: ForgetWebhook :exec
-- Handling it failed: let Polar's retry try again.
DELETE FROM billing_webhooks WHERE id = $1;

-- name: OrgOwnerEmails :many
SELECT u.email FROM org_members m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 AND m.role = 'owner';

-- name: OrgName :one
SELECT name FROM orgs WHERE id = $1;

-- name: NodeOrgAny :one
-- The org a node belongs to, removed or not.
SELECT org_id FROM nodes WHERE id = $1;

-- name: SetSweptNodes :exec
INSERT INTO billing_accounts (org_id, nodes, auto_canceled) VALUES ($1, $2, $3)
ON CONFLICT (org_id) DO UPDATE SET nodes = EXCLUDED.nodes, auto_canceled = EXCLUDED.auto_canceled, updated_at = now();
