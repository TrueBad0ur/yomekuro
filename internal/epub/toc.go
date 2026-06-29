package epub

import (
	"archive/zip"
	"net/url"
	"strings"

	"github.com/beevik/etree"
)

// TocEntry is one node in the table of contents tree.
type TocEntry struct {
	Label    string     `json:"label"`
	Href     string     `json:"href,omitempty"`
	Children []TocEntry `json:"children,omitempty"`
}

var possibleNCXIDs = map[string]bool{"toc": true, "ncx": true, "ncxtoc": true}

// GetTOC extracts the TOC from an open EPUB zip.
// Tries EPUB3 nav first, then EPUB2 NCX.
func GetTOC(zr *zip.Reader, manifest map[string]ManifestItem) []TocEntry {
	// EPUB3: manifest item with properties containing "nav"
	for _, item := range manifest {
		for _, prop := range item.Properties {
			if prop == "nav" {
				data, err := readZipEntry(zr, item.Href)
				if err != nil {
					break
				}
				navDir := ""
				if idx := strings.LastIndex(item.Href, "/"); idx >= 0 {
					navDir = item.Href[:idx]
				}
				if toc := parseNavTOC(data, navDir); len(toc) > 0 {
					return toc
				}
				break
			}
		}
	}
	// EPUB2: NCX by media-type or well-known id
	for _, item := range manifest {
		if item.MediaType == "application/x-dtbncx+xml" || possibleNCXIDs[item.ID] {
			data, err := readZipEntry(zr, item.Href)
			if err != nil {
				continue
			}
			ncxDir := ""
			if idx := strings.LastIndex(item.Href, "/"); idx >= 0 {
				ncxDir = item.Href[:idx]
			}
			if toc := parseNcxTOC(data, ncxDir); len(toc) > 0 {
				return toc
			}
		}
	}
	return nil
}

// parseNavTOC parses an EPUB3 XHTML nav document.
func parseNavTOC(data []byte, navDir string) []TocEntry {
	doc := etree.NewDocument()
	if doc.ReadFromBytes(data) != nil {
		return nil
	}
	// Find <nav epub:type="toc"> — match by local attr name "type" to be namespace-agnostic
	var navEl *etree.Element
	for _, el := range doc.FindElements("//nav") {
		for _, a := range el.Attr {
			if a.Key == "type" && a.Value == "toc" {
				navEl = el
				break
			}
		}
		if navEl != nil {
			break
		}
	}
	if navEl == nil {
		return nil
	}
	// First direct <ol> child
	var olEl *etree.Element
	for _, ch := range navEl.ChildElements() {
		if ch.Tag == "ol" {
			olEl = ch
			break
		}
	}
	if olEl == nil {
		return nil
	}
	var entries []TocEntry
	for _, li := range olEl.ChildElements() {
		if li.Tag == "li" {
			if e := navLiToEntry(li, navDir); e != nil {
				entries = append(entries, *e)
			}
		}
	}
	return entries
}

func navLiToEntry(li *etree.Element, navDir string) *TocEntry {
	var title, href string
	for _, ch := range li.ChildElements() {
		if ch.Tag == "a" || ch.Tag == "span" {
			title = strings.TrimSpace(ch.Text())
			if ch.Tag == "a" {
				raw := ch.SelectAttrValue("href", "")
				if raw != "" {
					decoded, err := url.PathUnescape(raw)
					if err != nil {
						decoded = raw
					}
					href = normalizeHref(navDir, decoded)
				}
			}
			break
		}
	}
	if title == "" {
		return nil
	}
	var children []TocEntry
	for _, ch := range li.ChildElements() {
		if ch.Tag == "ol" {
			for _, liChild := range ch.ChildElements() {
				if liChild.Tag == "li" {
					if e := navLiToEntry(liChild, navDir); e != nil {
						children = append(children, *e)
					}
				}
			}
			break
		}
	}
	return &TocEntry{Label: title, Href: href, Children: children}
}

// parseNcxTOC parses an EPUB2 NCX document.
func parseNcxTOC(data []byte, ncxDir string) []TocEntry {
	doc := etree.NewDocument()
	if doc.ReadFromBytes(data) != nil {
		return nil
	}
	navMap := doc.FindElement("//navMap")
	if navMap == nil {
		return nil
	}
	var entries []TocEntry
	for _, el := range navMap.ChildElements() {
		if el.Tag == "navPoint" {
			if e := ncxNavPointToEntry(el, ncxDir); e != nil {
				entries = append(entries, *e)
			}
		}
	}
	return entries
}

func ncxNavPointToEntry(el *etree.Element, ncxDir string) *TocEntry {
	textEl := el.FindElement("navLabel/text")
	if textEl == nil {
		return nil
	}
	title := strings.TrimSpace(textEl.Text())
	if title == "" {
		return nil
	}
	var href string
	if contentEl := el.FindElement("content"); contentEl != nil {
		raw := contentEl.SelectAttrValue("src", "")
		if raw != "" {
			decoded, err := url.PathUnescape(raw)
			if err != nil {
				decoded = raw
			}
			href = normalizeHref(ncxDir, decoded)
		}
	}
	var children []TocEntry
	for _, ch := range el.ChildElements() {
		if ch.Tag == "navPoint" {
			if e := ncxNavPointToEntry(ch, ncxDir); e != nil {
				children = append(children, *e)
			}
		}
	}
	return &TocEntry{Label: title, Href: href, Children: children}
}
