package epub

import (
	"archive/zip"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/beevik/etree"
	"golang.org/x/text/language"
)

// findOPFPath reads META-INF/container.xml and returns the OPF file path.
func findOPFPath(zr *zip.Reader) (string, error) {
	data, err := readZipEntry(zr, "META-INF/container.xml")
	if err != nil {
		return "", fmt.Errorf("epub: read container.xml: %w", err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(data); err != nil {
		return "", fmt.Errorf("epub: parse container.xml: %w", err)
	}
	el := doc.FindElement("//rootfile")
	if el == nil {
		return "", fmt.Errorf("epub: no rootfile in container.xml")
	}
	p := attrVal(el, "full-path")
	if p == "" {
		return "", fmt.Errorf("epub: rootfile has no full-path")
	}
	return p, nil
}

// parseOPFDoc reads and parses the OPF file into an etree Document.
func parseOPFDoc(zr *zip.Reader, opfPath string) (*etree.Document, error) {
	data, err := readZipEntry(zr, opfPath)
	if err != nil {
		return nil, fmt.Errorf("epub: read OPF: %w", err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(data); err != nil {
		return nil, fmt.Errorf("epub: parse OPF: %w", err)
	}
	return doc, nil
}

// normalizeHref resolves a manifest href against opfDir using zip-style paths.
func normalizeHref(opfDir, href string) string {
	anchor := ""
	base := href
	if idx := strings.LastIndex(href, "#"); idx >= 0 {
		anchor = href[idx+1:]
		base = href[:idx]
	}
	resolved := path.Clean(path.Join(opfDir, base))
	if anchor != "" {
		return resolved + "#" + anchor
	}
	return resolved
}

// buildManifest parses <manifest> items into a map keyed by id.
func buildManifest(doc *etree.Document, opfDir string) map[string]ManifestItem {
	manifest := make(map[string]ManifestItem)
	for _, el := range doc.FindElements("//manifest/item") {
		id := attrVal(el, "id")
		if id == "" {
			continue
		}
		rawHref := attrVal(el, "href")
		decoded, err := url.PathUnescape(rawHref)
		if err != nil {
			decoded = rawHref
		}
		manifest[id] = ManifestItem{
			ID:         id,
			Href:       normalizeHref(opfDir, decoded),
			MediaType:  attrVal(el, "media-type"),
			Properties: strings.Fields(attrVal(el, "properties")),
		}
	}
	return manifest
}

// buildSpine returns ordered spine items and page-progression-direction.
func buildSpine(doc *etree.Document, manifest map[string]ManifestItem) ([]SpineItem, string) {
	direction := ""
	if spineEl := doc.FindElement("//spine"); spineEl != nil {
		direction = attrVal(spineEl, "page-progression-direction")
	}
	var spine []SpineItem
	for _, ref := range doc.FindElements("//spine/itemref") {
		idref := attrVal(ref, "idref")
		if item, ok := manifest[idref]; ok {
			spine = append(spine, SpineItem{
				Href:      item.Href,
				MediaType: item.MediaType,
			})
		}
	}
	return spine, direction
}

type bookMetadata struct {
	title       string
	authors     []Author
	language    string
	publisher   string
	publishedAt *time.Time
	description string
	isbn        string
	tags        []string
}

var relators = map[string]string{
	"aut": "writer",
	"clr": "colorist",
	"cov": "cover",
	"edt": "editor",
	"art": "penciller",
	"ill": "penciller",
	"trl": "translator",
}

// parseMetadata extracts Dublin Core and OPF metadata from an OPF document.
func parseMetadata(doc *etree.Document) bookMetadata {
	var m bookMetadata

	m.title = strings.TrimSpace(textOf(doc.FindElement("//metadata/title")))

	m.description = stripHTML(strings.TrimSpace(textOf(doc.FindElement("//metadata/description"))))

	if dateEl := doc.FindElement("//metadata/date"); dateEl != nil {
		m.publishedAt = parseDate(strings.TrimSpace(dateEl.Text()))
	}

	m.publisher = strings.TrimSpace(textOf(doc.FindElement("//metadata/publisher")))

	m.language = normalizeLanguage(strings.TrimSpace(textOf(doc.FindElement("//metadata/language"))))

	// Build refines map: element id → marc:relators role code
	refineRoles := make(map[string]string)
	for _, el := range doc.FindElements("//metadata/meta") {
		if attrVal(el, "property") == "role" && attrVal(el, "scheme") == "marc:relators" {
			id := strings.TrimPrefix(attrVal(el, "refines"), "#")
			if id != "" {
				refineRoles[id] = strings.TrimSpace(el.Text())
			}
		}
	}

	for _, el := range doc.FindElements("//metadata/creator") {
		name := strings.TrimSpace(el.Text())
		if name == "" {
			continue
		}
		roleCode := attrVal(el, "role") // opf:role or role
		if roleCode == "" {
			id := attrVal(el, "id")
			roleCode = refineRoles[id]
		}
		role := relators[roleCode]
		if role == "" {
			role = "writer"
		}
		m.authors = append(m.authors, Author{Name: name, Role: role})
	}

	// ISBN from dc:identifier
	for _, el := range doc.FindElements("//metadata/identifier") {
		candidate := strings.ToLower(strings.TrimSpace(el.Text()))
		candidate = strings.TrimPrefix(candidate, "isbn:")
		candidate = strings.ReplaceAll(candidate, "-", "")
		candidate = strings.ReplaceAll(candidate, " ", "")
		if v := validateISBN(candidate); v != "" {
			m.isbn = v
			break
		}
	}

	// Tags from dc:subject
	for _, el := range doc.FindElements("//metadata/subject") {
		t := strings.TrimSpace(el.Text())
		if t != "" {
			m.tags = append(m.tags, t)
		}
	}

	return m
}

// ── helpers ──────────────────────────────────────────────────────────────────

func attrVal(el *etree.Element, name string) string {
	local := name
	if i := strings.IndexByte(name, ':'); i >= 0 {
		local = name[i+1:]
	}
	for _, a := range el.Attr {
		if a.Key == local {
			return a.Value
		}
	}
	return ""
}

func textOf(el *etree.Element) string {
	if el == nil {
		return ""
	}
	return el.Text()
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

var dateFormats = []string{
	"2006-01-02",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05",
	"2006",
}

func parseDate(s string) *time.Time {
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func normalizeLanguage(lang string) string {
	if lang == "" {
		return ""
	}
	tag, err := language.Parse(lang)
	if err != nil {
		return lang
	}
	return tag.String()
}

var leadingArticles = []string{"The ", "A ", "An "}

func computeSortTitle(title string) string {
	for _, a := range leadingArticles {
		if strings.HasPrefix(title, a) {
			return title[len(a):]
		}
	}
	return title
}

func validateISBN(s string) string {
	s = strings.ToUpper(s)
	switch len(s) {
	case 13:
		if isValidISBN13(s) {
			return s
		}
	case 10:
		if isValidISBN10(s) {
			return s
		}
	}
	return ""
}

func isValidISBN13(s string) bool {
	sum := 0
	for i := 0; i < 12; i++ {
		d := int(s[i] - '0')
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		if i%2 == 0 {
			sum += d
		} else {
			sum += d * 3
		}
	}
	check := (10 - sum%10) % 10
	return s[12] == byte('0'+check)
}

func isValidISBN10(s string) bool {
	sum := 0
	for i := 0; i < 9; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
		sum += int(s[i]-'0') * (10 - i)
	}
	last := s[9]
	if last == 'X' {
		sum += 10
	} else if last >= '0' && last <= '9' {
		sum += int(last - '0')
	} else {
		return false
	}
	return sum%11 == 0
}
