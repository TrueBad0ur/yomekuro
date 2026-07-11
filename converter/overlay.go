package main

import (
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// Per-glyph overlay tuning. The uniform per-line placement (writeLineDiv) lands
// each line's endpoints and average pitch on the printed text, but the print
// isn't a perfectly even grid — early characters are often set slightly wider —
// so a uniform column drifts up to ~1 glyph away from the ink mid-line. These
// snap each transparent glyph onto the actual ink of its cell.
const (
	ocrInkThreshold = 128  // gray value at/below which a pixel counts as ink
	ocrMinInkFrac   = 0.03 // min ink coverage of a cell to trust its centroid
	ocrGlyphSnapMax = 0.5  // clamp a glyph's snap to ±this many glyph sizes
	ocrCrossBand    = 0.35 // half-width (in glyph sizes) of the ink search band
)

// decodeGray loads an image as grayscale for ink analysis, or returns nil on
// any failure (missing file, unsupported format like webp/jxl) — callers then
// fall back to uniform per-line placement.
func decodeGray(path string) *image.Gray {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var img image.Image
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(f)
	case ".png":
		img, err = png.Decode(f)
	default:
		return nil
	}
	if err != nil {
		return nil
	}
	b := img.Bounds()
	g := image.NewGray(b)
	draw.Draw(g, b, img, b.Min, draw.Src)
	return g
}

// inkCentroidMain returns the ink-weighted centroid along the main (reading)
// axis y within the cell [xLo,xHi]×[yLo,yHi], plus whether enough ink was
// present to trust it. Used to pull a glyph onto the ink it's meant to cover.
func inkCentroidMain(g *image.Gray, yLo, yHi, xLo, xHi int) (float64, bool) {
	b := g.Bounds()
	xLo, yLo = max(xLo, b.Min.X), max(yLo, b.Min.Y)
	xHi, yHi = min(xHi, b.Max.X), min(yHi, b.Max.Y)
	if xHi <= xLo || yHi <= yLo {
		return 0, false
	}
	var sum, wsum float64
	for y := yLo; y < yHi; y++ {
		row := 0
		for x := xLo; x < xHi; x++ {
			if g.GrayAt(x, y).Y <= ocrInkThreshold {
				row++
			}
		}
		if row > 0 {
			sum += float64(y) * float64(row)
			wsum += float64(row)
		}
	}
	area := float64((yHi - yLo) * (xHi - xLo))
	if wsum < ocrMinInkFrac*area {
		return 0, false
	}
	return sum / wsum, true
}

// writeLineDivVerticalPerGlyph renders a vertical OCR line as one transparent
// column div whose glyphs are individually nudged onto their printed ink.
//
// Each glyph is a position:relative <span> inside a single line container, so
// the container's text stays one continuous run — Yomitan/10ten still read the
// whole line as connected text (a per-glyph position:absolute would split every
// word). The relative top/left only move the glyph's paint and hit box onto the
// ink; they don't change the flow, so the next glyph is unaffected.
func writeLineDivVerticalPerGlyph(b *strings.Builder, text string, coords [][]float64, g *image.Gray) {
	minX, minY := coords[0][0], coords[0][1]
	maxX, maxY := minX, minY
	for _, pt := range coords {
		if len(pt) < 2 {
			continue
		}
		minX, minY = min(minX, pt[0]), min(minY, pt[1])
		maxX, maxY = max(maxX, pt[0]), max(maxY, pt[1])
	}
	lh := maxY - minY
	adv := advanceEm(text)
	if lh <= 0 || adv <= 0 {
		return
	}
	fs := lh / (adv + ocrReadStartPad + ocrReadEndPad)
	if fs <= 0 {
		fs = 16
	}
	top := minY + ocrReadStartPad*fs

	// Column-centre x at a given reading-axis y, following the quad's slant
	// (comic-text-detector line quads are often parallelograms). Uses the two
	// short-edge midpoints, so a tilted column is tracked instead of smeared
	// across its axis-aligned bounding box.
	topC := (coords[0][0] + coords[1][0]) / 2
	botC := (coords[2][0] + coords[3][0]) / 2
	colCenter := func(y float64) float64 { return topC + (botC-topC)*((y-minY)/lh) }
	avgCenter := (topC + botC) / 2
	containerLeft := avgCenter - fs/2

	var spans strings.Builder
	cum := 0.0
	for _, r := range text {
		w := runeAdvanceEm(r)
		natCenter := top + (cum+w/2)*fs // this glyph's uniform-flow centre
		cum += w

		dy := 0.0
		half := fs / 2
		searchCX := colCenter(natCenter)
		if cy, ok := inkCentroidMain(g,
			iround(natCenter-half), iround(natCenter+half),
			iround(searchCX-ocrCrossBand*fs), iround(searchCX+ocrCrossBand*fs)); ok {
			dy = clampAbs(cy-natCenter, ocrGlyphSnapMax*fs)
		}
		// Cross target: the (slant-followed) column centre at the snapped y,
		// plus the small ink-vs-quad nudge; expressed relative to the span's
		// natural position (container centre = avgCenter).
		crossTarget := colCenter(natCenter+dy) + ocrCrossNudge*fs
		dx := crossTarget - avgCenter
		fmt.Fprintf(&spans, `<span style="position:relative;top:%.1fpx;left:%.1fpx">%s</span>`,
			dy, dx, xmlEsc(string(r)))
	}

	style := fmt.Sprintf(
		"position:absolute;left:%dpx;top:%dpx;width:%dpx;font-size:%.1fpx;line-height:1;white-space:nowrap;color:transparent;cursor:text;-webkit-user-select:text;user-select:text;writing-mode:vertical-rl;",
		iround(containerLeft), iround(top), iround(fs), fs,
	)
	fmt.Fprintf(b, "    <div style=\"%s\">%s</div>\n", style, spans.String())
}

func clampAbs(v, limit float64) float64 {
	if v > limit {
		return limit
	}
	if v < -limit {
		return -limit
	}
	return v
}
