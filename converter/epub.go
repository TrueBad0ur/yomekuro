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

	shared "github.com/truebad0ur/yomekuro-shared"
)

type imgEntry struct {
	id        string
	href      string
	mediaType string
}

// Trailing "v02"/"Vol 3"/"4", or a Japanese "（05）" — matched halfwidth-
// normalized, since real tankobon filenames use fullwidth digits and parens.
var reVolSuffix = regexp.MustCompile(`(?i)(\s+(v\.?\s*|vol\.?\s*|volume\s*)?\d+|\(\s*\d+\s*\))\s*$`)

// Strips a trailing volume number: "Dungeon Meshi v01" → "Dungeon Meshi",
// "葬送のフリーレン（０５）" → "葬送のフリーレン".
func seriesName(volumeName string) string {
	s := reVolSuffix.ReplaceAllString(strings.TrimSpace(shared.ToHalfwidth(volumeName)), "")
	s = strings.TrimSpace(s)
	if s == "" {
		return volumeName
	}
	return s
}

// volumeIndex extracts a numeric index from a volume name, e.g. "Dungeon Meshi v02" → 2.
func volumeIndex(name string) float64 {
	matches := reVolSuffix.FindString(shared.ToHalfwidth(name))
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

// A leading number: "1 Kage no koibito" → 1. For anthologies, where the number
// comes first and the rest of the name is an unrelated per-item title.
func leadingVolumeIndex(name string) (float64, bool) {
	m := reLeadingNum.FindStringSubmatch(shared.ToHalfwidth(name))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n == 0 {
		return 0, false
	}
	return float64(n), true
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
		// Zip entry name must stay the raw filename — only the OPF/XHTML href
		// reference needs URL-escaping, or a space (etc.) in the source
		// filename makes the zip entry and the decoded href disagree, and
		// nothing (including cover extraction) can find the file again.
		destName := "images/" + filepath.Base(page.ImgPath)
		destHref := "images/" + url.PathEscape(filepath.Base(page.ImgPath))

		if err := addFile(zw, "OPS/"+destName, imgSrc); err != nil {
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

// volumeDirection infers RTL/LTR from mokuro's per-block text orientation,
// counting characters (not blocks) so one caption doesn't flip the volume.
func volumeDirection(vol MokuroVolume) string {
	var vertChars, horizChars int
	for _, page := range vol.Pages {
		for _, blk := range page.Blocks {
			n := 0
			for _, line := range blk.Lines {
				n += utf8.RuneCountInString(line)
			}
			if blk.Vertical {
				vertChars += n
			} else {
				horizChars += n
			}
		}
	}
	if horizChars > vertChars {
		return "ltr"
	}
	return "rtl"
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
	fmt.Fprintf(&b, `  </manifest>
  <spine page-progression-direction="%s">
`, volumeDirection(vol))
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
				if ocr && isDuplicateBlockLine(blk, li) {
					continue
				}
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

// advanceEm is a string's width at font-size 1: fullwidth glyphs ~1em,
// halfwidth/ASCII ~0.55em. A raw rune count would skew mixed-script lines.
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

// Where the ink sits inside a mokuro quad, measured against real pages (300+
// lines per orientation). OCR volumes only — pdftotext quads are already tight.
const (
	ocrVertStartPad, ocrVertCrossNudge = 0.21, 0.05
	ocrHorStartPad, ocrHorCrossNudge   = 0.09, 0.00

	// A line whose own fitted size exceeds its block's median by more than this
	// is trusted no further than the median — see blockRefFontSize.
	ocrFontOutlier = 1.15

	// Quad lean beyond this is detector noise, not real printed tilt: genuine
	// scan skew is a couple degrees at most, and dialogue text is never drawn
	// at a visible angle (unlike hand-lettered SFX, which mokuro doesn't quad
	// as selectable text anyway). Measured against a real false-positive: a
	// plain 9-character vertical line came out at 7.7° from pure quad wobble.
	ocrMaxLeanDeg = 4.0

	// A line whose quad covers this much of its block's own box is a
	// *candidate* whole-block re-detection — but box coverage alone isn't
	// enough to act on (see ocrDuplicateFsRatio below): a block with just a
	// main line plus short furigana/reading-aid lines legitimately has its
	// main line covering most of the box too, and that's normal print, not a
	// bug. Real case this targets: the spurious line's box was 96.7% of the
	// block's area; its two real sibling lines were 1% and 18%.
	ocrDuplicateLineBoxRatio = 0.8

	// The box-ratio candidate above is only actually dropped if its own
	// fitted font size is also this far below its siblings' — the real
	// tell that it's cramming an OCR-merged multi-line read into one box,
	// not just a normal main line next to small furigana. Two real
	// false-positives this guards against both had completely ordinary font
	// sizes (~20-23px, matching the book's usual range) despite covering
	// 82-92% of their block's box; the one confirmed real spurious line had
	// fs 8.6px against sibling lines at 11-22px — well under half.
	ocrDuplicateFsRatio = 0.6
)

// Em cells a quad spans beyond the text's advance (so, negative): a quad wraps
// ink, and punctuation inks only part of its cell (。 a third, 「 a half).
func ocrSpanSlack(text string, vertical bool) float64 {
	r := []rune(text)
	if len(r) == 0 {
		return 0
	}
	tail := strings.ContainsRune("。、．，」』）】〉》！？", r[len(r)-1])
	head := strings.ContainsRune("「『（【〈《", r[0])
	switch {
	case vertical && head && tail:
		return -1.15
	case vertical && head:
		return -0.94
	case vertical && tail:
		return -0.55
	case vertical:
		return +0.10
	case head && tail:
		return -0.72
	case head:
		return -0.73
	case tail:
		return -0.60
	default:
		return -0.28
	}
}

func ocrPads(vertical bool) (start, nudge float64) {
	if vertical {
		return ocrVertStartPad, ocrVertCrossNudge
	}
	return ocrHorStartPad, ocrHorCrossNudge
}

// Resolves a quad into its text's centre line: start, size and lean. Quads are
// often parallelograms, so the axis comes from the two short edges' midpoints.
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
	startPad, _ := ocrPads(vertical)
	fs = main / (adv + ocrSpanSlack(strings.TrimSpace(text), vertical))
	if fs <= 0 {
		return 0, 0, 0, 0, false
	}
	// Text starts one start-pad along the line's own axis.
	startX = ax + startPad*fs*(bx-ax)/main
	startY = ay + startPad*fs*(by-ay)/main

	// Clockwise lean matching the quad: a column's sideways drift, a row's drop.
	var sin float64
	if vertical {
		sin = (ax - bx) / main
	} else {
		sin = (by - ay) / main
	}
	deg = math.Asin(math.Max(-1, math.Min(1, sin))) * 180 / math.Pi
	deg = math.Max(-ocrMaxLeanDeg, math.Min(ocrMaxLeanDeg, deg))
	return startX, startY, fs, deg, true
}

// True if line blk.Lines[idx] is mokuro re-detecting the whole block as an
// extra "line" on top of the real per-column lines it already found, and
// OCRing it into a garbled duplicate of their text — a spurious line that
// must be dropped, not rendered: correcting its font size would still
// overlay wrong, overlapping text on the real lines.
//
// Requires both: the line's quad covers most of the block's own box (a
// necessary but NOT sufficient signal — see ocrDuplicateFsRatio's comment),
// and its own fitted font size is well below its siblings' median. Box
// coverage alone false-positived twice on ordinary main-line-plus-furigana
// blocks; box coverage AND undersized font correctly separates those from
// the one confirmed real case. Requires at least 3 lines in the block: a
// 2-line block's main line legitimately covering most of the box is normal,
// and there aren't enough siblings left to form a reliable size reference.
func isDuplicateBlockLine(blk MokuroBlock, idx int) bool {
	if len(blk.Lines) < 3 || idx >= len(blk.LinesCoords) {
		return false
	}
	coords := blk.LinesCoords[idx]
	if len(coords) == 0 {
		return false
	}
	boxW, boxH := blk.Box[2]-blk.Box[0], blk.Box[3]-blk.Box[1]
	if boxW <= 0 || boxH <= 0 {
		return false
	}
	minX, minY := coords[0][0], coords[0][1]
	maxX, maxY := minX, minY
	for _, pt := range coords {
		if len(pt) < 2 {
			continue
		}
		minX, minY = math.Min(minX, pt[0]), math.Min(minY, pt[1])
		maxX, maxY = math.Max(maxX, pt[0]), math.Max(maxY, pt[1])
	}
	lineArea := (maxX - minX) * (maxY - minY)
	boxArea := float64(boxW * boxH)
	if boxArea <= 0 || lineArea/boxArea < ocrDuplicateLineBoxRatio {
		return false
	}

	localRef, hasRef := siblingMedianFs(blk, idx)
	if !hasRef {
		return false
	}
	_, _, myFs, _, ok := lineGeometry(coords, blk.Lines[idx], blk.Vertical)
	return ok && myFs < ocrDuplicateFsRatio*localRef
}

// Median fitted font size of every OTHER line in the block (excluding idx
// itself), or false if none. Unlike blockRefFontSize, this asks for only one
// sibling, not three — used as a fallback for exactly the blocks
// blockRefFontSize refuses to judge (too few substantial lines for a
// trustworthy median), where a single sibling is still a far better estimate
// than none. Real case: a 2-line block (a 3rd, short furigana line doesn't
// qualify) where the outlier's own quad undershot its real font size —
// blockRefFontSize returned 0 (needs 3+), so the correction in writeLineDiv
// never fired and the line rendered at half its sibling's size.
func siblingMedianFs(blk MokuroBlock, idx int) (float64, bool) {
	var sibFs []float64
	for i, text := range blk.Lines {
		if i == idx {
			continue
		}
		// Same filter as blockRefFontSize: a short line (furigana, a stray
		// "." or interjection) is normally printed smaller than body text —
		// trusting it as a size reference would corrupt the estimate.
		if utf8.RuneCountInString(strings.TrimSpace(text)) < 6 {
			continue
		}
		if _, _, fs, _, ok := lineGeometry(blk.LinesCoords[i], text, blk.Vertical); ok {
			sibFs = append(sibFs, fs)
		}
	}
	if len(sibFs) == 0 {
		return 0, false
	}
	slices.Sort(sibFs)
	return sibFs[len(sibFs)/2], true
}

// Median fitted size of a block's lines (print uses one size per block), or 0 if
// too few. Corrects lines whose OCR miscounted characters and so came out
// oversized or undersized (see writeLineDiv).
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
		if isDuplicateBlockLine(blk, li) {
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

// One OCR line as a transparent positioned <div>. It must stay a single <div>
// with one text run: pop-up dictionaries walk the DOM to assemble words.
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
		// line's characters; the block's median is the better bet. Checked both
		// directions: too few characters detected makes fs too big, too many
		// (e.g. the detector's quad undershooting a line's real ink) makes it
		// too small — a real case sampled at fs 13% of its block's median.
		if blockRef > 0 && fs > ocrFontOutlier*blockRef {
			fs = blockRef
		} else if blockRef > 0 && fs < blockRef/ocrFontOutlier {
			fs = blockRef
		}
		_, nudge := ocrPads(vertical)
		// The quad has slack around its glyphs and the ink sits mid-slack, so the
		// line is centred across its axis rather than pinned to the quad's edge.
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

	// Plain placement for pdftotext text-layer quads, which are already tight to
	// the glyphs, so the OCR offsets above don't apply.
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
