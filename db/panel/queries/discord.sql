-- The Discord bot (docs/PANEL.md#discord). A Discord account is linked when
-- it signs in to a Raptor account (oauth_accounts).

-- name: DiscordCustomers :many
-- Discord accounts that should have the Customer role: linked to an owner or
-- admin of an org with a plan in force, or, when billing is off, an org with
-- a node. Suspended and deleted orgs don't count.
SELECT DISTINCT a.subject FROM oauth_accounts a
JOIN org_members m ON m.user_id = a.user_id AND m.role IN ('owner', 'admin')
JOIN orgs o ON o.id = m.org_id AND o.deleted_at IS NULL AND o.suspended_at IS NULL
WHERE a.provider = 'discord'
  AND CASE WHEN @billing::bool
      THEN EXISTS (SELECT 1 FROM billing_accounts b WHERE b.org_id = o.id AND b.status = ANY (@live::text[]))
      ELSE EXISTS (SELECT 1 FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)
  END;

-- name: DiscordUser :one
-- The Raptor account a Discord account is linked to.
SELECT u.id, u.email, u.name, u.created_at, a.created_at AS linked_at
FROM oauth_accounts a JOIN users u ON u.id = a.user_id
WHERE a.provider = 'discord' AND a.subject = $1;

-- name: DiscordUserOrgs :many
-- A linked account's orgs, for /whois and /link: role, plan, and nodes.
SELECT o.id, o.name, m.role, coalesce(b.status, '')::text AS status,
       (o.suspended_at IS NOT NULL)::bool AS suspended,
       (SELECT count(*) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::int AS nodes,
       (SELECT coalesce(string_agg(DISTINCT n.wings_version, ', ' ORDER BY n.wings_version), '')
          FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL AND n.wings_version <> '')::text AS versions,
       (SELECT max(n.last_seen_at) FROM nodes n WHERE n.org_id = o.id AND n.deleted_at IS NULL)::timestamptz AS last_seen_at
FROM org_members m
JOIN orgs o ON o.id = m.org_id AND o.deleted_at IS NULL
LEFT JOIN billing_accounts b ON b.org_id = o.id
WHERE m.user_id = $1
ORDER BY o.created_at;

-- name: OpenDiscordTicket :one
-- Someone's open ticket, if they have one.
SELECT * FROM discord_tickets WHERE user_id = $1 AND closed_at IS NULL;

-- name: AddDiscordTicket :exec
INSERT INTO discord_tickets (thread_id, user_id, topic) VALUES ($1, $2, $3);

-- name: CloseDiscordTicket :execrows
UPDATE discord_tickets SET closed_at = now(), closed_by = $2
WHERE thread_id = $1 AND closed_at IS NULL;

-- name: CountDiscordTickets :one
-- How many tickets someone opened since a time.
SELECT count(*)::int FROM discord_tickets WHERE user_id = $1 AND opened_at > $2;

-- name: DiscordSettings :one
SELECT * FROM discord_settings;

-- name: SetDiscordRemoveRoles :exec
UPDATE discord_settings SET remove_roles = $1, last_preview = '';

-- name: SetDiscordPreview :execrows
-- Records the would-remove list; 0 rows if it's the one already posted.
UPDATE discord_settings SET last_preview = $1 WHERE last_preview <> $1;
