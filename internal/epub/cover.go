package epub

import (
	"archive/zip"
	"net/url"

	"github.com/beevik/etree"
)

// extractCover implements Komga's 3-level cover detection:
//  1. EPUB3: manifest item with properties containing "cover-image"
//  2. EPUB2: <meta name="cover" content="id">
//  3. Fallback: manifest item with id == "cover-image"
func extractCover(zr *zip.Reader, doc *etree.Document, manifest map[string]ManifestItem) ([]byte, string) {
	item, ok := findCoverItem(doc, manifest)
	if !ok {
		return nil, ""
	}
	data, err := readZipEntry(zr, item.Href)
	if err != nil {
		return nil, ""
	}
	return data, item.MediaType
}

func findCoverItem(doc *etree.Document, manifest map[string]ManifestItem) (ManifestItem, bool) {
	// Level 1: EPUB3 — properties contains "cover-image"
	for _, item := range manifest {
		for _, p := range item.Properties {
			if p == "cover-image" {
				return item, true
			}
		}
	}

	// Level 2: EPUB2 — <meta name="cover" content="id">
	for _, el := range doc.FindElements("//metadata/meta") {
		if attrVal(el, "name") == "cover" {
			content := attrVal(el, "content")
			if content == "" {
				continue
			}
			decoded, err := url.PathUnescape(content)
			if err != nil {
				decoded = content
			}
			if item, ok := manifest[decoded]; ok {
				return item, true
			}
		}
	}

	// Level 3: manifest item with id == "cover-image"
	if item, ok := manifest["cover-image"]; ok {
		return item, true
	}

	return ManifestItem{}, false
}
