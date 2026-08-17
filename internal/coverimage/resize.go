// Package coverimage downscales cover images extracted from EPUBs before
// they're saved to disk. Manga covers are the raw first-page scan (often
// several MB, sometimes 10+ MB) — full resolution buys nothing for a card
// thumbnail and blows past what a browser keeps warm in its HTTP cache once
// a library grid loads a few dozen of them at once.
package coverimage

import (
	"bytes"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// targetW/targetH match the card grid's CSS box for a cover image
// (`.book-card img { aspect-ratio: 2/3; object-fit: cover }` in style.css).
// Capping by the longest side alone under-sizes wide source images (a cover
// extracted from a fused two-page-spread scan, landscape instead of
// portrait): object-fit:cover in a portrait box scales such a source up to
// match its *height*, and a longest-side-only cap leaves the height far too
// small for that, producing a visibly blurry thumbnail. Sizing to cover a
// 400x600 box — the same box the browser actually renders into — keeps
// every orientation sharp at its real display size.
const (
	targetW = 400
	targetH = 600
)

const jpegQuality = 85

// Downscale re-encodes data as a JPEG sized to just cover a 400x600 box
// (see above), never upscaling. If data can't be decoded, or is already too
// small to shrink, it returns the input unchanged so a decode failure never
// loses a cover outright.
func Downscale(data []byte, mediaType string) ([]byte, string) {
	img, err := decode(data, mediaType)
	if err != nil {
		return data, mediaType
	}

	b := img.Bounds()
	w, h := b.Dx(), b.Dy()

	scale := max(float64(targetW)/float64(w), float64(targetH)/float64(h))
	if scale >= 1 {
		// Source is already too small (in whichever dimension binds for a
		// 2:3 cover-fit box) to shrink without upscaling.
		return data, mediaType
	}

	dw, dh := int(float64(w)*scale), int(float64(h)*scale)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return data, mediaType
	}
	return buf.Bytes(), "image/jpeg"
}

func decode(data []byte, mediaType string) (image.Image, error) {
	r := bytes.NewReader(data)
	switch mediaType {
	case "image/jpeg":
		return jpeg.Decode(r)
	case "image/png":
		return png.Decode(r)
	case "image/gif":
		return gif.Decode(r)
	case "image/webp":
		return webp.Decode(r)
	default:
		img, _, err := image.Decode(r)
		return img, err
	}
}
