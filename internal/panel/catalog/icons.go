package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/panel/images"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// SyncIcon gives an egg the icon its file carries (docs/PANEL.md#images):
// override if set (a catalog entry's icon.png), else the egg's own
// "image", or none. An icon an admin uploaded (icon_source "upload") is
// never replaced. A picture that isn't one just means no icon: it never
// stops the egg being saved.
func SyncIcon(ctx context.Context, q *store.Queries, e store.Egg, file, override []byte) error {
	if e.IconSource == "upload" {
		return nil
	}
	icon, source := "", ""
	if img, ok := fileIcon(file, override); ok {
		switch err := images.Save(ctx, q, img); {
		case err == nil:
			icon, source = img.ID, "file"
		case !errors.Is(err, images.ErrTakenDown):
			return err
		}
	}
	if e.Icon.String == icon && e.IconSource == source {
		return nil
	}
	return q.SetEggIcon(ctx, store.SetEggIconParams{ID: e.ID, Icon: images.Text(icon), IconSource: source})
}

func fileIcon(file, override []byte) (images.Image, bool) {
	if len(override) > 0 {
		if img, err := images.Normalize(override); err == nil {
			return img, true
		}
	}
	egg, err := eggs.Parse(file)
	if err != nil {
		return images.Image{}, false
	}
	img, ok, err := images.FromDataURI(egg.Icon)
	return img, ok && err == nil
}

// BackfillIcons gives eggs saved before eggs had icons the one their file
// carries. Eggs without one cost nothing: only files holding a data: image
// are looked at.
func BackfillIcons(ctx context.Context, db *pgxpool.Pool) (int, error) {
	q := store.New(db)
	rows, err := q.EggsWithoutIcons(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range rows {
		if err := SyncIcon(ctx, q, e, e.Egg, nil); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// SetIcon sets or clears an egg's uploaded icon; clearing it brings back
// the one its file carries.
func SetIcon(ctx context.Context, q *store.Queries, e store.Egg, icon string) error {
	if icon != "" {
		return q.SetEggIcon(ctx, store.SetEggIconParams{ID: e.ID, Icon: images.Text(icon), IconSource: "upload"})
	}
	e.IconSource = ""
	e.Icon = pgtype.Text{}
	if err := q.SetEggIcon(ctx, store.SetEggIconParams{ID: e.ID}); err != nil {
		return err
	}
	return SyncIcon(ctx, q, e, e.Egg, nil)
}
