// Package catalog puts Raptor's egg catalog (eggs/ in the repository,
// docs/EGGS.md#built-in-catalog) into the official egg library, where
// staff manage it like any official egg (docs/PANEL.md#eggs). The Panel
// seeds it on start: eggs the library doesn't have are added, and eggs
// only the seed ever changed follow the catalog when it changes. An egg
// staff edited is theirs from then on.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	catalog "github.com/xena-studios/raptor/eggs"
	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/eggimport"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// By is who the seed's versions are by. A newest version by anyone else
// means staff changed the egg, and the seed leaves it alone.
const By = "Raptor"

// seedLock keeps two Panels starting together from seeding at once.
const seedLock = 0x7261_6567 // "raeg"

// categories maps the catalog's folders to the library's categories.
var categories = map[string]string{
	"minecraft": "minecraft", "steam": "steam", "standalone": "standalone", "software": "software",
	"database": "database", "generic": "generic", "voice": "voice",
}

// Seed adds the catalog's eggs to the official library, and updates the
// ones only the seed has changed. It reports how many it added and updated.
func Seed(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) (added, updated int, err error) {
	all, err := catalog.All()
	if err != nil {
		return 0, 0, err
	}
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", seedLock); err != nil {
			return err
		}
		q := store.New(tx)
		for _, e := range all {
			a, u, err := seedOne(ctx, q, e)
			if err != nil {
				return fmt.Errorf("%s: %w", e.ID, err)
			}
			if a {
				added++
			}
			if u {
				updated++
			}
		}
		return nil
	})
	if err == nil && (added > 0 || updated > 0) {
		log.Info("egg catalog seeded", "added", added, "updated", updated)
	}
	return added, updated, err
}

func seedOne(ctx context.Context, q *store.Queries, e catalog.Entry) (added, updated bool, err error) {
	file, err := e.ServerEgg()
	if err != nil {
		return false, false, err
	}
	egg, err := eggs.Parse(file)
	if err != nil {
		return false, false, err
	}
	sum := eggimport.Sum(file)
	source := fmt.Sprintf("https://github.com/%s/blob/%s/%s", e.Source.Repo, e.Source.Commit, e.Source.Path)
	id := pgtype.Text{String: e.ID, Valid: true}
	row, err := q.GetEggByCatalogID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		folder, _, _ := strings.Cut(e.ID, "/")
		category := categories[folder]
		if category == "" {
			category = "other"
		}
		row, err := q.AddCatalogEgg(ctx, store.AddCatalogEggParams{
			Name: name(egg), Category: category, Egg: file, Sha256: sum, SourceUrl: source, CatalogID: id, Certified: e.Certified,
		})
		if err != nil {
			return false, false, err
		}
		return true, false, q.AddEggVersion(ctx, store.AddEggVersionParams{
			EggID: row.ID, Version: 1, Egg: file, Sha256: sum, Note: "From Raptor's catalog", CreatedBy: By,
		})
	}
	if err != nil || row.Sha256 == sum {
		return false, false, err
	}
	last, err := q.LatestEggVersion(ctx, row.ID)
	if err != nil || last.CreatedBy != By {
		return false, false, err // staff's now
	}
	version := last.Version + 1
	if err := q.AddEggVersion(ctx, store.AddEggVersionParams{
		EggID: row.ID, Version: version, Egg: file, Sha256: sum, Note: "Updated from Raptor's catalog", CreatedBy: By,
	}); err != nil {
		return false, false, err
	}
	return false, true, q.UpdateCatalogEgg(ctx, store.UpdateCatalogEggParams{
		ID: row.ID, Name: name(egg), Egg: file, Sha256: sum, Version: version, SourceUrl: source, Certified: e.Certified,
	})
}

func name(e *eggs.Egg) string {
	if n := strings.TrimSpace(e.Name); n != "" {
		return n
	}
	return "Unnamed egg"
}

// Proto is what the web app needs of an egg to list it and create a
// server from it; the caller fills in the library's fields.
func Proto(egg *eggs.Egg) *panelv1.CatalogEgg {
	out := &panelv1.CatalogEgg{Name: egg.Name, Description: egg.Description, Features: egg.Features, Arch: egg.Raptor.Arch}
	for _, i := range egg.Images {
		out.Images = append(out.Images, &panelv1.EggImage{Name: i.Name, Ref: i.Ref})
	}
	for _, v := range egg.Variables {
		out.Variables = append(out.Variables, &panelv1.EggVariable{
			Name: v.Name, Description: v.Description, Env: v.Env, Default: v.Default,
			UserViewable: v.UserViewable, UserEditable: v.UserEditable, Rules: v.Rules,
		})
	}
	return out
}
