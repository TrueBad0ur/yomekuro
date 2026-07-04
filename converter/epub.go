package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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

var reVolSuffix = regexp.MustCompile(`(?i)\s+(v\.?\s*|vol\.?\s*|volume\s*)?\d+$`)

// seriesName strips trailing volume number from a volume name.
// "Dungeon Meshi v01" → "Dungeon Meshi", "SAO Vol 3" → "SAO"
func seriesName(volumeName string) string {
	s := reVolSuffix.ReplaceAllString(strings.TrimSpace(volumeName), "")
	s = strings.TrimSpace(s)
	if s == "" {
		return volumeName
	}
	return s
}

// volumeIndex extracts a numeric index from a volume name, e.g. "Dungeon Meshi v02" → 2.
func volumeIndex(name string) float64 {
	matches := reVolSuffix.FindString(name)
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
		xhtml := pageXHTML(i+1, page, imgHref)
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
	series := seriesName(vol.Volume)
	idx := volumeIndex(vol.Volume)

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

func pageXHTML(num int, page MokuroPage, imgHref string) string {
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
			for li, line := range blk.Lines {
				writeLineDiv(&b, line, blk.LinesCoords[li], blk.Vertical)
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

// writeLineDiv renders one OCR line as a transparent, positioned <div>.
// font-size is derived from the line's own bounding box so N characters fill the
// reading axis exactly; white-space:nowrap prevents CSS from re-wrapping columns.
func writeLineDiv(b *strings.Builder, text string, coords [][]float64, vertical bool) {
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

	// Distribute characters along the reading axis (vertical: height, horizontal: width).
	var fs float64
	if vertical {
		fs = lh / float64(n)
	} else {
		fs = lw / float64(n)
	}
	if fs <= 0 {
		fs = 16
	}

	// No width/height: a fixed cross-axis size is what lets the browser wrap the
	// column/row when sub-pixel rounding makes content 1px too big. With the size
	// left to auto + white-space:nowrap, the text box hugs its single line and can
	// never break into a second column. Position comes from left/top; font-size
	// (= axis length / char count) reproduces the original extent.
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
