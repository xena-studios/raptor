-- Billing (docs/PANEL.md#billing-polar).

-- name: BillingAccount :one
SELECT * FROM billing_accounts WHERE org_id = $1;

-- name: OrgNodeCount :one
SELECT count(*)::int FROM nodes WHERE org_id = $1 AND deleted_at IS NULL;

-- name: SaveSubscription :exec
-- What Polar says about the org's subscription.
INSERT INTO billing_accounts (org_id, subscription_id, status, seats, current_period_end, trial_end,
    cancel_at_period_end, past_due_at, trial_used, storage_priced, storage_cents, fetched_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now(), now())
ON CONFLICT (org_id) DO UPDATE SET
    subscription_id = EXCLUDED.subscription_id, status = EXCLUDED.status, seats = EXCLUDED.seats,
    current_period_end = EXCLUDED.current_period_end, trial_end = EXCLUDED.trial_end,
    cancel_at_period_end = EXCLUDED.cancel_at_period_end, past_due_at = EXCLUDED.past_due_at,
    trial_used = billing_accounts.trial_used OR EXCLUDED.trial_used,
    storage_priced = EXCLUDED.storage_priced, storage_cents = EXCLUDED.storage_cents,
    fetched_at = now(), updated_at = now();

-- name: BillingOrgs :many
-- Every org the sweep looks at: with nodes, or with a subscription.
SELECT o.id AS org_id,
       (SELECT count(*) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS nodes,
       coalesce(b.subscription_id, '') AS subscription_id, coalesce(b.status, '') AS status,
       coalesce(b.seats, 0) AS seats, coalesce(b.cancel_at_period_end, false) AS cancel_at_period_end,
       b.past_due_at, b.trial_end, b.unpaid_since, coalesce(b.unpaid_notified, '{}')::text[] AS unpaid_notified,
       b.fetched_at, coalesce(b.nodes, 0) AS last_nodes, coalesce(b.auto_canceled, false) AS auto_canceled,
       w.id AS waiver_id, coalesce(w.all_nodes, false) AS waiver_all_nodes, coalesce(w.free_nodes, 0) AS waiver_free_nodes,
       coalesce(w.storage, false) AS waiver_storage, w.ends_at AS waiver_ends_at,
       coalesce(w.notified, '{}')::text[] AS waiver_notified
FROM orgs o
LEFT JOIN billing_accounts b ON b.org_id = o.id
LEFT JOIN billing_waivers w ON w.org_id = o.id AND w.ended_at IS NULL
WHERE EXISTS (SELECT 1 FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)
   OR coalesce(b.subscription_id, '') <> ''
   OR w.id IS NOT NULL
ORDER BY o.id;

-- name: SetUnpaid :exec
INSERT INTO billing_accounts (org_id, unpaid_since, unpaid_notified) VALUES ($1, $2, $3)
ON CONFLICT (org_id) DO UPDATE SET unpaid_since = EXCLUDED.unpaid_since,
    unpaid_notified = EXCLUDED.unpaid_notified, updated_at = now();

-- name: AddLedger :exec
INSERT INTO billing_ledger (org_id, kind, quantity, amount_cents, currency, polar_ref, data)
VALUES ($1, $2, $3, $4, $5, $6, $7);

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

-- Staff insights (the admin panel's Billing tab).

-- name: BillingPlanCounts :many
-- Seats on a plan whose nodes are all waived don't count: it ends with
-- its period (the seats can't go below one before then).
SELECT b.status, count(*)::int AS orgs,
       coalesce(sum(CASE WHEN w.id IS NOT NULL AND (w.all_nodes OR (w.free_nodes > 0 AND
                (SELECT count(*) FROM nodes n WHERE n.org_id = b.org_id AND n.deleted_at IS NULL) <= w.free_nodes))
            THEN 0 ELSE b.seats END), 0)::int AS seats
FROM billing_accounts b
LEFT JOIN billing_waivers w ON w.org_id = b.org_id AND w.ended_at IS NULL
WHERE b.subscription_id <> '' GROUP BY b.status ORDER BY b.status;

-- name: BillingUnpaidCounts :one
SELECT count(*) FILTER (WHERE unpaid_since IS NOT NULL)::int AS unpaid,
       count(*) FILTER (WHERE unpaid_since < now() - interval '7 days')::int AS read_only
FROM billing_accounts;

-- name: RevenueByMonth :many
SELECT to_char(date_trunc('month', at), 'YYYY-MM') AS month,
       coalesce(sum(amount_cents) FILTER (WHERE kind = 'order.paid'), 0)::bigint AS paid_cents,
       coalesce(sum(amount_cents) FILTER (WHERE kind = 'order.refunded'), 0)::bigint AS refunded_cents
FROM billing_ledger WHERE kind IN ('order.paid', 'order.refunded') AND at >= $1
GROUP BY 1 ORDER BY 1;

-- name: SetOwnerCanceled :exec
-- An owner cancelled or resumed the plan: from now on it's theirs, and
-- the sweep leaves it alone.
UPDATE billing_accounts SET auto_canceled = false, updated_at = now() WHERE org_id = $1;

-- Waivers (staff giving an org nodes or storage free; DECISIONS #334).

-- name: ActiveWaiver :one
SELECT * FROM billing_waivers WHERE org_id = $1 AND ended_at IS NULL;

-- name: InsertWaiver :one
INSERT INTO billing_waivers (org_id, all_nodes, free_nodes, storage, reason, ends_at, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: UpdateWaiver :one
-- A changed end date is a new one to remind about.
UPDATE billing_waivers SET all_nodes = $2, free_nodes = $3, storage = $4, reason = $5, ends_at = $6,
    notified = CASE WHEN ends_at IS DISTINCT FROM $6 THEN '{}' ELSE notified END, updated_at = now()
WHERE id = $1 AND ended_at IS NULL RETURNING *;

-- name: EndWaiver :execrows
UPDATE billing_waivers SET ended_at = @ended_at, updated_at = now() WHERE id = @id AND ended_at IS NULL;

-- name: SetWaiverNotified :exec
UPDATE billing_waivers SET notified = $2 WHERE id = $1;

-- name: WaiverCounts :one
-- Orgs with a waiver in force, and the nodes it covers.
SELECT count(*)::int AS orgs,
       coalesce(sum(CASE WHEN w.all_nodes THEN x.nodes ELSE least(w.free_nodes, x.nodes) END), 0)::int AS nodes
FROM billing_waivers w
CROSS JOIN LATERAL (SELECT count(*)::int AS nodes FROM nodes n WHERE n.org_id = w.org_id AND n.deleted_at IS NULL) x
WHERE w.ended_at IS NULL;

-- Raptor Backup Storage, charged (DECISIONS #335).

-- name: StorageDaysToBill :many
-- Measured days since @since that haven't been billed or skipped, with
-- what decides it: the org's nodes (each includes 10 GB), its plan, and a
-- storage waiver.
SELECT u.org_id, u.day, u.bytes,
       (SELECT count(*) FROM nodes n WHERE n.org_id = u.org_id AND n.deleted_at IS NULL)::int AS nodes,
       coalesce(b.status, '') AS status, coalesce(b.storage_priced, false) AS storage_priced,
       EXISTS (SELECT 1 FROM billing_waivers w WHERE w.org_id = u.org_id AND w.ended_at IS NULL AND w.storage
               AND (w.ends_at IS NULL OR w.ends_at > now()))::bool AS waived
FROM backup_storage_usage u
LEFT JOIN billing_accounts b ON b.org_id = u.org_id
WHERE u.day >= @since
  AND NOT EXISTS (SELECT 1 FROM backup_storage_billing x WHERE x.org_id = u.org_id AND x.day = u.day)
ORDER BY u.day, u.org_id;

-- name: AddStorageBilling :exec
INSERT INTO backup_storage_billing (org_id, day, used_bytes, over_bytes, gb_months, skipped, sent_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (org_id, day) DO NOTHING;
