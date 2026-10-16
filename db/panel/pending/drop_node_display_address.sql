-- Not a migration: sqlc reads this after db/panel/migrations, so the
-- generated code no longer reads nodes.display_address while the column is
-- still there for v0.5.0, which does (DECISIONS #253). Migration 42 moved
-- shown addresses to servers and emptied it. In the release after the next
-- one this becomes a migration and the file goes (docs/ROADMAP.md).
ALTER TABLE nodes DROP COLUMN display_address;
