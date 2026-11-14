// Package images is how the Panel takes pictures from people: org logos,
// user avatars, and egg icons (docs/PANEL.md#images). Nothing is kept as
// sent: an upload is decoded (PNG, JPEG, GIF, or WebP; never SVG, which can
// carry script), cropped square, scaled down to Size, and written out again
// as a PNG, which drops everything but the pixels (EXIF, GPS positions,
// comments, extra frames). Images are stored once, by the SHA-256 of that
// PNG, which is also their ID and their address.
package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/xena-studios/raptor/internal/panel/store"
)

// Limits on what's taken.
const (
	// Size is the largest side kept, in pixels. Pictures are shown at 32 to
	// 96 CSS pixels; 256 covers high-density screens.
	Size = 256
	// MaxBytes is the largest upload.
	MaxBytes = 1 << 20
	// maxSide and maxPixels bound what's decoded, checked from the header
	// first: a small file can claim a huge image.
	maxSide   = 8192
	maxPixels = 40 << 20
)

// Errors a caller can show.
var (
	ErrTooLarge = fmt.Errorf("the picture is too large (at most %d MB)", MaxBytes>>20)
	ErrFormat   = errors.New("the picture must be a PNG, JPEG, GIF, or WebP")
	ErrSize     = fmt.Errorf("the picture is too big (at most %d×%d pixels)", maxSide, maxSide)
)

// Image is a picture ready to store.
type Image struct {
	ID            string // hex SHA-256 of PNG
	PNG           []byte
	Width, Height int
}

// idPattern is what an image ID looks like.
var idPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidID reports whether id looks like an image ID.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Normalize decodes a picture and writes it out again as a square PNG of
// at most Size pixels a side. Off-centre pictures are cropped to their
// middle; pictures smaller than Size keep their size, so pixel art stays
// sharp.
func Normalize(data []byte) (Image, error) {
	if len(data) > MaxBytes {
		return Image{}, ErrTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, ErrFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxSide || cfg.Height > maxSide || cfg.Width*cfg.Height > maxPixels {
		return Image{}, ErrSize
	}
	var src image.Image
	r := bytes.NewReader(data)
	switch format {
	case "png":
		src, err = png.Decode(r)
	case "jpeg":
		src, err = jpeg.Decode(r)
	case "gif":
		src, err = gif.Decode(r) // the first frame
	case "webp":
		src, err = webp.Decode(r)
	default:
		return Image{}, ErrFormat
	}
	if err != nil {
		return Image{}, ErrFormat
	}
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	crop := image.Rect(0, 0, side, side).Add(image.Pt(b.Min.X+(b.Dx()-side)/2, b.Min.Y+(b.Dy()-side)/2))
	out := image.NewNRGBA(image.Rect(0, 0, min(side, Size), min(side, Size)))
	if side <= Size {
		draw.Draw(out, out.Bounds(), src, crop.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(out, out.Bounds(), src, crop, xdraw.Src, nil)
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, out); err != nil {
		return Image{}, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return Image{ID: hex.EncodeToString(sum[:]), PNG: buf.Bytes(), Width: out.Bounds().Dx(), Height: out.Bounds().Dy()}, nil
}

// FromDataURI normalizes a picture given as a data: URI, as eggs carry
// their icons ("data:image/png;base64,…"). ok is false when s isn't one
// (empty, or a link, which isn't fetched).
func FromDataURI(s string) (img Image, ok bool, err error) {
	rest, found := strings.CutPrefix(strings.TrimSpace(s), "data:")
	if !found {
		return Image{}, false, nil
	}
	meta, payload, found := strings.Cut(rest, ",")
	if !found || !strings.HasSuffix(meta, ";base64") || !strings.HasPrefix(meta, "image/") {
		return Image{}, true, ErrFormat
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > MaxBytes+3 {
		return Image{}, true, ErrTooLarge
	}
	data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(payload), ""))
	if err != nil {
		return Image{}, true, ErrFormat
	}
	img, err = Normalize(data)
	return img, true, err
}

// ErrTakenDown is returned for a picture Raptor's staff took down.
var ErrTakenDown = errors.New("this picture can't be used")

// Save stores img, or refreshes it if it's stored already.
func Save(ctx context.Context, q *store.Queries, img Image) error {
	_, err := q.SaveImage(ctx, store.SaveImageParams{ID: img.ID, Png: img.PNG, Width: int32(img.Width), Height: int32(img.Height)}) //nolint:gosec // at most Size
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTakenDown
	}
	return err
}

// Upload normalizes and stores a picture someone sent, and returns its ID.
// Problems with the picture are INVALID_ARGUMENT, with a message to show.
func Upload(ctx context.Context, q *store.Queries, data []byte) (string, error) {
	img, err := Normalize(data)
	if err == nil {
		err = Save(ctx, q, img)
	}
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrFormat) || errors.Is(err, ErrSize) || errors.Is(err, ErrTakenDown) {
		return "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	return img.ID, err
}

// Text is an image ID as a nullable column: "" is NULL.
func Text(id string) pgtype.Text { return pgtype.Text{String: id, Valid: id != ""} }
