// Package catalog puts Raptor's egg library (library/ in the repository,
// docs/EGGS.md#egg-library) into the official egg library, where staff
// manage it like any official egg (docs/PANEL.md#eggs). The Panel seeds it
// on every start: eggs the official library doesn't have are added, and
// eggs only the seed ever changed follow the library when it changes. An
// egg staff changed is theirs from then on.
package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/eggimport"
	"github.com/xena-studios/raptor/internal/panel/images"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/library"
)

// By is who the seed's versions are by. A newest version by anyone else
// means staff changed the egg, and the seed leaves it alone.
const By = "Raptor"

// Version notes.
const (
	noteAdded   = "From Raptor's egg library"
	noteUpdated = "Updated from Raptor's egg library"
)

// seedLock keeps two Panels starting together from seeding at once.
const seedLock = 0x7261_6567 // "raeg"

// categories maps the library's folders to the official library's
// categories.
var categories = map[string]string{
	"minecraft": "minecraft", "steam": "steam", "standalone": "standalone", "software": "software",
	"database": "database", "generic": "generic", "voice": "voice",
}

// Seed adds the library's eggs to the official library, updates the ones
// the seed owns, and deletes the ones the library no longer has that no
// server was made from (ones in use stay as they are). It reports how many
// it added, updated, and removed. An egg whose file, icon, and settings
// are what the seed last wrote costs a hash and nothing else.
func Seed(ctx context.Context, db *pgxpool.Pool, log *slog.Logger) (added, updated, removed int, err error) {
	all, err := library.All()
	if err != nil {
		return 0, 0, 0, err
	}
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", seedLock); err != nil {
			return err
		}
		q := store.New(tx)
		rows, err := q.LibraryEggs(ctx)
		if err != nil {
			return err
		}
		have := make(map[string]store.LibraryEggsRow, len(rows))
		for _, r := range rows {
			have[r.CatalogID] = r
		}
		ids := make([]string, len(all))
		var todo []change
		for i, e := range all {
			ids[i] = e.ID
			c, err := plan(e, have)
			if err != nil {
				return fmt.Errorf("%s: %w", e.ID, err)
			}
			if c != nil {
				todo = append(todo, *c)
			}
		}
		icons(todo)
		for _, c := range todo {
			a, u, err := c.apply(ctx, q)
			if err != nil {
				return fmt.Errorf("%s: %w", c.e.ID, err)
			}
			if a {
				added++
			}
			if u {
				updated++
			}
		}
		retired, err := q.RetiredCatalogEggs(ctx, ids)
		if err != nil {
			return err
		}
		for _, r := range retired {
			if r.Servers > 0 {
				continue
			}
			if _, err := q.DeleteEgg(ctx, r.ID); err != nil {
				return fmt.Errorf("%s: %w", r.CatalogID, err)
			}
			removed++
		}
		return nil
	})
	if err == nil && (added > 0 || updated > 0 || removed > 0) {
		log.Info("egg library seeded", "eggs", len(all), "added", added, "updated", updated, "removed", removed)
	}
	return added, updated, removed, err
}

// librarySum covers everything the seed writes from an entry: the egg
// file, its icon.png, and the settings kept beside it in the row.
func librarySum(file []byte, e library.Entry) string {
	h := sha256.New()
	for _, part := range [][]byte{file, e.Icon, []byte(strconv.FormatBool(e.Certified)), []byte(strconv.FormatBool(e.Internal))} {
		h.Write([]byte(strconv.Itoa(len(part)) + ":"))
		h.Write(part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// change is a library egg the seed has to write.
type change struct {
	e       library.Entry
	row     store.LibraryEggsRow
	exists  bool
	file    []byte
	sum     string
	icon    images.Image
	hasIcon bool
}

// plan works out whether an entry needs writing: not if staff own its egg,
// or if it's what the seed last wrote.
func plan(e library.Entry, have map[string]store.LibraryEggsRow) (*change, error) {
	row, exists := have[e.ID]
	if exists && !row.Seeded {
		return nil, nil // staff's now
	}
	file, err := e.ServerEgg()
	if err != nil {
		return nil, err
	}
	sum := librarySum(file, e)
	if exists && row.LibrarySum == sum {
		return nil, nil
	}
	return &change{e: e, row: row, exists: exists, file: file, sum: sum}, nil
}

// icons prepares the changed eggs' icons side by side: re-encoding one
// takes tens of milliseconds, and a fresh Panel seeds hundreds.
// iconMemo keeps each library icon once it's encoded, by the bytes it came
// from: encoding is the slow part of a seed, and the same embedded icons
// come back on every seed in a process (the tests seed many databases).
// It holds at most one image per library egg.
var iconMemo sync.Map // string -> memoIcon

type memoIcon struct {
	img images.Image
	ok  bool
}

func libraryIcon(file, override []byte) (images.Image, bool) {
	a, b := sha256.Sum256(file), sha256.Sum256(override)
	key := string(a[:]) + string(b[:])
	if m, ok := iconMemo.Load(key); ok {
		return m.(memoIcon).img, m.(memoIcon).ok
	}
	img, ok := fileIcon(file, override)
	iconMemo.Store(key, memoIcon{img, ok})
	return img, ok
}

func icons(todo []change) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	for i := range todo {
		wg.Add(1)
		sem <- struct{}{}
		go func(c *change) {
			defer func() { <-sem; wg.Done() }()
			c.icon, c.hasIcon = libraryIcon(c.file, c.e.Icon)
		}(&todo[i])
	}
	wg.Wait()
}

func (c change) apply(ctx context.Context, q *store.Queries) (added, updated bool, err error) {
	e, row, file, sum := c.e, c.row, c.file, c.sum
	egg, err := eggs.Parse(file)
	if err != nil {
		return false, false, err
	}
	fileSum := eggimport.Sum(file)
	source := e.SourceURL()
	if !c.exists {
		category := categories[e.Category()]
		if category == "" {
			category = "other"
		}
		row, err := q.AddCatalogEgg(ctx, store.AddCatalogEggParams{
			Name: name(egg), Category: category, Egg: file, Sha256: fileSum, SourceUrl: source,
			CatalogID: pgtype.Text{String: e.ID, Valid: true}, Certified: e.Certified, Internal: e.Internal, LibrarySum: sum,
		})
		if err != nil {
			return false, false, err
		}
		if err := q.AddEggVersion(ctx, store.AddEggVersionParams{
			EggID: row.ID, Version: 1, Egg: file, Sha256: fileSum, Note: noteAdded, CreatedBy: By,
		}); err != nil {
			return false, false, err
		}
		return true, false, setIcon(ctx, q, row, c.icon, c.hasIcon)
	}
	current := store.Egg{ID: row.ID, Icon: row.Icon, IconSource: row.IconSource}
	if row.Sha256 == fileSum {
		// The egg hasn't changed, but its icon.png or settings have (or
		// the seed hasn't recorded what it wrote yet).
		if err := q.SetEggLibrarySum(ctx, store.SetEggLibrarySumParams{
			ID: row.ID, Certified: e.Certified, Internal: e.Internal, LibrarySum: sum,
		}); err != nil {
			return false, false, err
		}
		return false, false, setIcon(ctx, q, current, c.icon, c.hasIcon)
	}
	version := row.Latest + 1
	if err := q.AddEggVersion(ctx, store.AddEggVersionParams{
		EggID: row.ID, Version: version, Egg: file, Sha256: fileSum, Note: noteUpdated, CreatedBy: By,
	}); err != nil {
		return false, false, err
	}
	if err := q.UpdateCatalogEgg(ctx, store.UpdateCatalogEggParams{
		ID: row.ID, Name: name(egg), Egg: file, Sha256: fileSum, Version: version, SourceUrl: source,
		Certified: e.Certified, Internal: e.Internal, LibrarySum: sum,
	}); err != nil {
		return false, false, err
	}
	return false, true, setIcon(ctx, q, current, c.icon, c.hasIcon)
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
