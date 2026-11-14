package images

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func encode(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A 1×1 lossless WebP.
var webp1x1, _ = base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")

func TestNormalize(t *testing.T) {
	for _, tc := range []struct {
		name          string
		data          []byte
		width, height int
	}{
		{"png", encode(t, "png", 640, 480), 256, 256},
		{"jpeg", encode(t, "jpeg", 300, 900), 256, 256},
		{"gif", encode(t, "gif", 100, 60), 60, 60},
		{"small png keeps its size", encode(t, "png", 16, 16), 16, 16},
		{"webp", webp1x1, 1, 1},
	} {
		img, err := Normalize(tc.data)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(img.PNG))
		if err != nil || format != "png" || cfg.Width != tc.width || cfg.Height != tc.height || img.Width != tc.width || img.Height != tc.height {
			t.Errorf("%s: %s %dx%d (%v), want png %dx%d", tc.name, format, cfg.Width, cfg.Height, err, tc.width, tc.height)
		}
		if !ValidID(img.ID) {
			t.Errorf("%s: ID %q", tc.name, img.ID)
		}
		again, _ := Normalize(tc.data)
		if again.ID != img.ID {
			t.Errorf("%s: the same picture got two IDs", tc.name)
		}
	}
}

// Only the pixels are kept: a JPEG's EXIF (with a GPS position) is gone.
func TestNormalizeDropsMetadata(t *testing.T) {
	data := encode(t, "jpeg", 64, 64)
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x1A}, []byte("Exif\x00\x00GPSLatitude=51.5074")...)
	data = append(append(append([]byte{}, data[:2]...), exif...), data[2:]...)
	if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("the test JPEG doesn't decode: %v", err)
	}
	img, err := Normalize(data)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(img.PNG, []byte("GPS")) || bytes.Contains(img.PNG, []byte("Exif")) {
		t.Error("metadata survived")
	}
}

func TestNormalizeRefuses(t *testing.T) {
	// A PNG header claiming 100000×100000 pixels: refused before decoding.
	bomb := encode(t, "png", 1, 1)
	bomb[16], bomb[17], bomb[18], bomb[19] = 0, 1, 0x86, 0xA0
	bomb[20], bomb[21], bomb[22], bomb[23] = 0, 1, 0x86, 0xA0
	binary.BigEndian.PutUint32(bomb[29:33], crc32.ChecksumIEEE(bomb[12:29])) // the IHDR chunk's CRC
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	for name, tc := range map[string]struct {
		data []byte
		want error
	}{
		"svg":       {svg, ErrFormat},
		"html":      {[]byte("<html><script>alert(1)</script>"), ErrFormat},
		"empty":     {nil, ErrFormat},
		"truncated": {encode(t, "png", 64, 64)[:100], ErrFormat},
		"bomb":      {bomb, ErrSize},
		"too large": {make([]byte, MaxBytes+1), ErrTooLarge},
	} {
		if _, err := Normalize(tc.data); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

func TestFromDataURI(t *testing.T) {
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encode(t, "png", 32, 32))
	img, ok, err := FromDataURI(uri)
	if !ok || err != nil || img.Width != 32 {
		t.Errorf("a data URI: %v %v %v", img.Width, ok, err)
	}
	if _, ok, err := FromDataURI(""); ok || err != nil {
		t.Errorf("no image: %v %v", ok, err)
	}
	if _, ok, err := FromDataURI("https://example.com/icon.png"); ok || err != nil {
		t.Errorf("a link is ignored, never fetched: %v %v", ok, err)
	}
	if _, ok, err := FromDataURI("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>"))); !ok || !errors.Is(err, ErrFormat) {
		t.Errorf("an SVG: %v %v", ok, err)
	}
	if _, ok, err := FromDataURI("data:text/html,<script>"); !ok || !errors.Is(err, ErrFormat) {
		t.Errorf("not base64: %v %v", ok, err)
	}
}

// The handler only answers well-formed IDs, with headers that keep a
// picture from being anything but a picture.
func TestHandlerRefusesBadIDs(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET "+Path+"{name}", Handler(nil))
	for _, p := range []string{"x.png", strings.Repeat("a", 64), strings.Repeat("A", 64) + ".png", strings.Repeat("a", 64) + ".svg"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, Path+p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: headers %v", p, rec.Header())
		}
	}
}
