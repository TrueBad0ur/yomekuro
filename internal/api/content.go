package api

import (
	"archive/zip"
	"container/list"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
	"github.com/truebad0ur/yomekuro/internal/epub"
)

type tocEntryDTO struct {
	Label      string        `json:"label"`
	Href       string        `json:"href,omitempty"`
	SpineIndex int           `json:"spine_index"`
	Children   []tocEntryDTO `json:"children,omitempty"`
}

func enrichTOC(entries []epub.TocEntry, spine []epub.SpineItem) []tocEntryDTO {
	result := make([]tocEntryDTO, 0, len(entries))
	for _, e := range entries {
		base := e.Href
		if idx := strings.LastIndex(base, "#"); idx >= 0 {
			base = base[:idx]
		}
		spineIdx := -1
		for i, s := range spine {
			if s.Href == base {
				spineIdx = i
				break
			}
		}
		dto := tocEntryDTO{Label: e.Label, Href: e.Href, SpineIndex: spineIdx}
		if len(e.Children) > 0 {
			dto.Children = enrichTOC(e.Children, spine)
		}
		result = append(result, dto)
	}
	return result
}

// ── LRU zip cache ─────────────────────────────────────────────────────────────

type zipCache struct {
	mu    sync.Mutex
	cap   int
	items map[string]*list.Element
	order *list.List
}

type cacheEntry struct {
	path string
	hash string
	rc   *zip.ReadCloser
}

func newZipCache(capacity int) *zipCache {
	return &zipCache{
		cap:   capacity,
		items: make(map[string]*list.Element),
		order: list.New(),
	}
}

// A cached *zip.ReadCloser, reopened if its hash changed (file rewritten in
// place). The cache owns it — callers must not close it.
func (c *zipCache) get(path, hash string) (*zip.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[path]; ok {
		entry := el.Value.(*cacheEntry)
		if entry.hash == hash {
			c.order.MoveToFront(el)
			return entry.rc, nil
		}
		// File changed on disk — drop the stale reader.
		entry.rc.Close()
		delete(c.items, path)
		c.order.Remove(el)
	}

	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}

	if c.order.Len() >= c.cap {
		back := c.order.Back()
		if back != nil {
			entry := back.Value.(*cacheEntry)
			entry.rc.Close()
			delete(c.items, entry.path)
			c.order.Remove(back)
		}
	}

	el := c.order.PushFront(&cacheEntry{path: path, hash: hash, rc: rc})
	c.items[path] = el
	return rc, nil
}

// No caller yet — the hook for a future graceful shutdown.
//
//nolint:unused
func (c *zipCache) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, el := range c.items {
		el.Value.(*cacheEntry).rc.Close()
	}
	c.items = make(map[string]*list.Element)
	c.order.Init()
}

// ── manifest ──────────────────────────────────────────────────────────────────

type manifestResponse struct {
	Title            string         `json:"title"`
	Format           string         `json:"format"`
	ReadingDirection string         `json:"reading_direction"`
	FixedLayout      bool           `json:"fixed_layout"`
	Spine            []spineItemDTO `json:"spine"`
	TOC              []tocEntryDTO  `json:"toc"`
}

type spineItemDTO struct {
	Href      string `json:"href"`
	MediaType string `json:"media_type"`
}

func (s *Server) getBookManifest(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	b, err := db.GetBookByID(r.Context(), s.db, id)
	if err == pgx.ErrNoRows {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if b.Format == "html" {
		respond(w, manifestResponse{
			Title:            b.Title,
			Format:           b.Format,
			ReadingDirection: b.ReadingDirection,
			FixedLayout:      false,
			Spine:            []spineItemDTO{{Href: b.Filename, MediaType: "text/html; charset=utf-8"}},
			TOC:              []tocEntryDTO{},
		})
		return
	}

	spine, _, fixedLayout, toc, err := epub.OpenManifest(b.Path)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "could not open epub")
		return
	}

	spineDTO := make([]spineItemDTO, len(spine))
	for i, item := range spine {
		spineDTO[i] = spineItemDTO{Href: item.Href, MediaType: item.MediaType}
	}
	respond(w, manifestResponse{
		Title:            b.Title,
		Format:           b.Format,
		ReadingDirection: b.ReadingDirection,
		FixedLayout:      fixedLayout,
		Spine:            spineDTO,
		TOC:              enrichTOC(toc, spine),
	})
}

// ── content ───────────────────────────────────────────────────────────────────

func (s *Server) getBookContent(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	b, err := db.GetBookByID(r.Context(), s.db, id)
	if err == pgx.ErrNoRows {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// chi wildcard captures the path after /content/
	entryPath := chi.URLParam(r, "*")
	entryPath = strings.TrimPrefix(entryPath, "/")
	if entryPath == "" {
		respondError(w, http.StatusBadRequest, "missing entry path")
		return
	}

	etag := fmt.Sprintf(`"%s-%s"`, b.FileHash, entryPath)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if b.Format == "html" {
		w.Header().Set("Content-Type", mimeByExt(entryPath))
		w.Header().Set("ETag", etag)
		// private: same reasoning as below — never let a shared proxy cache this.
		w.Header().Set("Cache-Control", "private, no-cache")
		http.ServeFile(w, r, b.Path)
		return
	}

	zr, err := s.zips.get(b.Path, b.FileHash)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "could not open epub")
		return
	}

	// Find and stream the zip entry.
	var found *zip.File
	for _, f := range zr.File {
		if f.Name == entryPath {
			found = f
			break
		}
	}
	if found == nil {
		respondError(w, http.StatusNotFound, "entry not found")
		return
	}

	rc, err := found.Open()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "could not read entry")
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Type", mimeByExt(entryPath))
	w.Header().Set("ETag", etag)
	// no-cache = always revalidate via ETag. Content can change on re-convert;
	// the hash-based ETag makes revalidation cheap (304) while never serving stale
	// pages. private: this is a specific user's book content — a shared proxy/CDN
	// must never cache and replay it to a different (possibly unauthenticated)
	// requester behind the same cache.
	w.Header().Set("Cache-Control", "private, no-cache")
	io.Copy(w, rc)
}

func mimeByExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xhtml":
		return "application/xhtml+xml; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ncx":
		return "application/x-dtbncx+xml"
	default:
		return "application/octet-stream"
	}
}
