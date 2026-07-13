package main

import (
	"archive/zip"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type imgEntry struct {
	id        string
	href      string
	mediaType string
}

// Matches either "... v02"/"... Vol 3"/"... 4" (whitespace + optional word +
// digits at the end) or a Japanese-style "...（05）" parenthesized volume
// number — matched against the halfwidth-normalized name (see
// toHalfwidthVolume), since real-world tankobon filenames use fullwidth
// digits and fullwidth parens (１２３, （ ）), not ASCII ones.
var reVolSuffix = regexp.MustCompile(`(?i)(\s+(v\.?\s*|vol\.?\s*|volume\s*)?\d+|\(\s*\d+\s*\))\s*$`)

// seriesName strips trailing volume number from a volume name.
// "Dungeon Meshi v01" → "Dungeon Meshi", "SAO Vol 3" → "SAO",
// "葬送のフリーレン（０５）" → "葬送のフリーレン"
func seriesName(volumeName string) string {
	s := reVolSuffix.ReplaceAllString(strings.TrimSpace(toHalfwidthVolume(volumeName)), "")
	s = strings.TrimSpace(s)
	if s == "" {
		return volumeName
	}
	return s
}

// volumeIndex extracts a numeric index from a volume name, e.g. "Dungeon Meshi v02" → 2.
func volumeIndex(name string) float64 {
	matches := reVolSuffix.FindString(toHalfwidthVolume(name))
	nums := regexp.MustCompile(`\d+`).FindString(matches)
	if nums == "" {
		return 1
	}
	n, _ := strconv.Atoi(nums)
	if n == 0 {
		return 1
	}
	return float64(n)
}

var reLeadingNum = regexp.MustCompile(`^\s*0*(\d+)`)

// leadingVolumeIndex extracts a leading number from name, e.g.
// "1 Kage no koibito" → 1. Unlike volumeIndex (trailing "vNN"/"（NN）"
// patterns, for series where each volume's own name embeds the series
// title), this handles anthology-style naming where the number comes first
// and the rest of the name is an unrelated per-item title.
func leadingVolumeIndex(name string) (float64, bool) {
	m := reLeadingNum.FindStringSubmatch(toHalfwidthVolume(name))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n == 0 {
		return 0, false
	}
	return float64(n), true
}

// toHalfwidthVolume maps fullwidth digits (U+FF10-U+FF19) and fullwidth
// parens (U+FF08/09) to their ASCII equivalents, leaving everything else
// untouched.
func toHalfwidthVolume(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case r == '（':
			return '('
		case r == '）':
			return ')'
		}
		return r
	}, s)
}

func buildEPUB(vol MokuroVolume, inputDir, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// mimetype must be first entry, uncompressed
	mw, err := zw.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(mw, "application/epub+zip"); err != nil {
		return err
	}

	if err := addText(zw, "META-INF/container.xml", containerXML()); err != nil {
		return err
	}

	var images []imgEntry

	for i, page := range vol.Pages {
		imgSrc := filepath.Join(inputDir, vol.Volume, filepath.FromSlash(page.ImgPath))
		ext := strings.ToLower(filepath.Ext(page.ImgPath))
		mt := extMediaType(ext)
		destHref := fmt.Sprintf("images/%s", url.PathEscape(filepath.Base(page.ImgPath)))

		if err := addFile(zw, "OPS/"+destHref, imgSrc); err != nil {
			return fmt.Errorf("page %d image: %w", i+1, err)
		}
		images = append(images, imgEntry{
			id:        fmt.Sprintf("img%04d", i+1),
			href:      destHref,
			mediaType: mt,
		})
	}

	for i, page := range vol.Pages {
		imgHref := fmt.Sprintf("../images/%s", url.PathEscape(filepath.Base(page.ImgPath)))
		xhtml := pageXHTML(i+1, page, imgHref, vol.OCR)
		if err := addText(zw, fmt.Sprintf("OPS/pages/p%04d.xhtml", i+1), xhtml); err != nil {
			return err
		}
	}

	if err := addText(zw, "OPS/nav.xhtml", navXHTML(vol)); err != nil {
		return err
	}

	if err := addText(zw, "OPS/content.opf", contentOPF(vol, images)); err != nil {
		return err
	}

	return nil
}

func containerXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
}

func contentOPF(vol MokuroVolume, images []imgEntry) string {
	var b strings.Builder
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	series := vol.Series
	if series == "" {
		series = seriesName(vol.Volume)
	}
	idx := vol.SeriesIndex
	if idx == 0 {
		idx = volumeIndex(vol.Volume)
	}

	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid" xml:lang="ja">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:`)
	b.WriteString(vol.VolumeUUID)
	b.WriteString(`</dc:identifier>
    <dc:title>`)
	b.WriteString(xmlEsc(vol.Volume))
	b.WriteString(`</dc:title>
    <dc:language>ja</dc:language>
    <meta property="dcterms:modified">`)
	b.WriteString(now)
	b.WriteString(`</meta>
    <meta property="rendition:layout">pre-paginated</meta>
    <meta property="rendition:orientation">auto</meta>
    <meta property="rendition:spread">landscape</meta>
    <meta id="series-id" property="belongs-to-collection">`)
	b.WriteString(xmlEsc(series))
	b.WriteString(`</meta>
    <meta refines="#series-id" property="collection-type">series</meta>
    <meta refines="#series-id" property="group-position">`)
	b.WriteString(strconv.FormatFloat(idx, 'f', -1, 64))
	b.WriteString(`</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
`)
	for i := range images {
		properties := ""
		if i == 0 {
			// First page doubles as the cover — matches EPUB3 level-1 detection
			// in internal/epub/cover.go (properties contains "cover-image").
			properties = ` properties="cover-image"`
		}
		fmt.Fprintf(&b, `    <item id="%s" href="%s" media-type="%s"%s/>
`, images[i].id, images[i].href, images[i].mediaType, properties)
	}
	for i := range vol.Pages {
		fmt.Fprintf(&b, `    <item id="p%04d" href="pages/p%04d.xhtml" media-type="application/xhtml+xml"/>
`, i+1, i+1)
	}
	b.WriteString(`  </manifest>
  <spine page-progression-direction="rtl">
`)
	for i := range vol.Pages {
		fmt.Fprintf(&b, `    <itemref idref="p%04d"/>
`, i+1)
	}
	b.WriteString(`  </spine>
</package>`)
	return b.String()
}

func navXHTML(vol MokuroVolume) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<head><meta charset="UTF-8"/><title>`)
	b.WriteString(xmlEsc(vol.Volume))
	b.WriteString(`</title></head>
<body>
  <nav epub:type="toc">
    <ol>
`)
	for i := range vol.Pages {
		fmt.Fprintf(&b, `      <li><a href="pages/p%04d.xhtml">Page %d</a></li>
`, i+1, i+1)
	}
	b.WriteString(`    </ol>
  </nav>
</body>
</html>`)
	return b.String()
}

func pageXHTML(num int, page MokuroPage, imgHref string, ocr bool) string {
	w, h := page.ImgWidth, page.ImgHeight
	if w == 0 {
		w = 1350
	}
	if h == 0 {
		h = 1920
	}

	// All styles are inline so they survive reader.js body-only insertion.
	pageStyle := fmt.Sprintf(
		"position:relative;width:%dpx;height:%dpx;overflow:hidden;margin:0 auto;",
		w, h,
	)
	imgStyle := "position:absolute;top:0;left:0;width:100%;height:100%;display:block;"

	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
  <meta charset="UTF-8"/>
  <meta name="viewport" content="width=%d, height=%d"/>
  <title>Page %d</title>
</head>
<body style="margin:0;padding:0;">
  <div style="%s">
    <img src="%s" alt="page %d" style="%s"/>
`, w, h, num, pageStyle, imgHref, num, imgStyle)

	for _, blk := range page.Blocks {
		// Preferred path: render each line at its own coordinates so transparent
		// glyphs land exactly on the image characters (correct Yomitan hit-testing).
		if len(blk.LinesCoords) == len(blk.Lines) && len(blk.Lines) > 0 {
			ref := blockRefFontSize(blk, ocr)
			for li, line := range blk.Lines {
				writeLineDiv(&b, line, blk.LinesCoords[li], blk.Vertical, ocr, ref)
			}
			continue
		}
		// Fallback (no per-line coords): position joined text in the block box.
		writeBlockDiv(&b, strings.Join(blk.Lines, ""), blk.Box, blk.FontSize, blk.Vertical)
	}

	b.WriteString(`  </div>
</body>
</html>`)
	return b.String()
}

// advanceEm approximates a string's rendered advance in em units (its width at
// font-size 1): fullwidth glyphs advance ~1em, halfwidth/ASCII ~0.55em. Used
// instead of a raw rune count so punctuation and halfwidth/Latin don't skew the
// derived font size on a mixed-script line.
func advanceEm(text string) float64 {
	var adv float64
	for _, r := range text {
		if r < 0x2000 || (r >= 0xff61 && r <= 0xff9f) {
			adv += 0.55
		} else {
			adv += 1
		}
	}
	return adv
}

// Offsets between a mokuro line quad and the ink actually printed inside it,
// measured over 300+ vertical lines / 16 pages (rendered-text box vs image ink,
// using the slant-aware geometry below): comic-text-detector pads the
// reading-START of a quad by ~0.21 glyph and its reading-END by ~0.05, and the
// ink sits ~0.05 glyph to the right of the column's centre line.
//
// Anchoring text at the quad's top-left (the plain placement below) instead
// lands every column ~12px left of and ~10px above the real characters. These
// constants put it back on the glyphs. They describe the OCR detector's
// geometry, so they apply only to OCR volumes — pdftotext quads
// (buildTextVolume) are already tight to the text.
// The pads differ per orientation, so both were calibrated the same way against
// real pages: vertical over 300+ columns, horizontal over 380+ rows. The
// horizontal quad in particular runs ~1.7x taller than its glyphs, so anchoring
// the row at the quad's top edge (as the plain placement does) floats every line
// ~0.35 glyph above the print.
const (
	ocrVertStartPad, ocrVertEndPad, ocrVertCrossNudge = 0.21, 0.05, 0.05
	ocrHorStartPad, ocrHorEndPad, ocrHorCrossNudge    = 0.09, 0.05, 0.00

	// A line whose own fitted size exceeds its block's median by more than this
	// is trusted no further than the median — see blockRefFontSize.
	ocrFontOutlier = 1.15
)

func ocrPads(vertical bool) (start, end, nudge float64) {
	if vertical {
		return ocrVertStartPad, ocrVertEndPad, ocrVertCrossNudge
	}
	return ocrHorStartPad, ocrHorEndPad, ocrHorCrossNudge
}

// lineGeometry resolves an OCR line quad into the centre line of its text:
// where the glyphs start, how far they run, the fitted glyph size, and how far
// the line leans.
//
// Detector quads are often parallelograms, and a tilted one's axis-aligned bbox
// both over-states its size and hides the lean — so the reading axis is taken
// from the midpoints of the quad's two leading/trailing short edges instead.
// For a vertical line those are the top and bottom edges; for a horizontal one,
// the left and right.
func lineGeometry(coords [][]float64, text string, vertical bool) (startX, startY, fs, deg float64, ok bool) {
	if len(coords) != 4 {
		return 0, 0, 0, 0, false
	}
	adv := advanceEm(strings.TrimSpace(text))
	if adv <= 0 {
		return 0, 0, 0, 0, false
	}
	var ax, ay, bx, by float64 // leading and trailing edge midpoints
	if vertical {
		ax, ay = (coords[0][0]+coords[1][0])/2, (coords[0][1]+coords[1][1])/2
		bx, by = (coords[2][0]+coords[3][0])/2, (coords[2][1]+coords[3][1])/2
	} else {
		ax, ay = (coords[0][0]+coords[3][0])/2, (coords[0][1]+coords[3][1])/2
		bx, by = (coords[1][0]+coords[2][0])/2, (coords[1][1]+coords[2][1])/2
	}
	main := math.Hypot(bx-ax, by-ay)
	if main <= 0 {
		return 0, 0, 0, 0, false
	}
	startPad, endPad, _ := ocrPads(vertical)
	fs = main / (adv + startPad + endPad)
	if fs <= 0 {
		return 0, 0, 0, 0, false
	}
	// Text starts one start-pad along the line's own axis.
	startX = ax + startPad*fs*(bx-ax)/main
	startY = ay + startPad*fs*(by-ay)/main

	// Clockwise rotation that makes the div lean the way the quad does. For a
	// vertical column that's the sideways drift of its foot; for a horizontal
	// row, the drop of its far end.
	var sin float64
	if vertical {
		sin = (ax - bx) / main
	} else {
		sin = (by - ay) / main
	}
	deg = math.Asin(math.Max(-1, math.Min(1, sin))) * 180 / math.Pi
	return startX, startY, fs, deg, true
}

// blockRefFontSize is the median fitted size of a block's lines, or 0 when
// there are too few to be meaningful.
//
// Print sets a whole block at one size, so the median is the block's true glyph
// size. A line's own fitted size divides its extent by the number of characters
// the OCR *read* — so when the OCR drops characters (one real case: it read a
// parenthesised year as 3 characters fewer than printed) the size comes out
// far too big and the line is stretched right off the glyphs. Capping such
// outliers at the block's median keeps their glyphs on the print; the line then
// simply ends short instead of skewing along its whole length.
func blockRefFontSize(blk MokuroBlock, ocr bool) float64 {
	if !ocr {
		return 0
	}
	var sizes []float64
	for li, line := range blk.Lines {
		// Short lines carry too little text for a reliable fit.
		if utf8.RuneCountInString(strings.TrimSpace(line)) < 6 {
			continue
		}
		if _, _, fs, _, ok := lineGeometry(blk.LinesCoords[li], line, blk.Vertical); ok {
			sizes = append(sizes, fs)
		}
	}
	if len(sizes) < 3 {
		return 0
	}
	slices.Sort(sizes)
	return sizes[len(sizes)/2]
}

// writeLineDiv renders one OCR line as a transparent, positioned <div>.
// white-space:nowrap keeps it a single column/row. The whole line stays one
// <div> with one text run — pop-up dictionaries (Yomitan/10ten) walk the DOM to
// assemble multi-character words, so the line must never be split into per-glyph
// elements.
func writeLineDiv(b *strings.Builder, text string, coords [][]float64, vertical, ocr bool, blockRef float64) {
	text = strings.TrimSpace(text)
	n := utf8.RuneCountInString(text)
	if n == 0 || len(coords) == 0 {
		return
	}

	minX, minY := coords[0][0], coords[0][1]
	maxX, maxY := minX, minY
	for _, pt := range coords {
		if len(pt) < 2 {
			continue
		}
		minX, minY = min(minX, pt[0]), min(minY, pt[1])
		maxX, maxY = max(maxX, pt[0]), max(maxY, pt[1])
	}
	lw, lh := maxX-minX, maxY-minY
	if lw <= 0 || lh <= 0 {
		return
	}

	if startX, startY, fs, deg, ok := lineGeometry(coords, text, vertical); ocr && ok {
		// An outlier against the block's own size means the OCR miscounted this
		// line's characters; the block's median is the better bet.
		if blockRef > 0 && fs > ocrFontOutlier*blockRef {
			fs = blockRef
		}
		_, _, nudge := ocrPads(vertical)
		// The quad is wider/taller than the glyphs it holds, and the ink sits
		// near the middle of that slack — so the line is centred across its own
		// axis rather than pinned to the quad's edge, then nudged onto the ink.
		// It stays a single <div> with one text run: pop-up dictionaries walk the
		// DOM to assemble multi-character words, so a line must never be split.
		var style string
		if vertical {
			style = fmt.Sprintf(
				"position:absolute;left:%dpx;top:%dpx;width:%dpx;font-size:%.1fpx;line-height:1;white-space:nowrap;color:transparent;cursor:text;-webkit-user-select:text;user-select:text;writing-mode:vertical-rl;transform:rotate(%.2fdeg);transform-origin:50%% 0;",
				iround(startX-fs/2+nudge*fs), iround(startY), iround(fs), fs, deg,
			)
		} else {
			style = fmt.Sprintf(
				"position:absolute;left:%dpx;top:%dpx;font-size:%.1fpx;line-height:1;white-space:nowrap;color:transparent;cursor:text;-webkit-user-select:text;user-select:text;transform:rotate(%.2fdeg);transform-origin:0 50%%;",
				iround(startX), iround(startY-fs/2+nudge*fs), fs, deg,
			)
		}
		fmt.Fprintf(b, "    <div style=\"%s\">%s</div>\n", style, xmlEsc(text))
		return
	}

	// Plain placement: anchor at the quad's top-left, size = axis length / char
	// count. Used for horizontal lines and for pdftotext text-layer quads, which
	// are already tight to the glyphs so the OCR offsets above don't apply.
	var fs float64
	if vertical {
		fs = lh / float64(n)
	} else {
		fs = lw / float64(n)
	}
	if fs <= 0 {
		fs = 16
	}

	// No explicit width/height: white-space:nowrap + font-size derived from
	// axis-length/char-count reproduces the original extent without it.
	style := fmt.Sprintf(
		"position:absolute;left:%dpx;top:%dpx;font-size:%.1fpx;line-height:1;white-space:nowrap;color:transparent;cursor:text;-webkit-user-select:text;user-select:text;",
		iround(minX), iround(minY), fs,
	)
	if vertical {
		style += "writing-mode:vertical-rl;"
	}
	fmt.Fprintf(b, "    <div style=\"%s\">%s</div>\n", style, xmlEsc(text))
}

// writeBlockDiv is the fallback for blocks lacking per-line coordinates.
func writeBlockDiv(b *strings.Builder, text string, box [4]int, fontSize float64, vertical bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	bw, bh := box[2]-box[0], box[3]-box[1]
	if bw <= 0 || bh <= 0 {
		return
	}
	fs := fontSize
	if fs <= 0 {
		fs = 16
	}
	style := fmt.Sprintf(
		"position:absolute;left:%dpx;top:%dpx;width:%dpx;height:%dpx;font-size:%.1fpx;line-height:1;color:transparent;cursor:text;-webkit-user-select:text;user-select:text;",
		box[0], box[1], bw, bh, fs,
	)
	if vertical {
		style += "writing-mode:vertical-rl;"
	}
	fmt.Fprintf(b, "    <div style=\"%s\">%s</div>\n", style, xmlEsc(text))
}

func iround(f float64) int {
	if f < 0 {
		return int(f - 0.5)
	}
	return int(f + 0.5)
}

func extMediaType(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	default:
		return "image/jpeg"
	}
}

func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func addText(zw *zip.Writer, name, content string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, content)
	return err
}

func addFile(zw *zip.Writer, dest, src string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	w, err := zw.Create(dest)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
