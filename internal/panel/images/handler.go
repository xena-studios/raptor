package images

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Path is where pictures are served: <Path><id>.png.
const Path = "/api/images/"

// Handler serves stored pictures. They're public to anyone with the
// address, which is only shown to people who may see the picture, and
// never change, so browsers keep them a week; Cloudflare keeps them an hour,
// so one taken down soon stops being served from its cache too.
func Handler(db store.DBTX) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		id, ok := strings.CutSuffix(r.PathValue("name"), ".png")
		if !ok || !ValidID(id) {
			h.Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		etag := `"` + id + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		png, err := store.New(db).ImagePNG(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			h.Set("Cache-Control", "public, max-age=60")
			http.NotFound(w, r)
			return
		}
		if err != nil {
			h.Set("Cache-Control", "no-store")
			http.Error(w, "the picture couldn't be read", http.StatusServiceUnavailable)
			return
		}
		h.Set("Content-Type", "image/png")
		h.Set("Content-Length", strconv.Itoa(len(png)))
		h.Set("Cache-Control", "public, max-age=604800, immutable")
		h.Set("CDN-Cache-Control", "max-age=3600")
		h.Set("ETag", etag)
		if r.Method != http.MethodHead {
			_, _ = w.Write(png) //nolint:gosec // a PNG the Panel wrote itself, as image/png with nosniff and a sandboxing CSP
		}
	})
}
