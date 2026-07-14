// Package thumbnail renders a plain text-card cover image for books that have
// no cover of their own — currently just standalone HTML-library files, which
// (unlike EPUB) never carry any embedded cover image to extract.
package thumbnail

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed assets/NotoSansCJKjp-Regular.otf
var fontFS embed.FS

var (
	titleFace font.Face
	bodyFace  font.Face
)

func init() {
	data, err := fontFS.ReadFile("assets/NotoSansCJKjp-Regular.otf")
	if err != nil {
		panic("thumbnail: embedded font missing: " + err.Error())
	}
	f, err := opentype.Parse(data)
	if err != nil {
		panic("thumbnail: parse embedded font: " + err.Error())
	}
	titleFace, err = opentype.NewFace(f, &opentype.FaceOptions{Size: 26, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	bodyFace, err = opentype.NewFace(f, &opentype.FaceOptions{Size: 17, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
}

const (
	width  = 400
	height = 600
	margin = 26
)

var (
	bgColor    = color.RGBA{38, 38, 44, 255}
	titleColor = color.RGBA{235, 235, 240, 255}
	bodyColor  = color.RGBA{165, 165, 175, 255}
	lineColor  = color.RGBA{80, 80, 90, 255}
)

// Render draws a title + text-excerpt card, PNG-encoded — the HTML-library
// equivalent of an EPUB's extracted cover image.
func Render(title, excerpt string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	y := margin + 30
	y = drawWrapped(img, titleFace, title, margin, y, width-2*margin, titleColor, 4)

	y += 14
	drawHLine(img, margin, y, width-margin, lineColor)
	y += 34

	drawWrapped(img, bodyFace, excerpt, margin, y, width-2*margin, bodyColor, 15)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

func drawHLine(img draw.Image, x0, y, x1 int, col color.Color) {
	for x := x0; x < x1; x++ {
		img.Set(x, y, col)
	}
}

// drawWrapped word-wraps text (rune-by-rune — correct for Japanese, which has
// no spaces) to maxWidth, draws up to maxLines, and returns the y position
// just past the last line drawn.
func drawWrapped(dst draw.Image, face font.Face, text string, x, y, maxWidth int, col color.Color, maxLines int) int {
	lineHeight := face.Metrics().Height.Ceil() + 6
	lines := wrapText(face, text, maxWidth, maxLines)

	d := &font.Drawer{Dst: dst, Src: image.NewUniform(col), Face: face}
	for _, line := range lines {
		d.Dot = fixed.P(x, y)
		d.DrawString(line)
		y += lineHeight
	}
	return y
}

func wrapText(face font.Face, text string, maxWidth, maxLines int) []string {
	maxW := fixed.I(maxWidth)
	var lines []string
	var cur []rune
	curW := fixed.I(0)

	flush := func() bool {
		lines = append(lines, string(cur))
		cur = nil
		curW = 0
		return len(lines) >= maxLines
	}

	for _, r := range text {
		if r == '\n' {
			if flush() {
				return lines
			}
			continue
		}
		adv, ok := face.GlyphAdvance(r)
		if !ok {
			adv = fixed.I(10)
		}
		if curW+adv > maxW && len(cur) > 0 {
			if flush() {
				return lines
			}
		}
		cur = append(cur, r)
		curW += adv
	}
	if len(cur) > 0 && len(lines) < maxLines {
		lines = append(lines, string(cur))
	}
	return lines
}
