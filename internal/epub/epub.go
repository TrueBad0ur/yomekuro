package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type Book struct {
	Path     string
	FileSize int64

	Title            string
	SortTitle        string
	Authors          []Author
	Language         string
	Publisher        string
	PublishedAt      *time.Time
	Description      string
	ISBN             string
	SeriesName       string
	SeriesIndex      float64
	PageCount        int
	ReadingDirection string // "ltr" | "rtl" | ""
	Tags             []string

	CoverData      []byte
	CoverMediaType string

	Spine    []SpineItem
	Manifest map[string]ManifestItem // keyed by id
}

type Author struct {
	Name string
	Role string
}

type ManifestItem struct {
	ID         string
	Href       string // normalized path within zip (always forward slashes)
	MediaType  string
	Properties []string
}

type SpineItem struct {
	Href      string
	MediaType string
}

// Parses an EPUB into a Book. libraryPath drives the series-name fallback
// (parent-directory heuristic); pass "" to skip it.
func Open(filePath, libraryPath string) (*Book, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, fmt.Errorf("epub: open zip: %w", err)
	}
	defer zr.Close()

	opfPath, err := findOPFPath(&zr.Reader)
	if err != nil {
		return nil, err
	}

	opfDir := ""
	if idx := strings.LastIndex(opfPath, "/"); idx >= 0 {
		opfDir = opfPath[:idx]
	}

	doc, err := parseOPFDoc(&zr.Reader, opfPath)
	if err != nil {
		return nil, err
	}

	manifest := buildManifest(doc, opfDir)
	spine, direction := buildSpine(doc, manifest)
	meta := parseMetadata(doc)
	seriesName, seriesIndex := parseSeries(doc, filePath, libraryPath)
	coverData, coverMT := extractCover(&zr.Reader, doc, manifest)
	pageCount := computePageCount(&zr.Reader, spine)

	return &Book{
		Path:             filePath,
		FileSize:         fi.Size(),
		Title:            meta.title,
		SortTitle:        computeSortTitle(meta.title),
		Authors:          meta.authors,
		Language:         meta.language,
		Publisher:        meta.publisher,
		PublishedAt:      meta.publishedAt,
		Description:      meta.description,
		ISBN:             meta.isbn,
		SeriesName:       seriesName,
		SeriesIndex:      seriesIndex,
		PageCount:        pageCount,
		ReadingDirection: direction,
		Tags:             meta.tags,
		CoverData:        coverData,
		CoverMediaType:   coverMT,
		Spine:            spine,
		Manifest:         manifest,
	}, nil
}

// OpenManifest returns spine, reading direction, fixed-layout flag, and TOC
// without extracting covers or full metadata.
func OpenManifest(filePath string) ([]SpineItem, string, bool, []TocEntry, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, "", false, nil, err
	}
	defer zr.Close()

	opfPath, err := findOPFPath(&zr.Reader)
	if err != nil {
		return nil, "", false, nil, err
	}
	opfDir := ""
	if idx := strings.LastIndex(opfPath, "/"); idx >= 0 {
		opfDir = opfPath[:idx]
	}
	doc, err := parseOPFDoc(&zr.Reader, opfPath)
	if err != nil {
		return nil, "", false, nil, err
	}
	manifest := buildManifest(doc, opfDir)
	spine, direction := buildSpine(doc, manifest)
	fixedLayout := detectFixedLayout(doc)
	toc := GetTOC(&zr.Reader, manifest)
	return spine, direction, fixedLayout, toc, nil
}

// ReadZipEntry opens the epub at filePath and reads a single named entry.
// Used for one-off reads; for serving many entries use an open *zip.ReadCloser.
func ReadZipEntry(filePath, entryName string) ([]byte, error) {
	zr, err := zip.OpenReader(filePath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return readZipEntry(&zr.Reader, entryName)
}

func readZipEntry(zr *zip.Reader, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("epub: entry not found: %s", name)
}
