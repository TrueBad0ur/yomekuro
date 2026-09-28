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
	img = trimWhitespaceMargins(img)

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

// whiteLevel is the per-channel (8-bit) threshold above which a pixel counts
// as "blank canvas" rather than actual artwork.
const whiteLevel = 250

// contentFraction is the minimum share of sampled pixels in a column/row
// that must be non-blank for that column/row to count as "has content".
const contentFraction = 0.02

// trimWhitespaceMargins cuts off a wide blank margin some raw scans leave
// next to the actual cover art — e.g. a real cover found in the wild:
// 1521x1080, cover artwork occupying only the left ~half, the rest plain
// white canvas. A centered object-fit:cover crop (see targetW/targetH above)
// on the untrimmed image splits the difference: half the character cropped
// off on one side, blank white filling the other. Only landscape-ish
// sources (aspect > 1.15, the same fused-two-page-spread threshold reader.js
// uses) are even considered, and only if trimming removes a substantial
// margin (>15% of width) — a normal wraparound/spread cover with art filling
// the whole canvas has no such margin and is left untouched.
func trimWhitespaceMargins(img image.Image) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= h*115/100 {
		return img
	}

	minContent := int(float64(h) * contentFraction)
	left := findContentColumn(img, b, minContent, 1)
	right := findContentColumn(img, b, minContent, -1)
	if left < 0 || right < 0 || right <= left {
		return img
	}

	if float64(right-left+1) >= float64(w)*0.85 {
		return img
	}

	const pad = 4
	left = max(b.Min.X, left-pad)
	right = min(b.Max.X-1, right+pad)

	type subImager interface {
		SubImage(image.Rectangle) image.Image
	}
	si, ok := img.(subImager)
	if !ok {
		return img
	}
	return si.SubImage(image.Rect(left, b.Min.Y, right+1, b.Max.Y))
}

// findContentColumn scans columns from one edge (dir=1 for left-to-right,
// dir=-1 for right-to-left) and returns the first column with enough
// non-blank pixels, or -1 if none found.
func findContentColumn(img image.Image, b image.Rectangle, minContent, dir int) int {
	start, end := b.Min.X, b.Max.X
	if dir < 0 {
		start, end = b.Max.X-1, b.Min.X-1
	}
	for x := start; x != end; x += dir {
		cnt := 0
		for y := b.Min.Y; y < b.Max.Y; y += 2 { // sample every other row
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < whiteLevel || g>>8 < whiteLevel || bl>>8 < whiteLevel {
				cnt++
				if cnt > minContent {
					return x
				}
			}
		}
	}
	return -1
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
