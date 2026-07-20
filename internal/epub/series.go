package epub

import (
	"archive/zip"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/beevik/etree"
	shared "github.com/truebad0ur/yomekuro-shared"
)

// Series name and index, in order: EPUB3 belongs-to-collection, Calibre's EPUB2
// meta tags, then the parent directory name.
func parseSeries(doc *etree.Document, filePath, libraryPath string) (name string, index float64) {
	// Strategy 1: EPUB3 belongs-to-collection
	for _, el := range doc.FindElements("//metadata/meta") {
		if attrVal(el, "property") != "belongs-to-collection" {
			continue
		}
		name = strings.TrimSpace(el.Text())
		id := attrVal(el, "id")
		if id != "" {
			for _, refine := range doc.FindElements("//metadata/meta") {
				if attrVal(refine, "refines") == "#"+id && attrVal(refine, "property") == "group-position" {
					index, _ = strconv.ParseFloat(strings.TrimSpace(refine.Text()), 64)
				}
			}
		}
		if name != "" {
			return
		}
	}

	// Strategy 2: Calibre EPUB2 meta tags
	var calibreName, calibreIndex string
	for _, el := range doc.FindElements("//metadata/meta") {
		switch attrVal(el, "name") {
		case "calibre:series":
			calibreName = strings.TrimSpace(attrVal(el, "content"))
		case "calibre:series_index":
			calibreIndex = strings.TrimSpace(attrVal(el, "content"))
		}
	}
	if calibreName != "" {
		name = calibreName
		if calibreIndex != "" {
			index, _ = strconv.ParseFloat(calibreIndex, 64)
		}
		return
	}

	// Strategy 3: parent directory name relative to library root
	dir := filepath.Dir(filePath)
	if libraryPath != "" {
		rel, err := filepath.Rel(libraryPath, dir)
		if err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			parts := strings.Split(rel, string(filepath.Separator))
			name = parts[len(parts)-1]
		}
	} else {
		name = filepath.Base(dir)
	}
	index = indexFromFilename(filepath.Base(filePath))
	return
}

// The last number in a filename, as series index. Japanese releases often use
// fullwidth digits ("（１２）"), which \d alone silently misses — shared.LastNumber
// normalizes those first (see its doc comment for why this lives there).
func indexFromFilename(filename string) float64 {
	ext := filepath.Ext(filename)
	f, _ := shared.LastNumber(strings.TrimSuffix(filename, ext))
	return f
}

// computePageCount uses the Readium method: 1 page per 1024 bytes of compressed spine content.
func computePageCount(zr *zip.Reader, spine []SpineItem) int {
	hrefs := make(map[string]struct{}, len(spine))
	for _, s := range spine {
		hrefs[s.Href] = struct{}{}
	}
	total := 0
	for _, f := range zr.File {
		if _, ok := hrefs[f.Name]; ok {
			pages := int(math.Ceil(float64(f.CompressedSize64) / 1024.0))
			if pages < 1 {
				pages = 1
			}
			total += pages
		}
	}
	if total < 1 {
		total = 1
	}
	return total
}
