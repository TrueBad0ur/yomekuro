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

// cacheEntry is refcounted: LRU eviction or hash-invalidation only marks it
// evicted rather than closing it outright, since a concurrent reader may
// still be streaming a chapter's body from entry.rc at that moment (e.g. a
// reconvert overwrites the EPUB, changing file_hash, while another request
// is mid-read of the old content) — closing under it would corrupt that read
// or read from a reused file descriptor.
type cacheEntry struct {
	path    string
	hash    string
	rc      *zip.ReadCloser
	refs    int
	evicted bool
}

// zipHandle is what callers of zipCache.get hold. Release must be called
// exactly once (typically via defer) when done reading from Files().
type zipHandle struct {
	cache *zipCache
	entry *cacheEntry
}

func (h *zipHandle) Files() []*zip.File { return h.entry.rc.File }

func (h *zipHandle) Release() {
	h.cache.mu.Lock()
	defer h.cache.mu.Unlock()
	h.entry.refs--
	if h.entry.refs == 0 && h.entry.evicted {
		h.entry.rc.Close()
	}
}

// closeOrMarkEvicted closes entry immediately if nothing is reading from it,
// or flags it for close-on-last-release otherwise. Caller must hold c.mu.
func closeOrMarkEvicted(entry *cacheEntry) {
	if entry.refs == 0 {
		entry.rc.Close()
	} else {
		entry.evicted = true
	}
}

func newZipCache(capacity int) *zipCache {
	return &zipCache{
		cap:   capacity,
		items: make(map[string]*list.Element),
		order: list.New(),
	}
}

// A cached, refcounted zip reader, reopened if its hash changed (file
// rewritten in place). Callers must call Release() on the returned handle.
func (c *zipCache) get(path, hash string) (*zipHandle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[path]; ok {
		entry := el.Value.(*cacheEntry)
		if entry.hash == hash {
			c.order.MoveToFront(el)
			entry.refs++
			return &zipHandle{cache: c, entry: entry}, nil
		}
		// File changed on disk — drop the stale reader.
		closeOrMarkEvicted(entry)
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
			closeOrMarkEvicted(entry)
			delete(c.items, entry.path)
			c.order.Remove(back)
		}
	}

	entry := &cacheEntry{path: path, hash: hash, rc: rc, refs: 1}
	el := c.order.PushFront(entry)
	c.items[path] = el
	return &zipHandle{cache: c, entry: entry}, nil
}

// No caller yet — the hook for a future graceful shutdown. Closes
// unconditionally: only meant to run once nothing is reading anymore.
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
		respondInternal(w, "internal error", err)
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
		respondInternal(w, "internal error", err)
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
		// max-age, not no-cache: the ETag (keyed on FileHash) already changes
		// whenever the file changes, so a real cache lifetime is safe — the
		// reader's cache-mode toggle uses a cache-busting query string instead
		// of forcing revalidation here, so "cache on" browsing can skip the
		// network entirely, not just get a cheap 304 on every page turn.
		w.Header().Set("Cache-Control", "private, max-age=604800")
		http.ServeFile(w, r, b.Path)
		return
	}

	zh, err := s.zips.get(b.Path, b.FileHash)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "could not open epub")
		return
	}
	defer zh.Release()

	// Find and stream the zip entry.
	var found *zip.File
	for _, f := range zh.Files() {
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
	// max-age, not no-cache — see the html branch above for why. private: a
	// shared proxy/CDN must never replay this to another requester.
	w.Header().Set("Cache-Control", "private, max-age=604800")
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
