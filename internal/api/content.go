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

// ── LRU zip cache ─────────────────────────────────────────────────────────────

type zipCache struct {
	mu    sync.Mutex
	cap   int
	items map[string]*list.Element
	order *list.List
}

type cacheEntry struct {
	path string
	rc   *zip.ReadCloser
}

func newZipCache(capacity int) *zipCache {
	return &zipCache{
		cap:   capacity,
		items: make(map[string]*list.Element),
		order: list.New(),
	}
}

// get returns a cached *zip.ReadCloser, opening it if necessary.
// The caller must NOT close the returned ReadCloser; the cache owns it.
func (c *zipCache) get(path string) (*zip.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[path]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*cacheEntry).rc, nil
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

	el := c.order.PushFront(&cacheEntry{path: path, rc: rc})
	c.items[path] = el
	return rc, nil
}

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
	ReadingDirection string         `json:"reading_direction"`
	Spine            []spineItemDTO `json:"spine"`
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

	book, err := epub.Open(b.Path, "")
	if err != nil {
		respondError(w, http.StatusInternalServerError, "could not open epub")
		return
	}

	spine := make([]spineItemDTO, len(book.Spine))
	for i, item := range book.Spine {
		spine[i] = spineItemDTO{Href: item.Href, MediaType: item.MediaType}
	}
	respond(w, manifestResponse{
		Title:            b.Title,
		ReadingDirection: b.ReadingDirection,
		Spine:            spine,
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

	zr, err := s.zips.get(b.Path)
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
	w.Header().Set("Cache-Control", "max-age=3600")
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
