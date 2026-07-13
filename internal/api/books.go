package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
)

type bookDTO struct {
	ID               string   `json:"id"`
	LibraryID        string   `json:"library_id"`
	Title            string   `json:"title"`
	SortTitle        string   `json:"sort_title"`
	Authors          []string `json:"authors"`
	Language         string   `json:"language"`
	Publisher        string   `json:"publisher"`
	PublishedAt      *string  `json:"published_at,omitempty"`
	Description      string   `json:"description"`
	ISBN             string   `json:"isbn"`
	SeriesName       string   `json:"series_name"`
	SeriesIndex      float64  `json:"series_index"`
	PageCount        int      `json:"page_count"`
	ReadingDirection string   `json:"reading_direction"`
	CoverURL         string   `json:"cover_url"`
	FileSize         int64    `json:"file_size"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	ProgressPct      float64  `json:"progress_pct"`
}

func toBookDTO(b db.Book) bookDTO {
	d := bookDTO{
		ID:               db.UUIDString(b.ID),
		LibraryID:        db.UUIDString(b.LibraryID),
		Title:            b.Title,
		SortTitle:        b.SortTitle,
		Authors:          b.Authors,
		Language:         b.Language,
		Publisher:        b.Publisher,
		Description:      b.Description,
		ISBN:             b.ISBN,
		SeriesName:       b.SeriesName,
		SeriesIndex:      b.SeriesIndex,
		PageCount:        b.PageCount,
		ReadingDirection: b.ReadingDirection,
		CoverURL:         fmt.Sprintf("/api/books/%s/cover", db.UUIDString(b.ID)),
		FileSize:         b.FileSize,
		CreatedAt:        b.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:        b.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if b.Authors == nil {
		d.Authors = []string{}
	}
	if b.PublishedAt != nil {
		s := b.PublishedAt.Format("2006-01-02")
		d.PublishedAt = &s
	}
	return d
}

func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))

	f := db.BookFilter{
		LibraryID: q.Get("library"),
		Language:  q.Get("lang"),
		Series:    q.Get("series"),
		Tag:       q.Get("tag"),
		Query:     q.Get("q"),
		Sort:      q.Get("sort"),
		Page:      page,
		Limit:     limit,
	}

	result, err := db.ListBooks(r.Context(), s.db, f)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dtos := s.bookDTOsWithProgress(r, result.Items)
	respond(w, map[string]any{
		"items": dtos,
		"total": result.Total,
		"page":  result.Page,
		"limit": result.Limit,
	})
}

func (s *Server) getBook(w http.ResponseWriter, r *http.Request) {
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
	respond(w, toBookDTO(b))
}

func (s *Server) getBookCover(w http.ResponseWriter, r *http.Request) {
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
	if b.CoverPath == "" {
		respondError(w, http.StatusNotFound, "no cover")
		return
	}

	etag := fmt.Sprintf(`"%s-cover"`, b.FileHash)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "max-age=86400")
	if b.CoverMediaType != "" {
		w.Header().Set("Content-Type", b.CoverMediaType)
	}
	http.ServeFile(w, r, b.CoverPath)
}

func (s *Server) getBookFile(w http.ResponseWriter, r *http.Request) {
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
	if _, err := os.Stat(b.Path); err != nil {
		respondError(w, http.StatusNotFound, "file not found on disk")
		return
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, b.Filename))
	if b.Format == "html" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/epub+zip")
	}
	http.ServeFile(w, r, b.Path)
}

func (s *Server) listSeries(w http.ResponseWriter, r *http.Request) {
	excludeHTML := r.URL.Query().Get("exclude_html") == "1"
	series, err := db.ListSeries(r.Context(), s.db, r.URL.Query().Get("library"), excludeHTML)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type seriesDTO struct {
		Name      string `json:"name"`
		BookCount int    `json:"book_count"`
		CoverURL  string `json:"cover_url"`
	}
	dtos := make([]seriesDTO, len(series))
	for i, sr := range series {
		dtos[i] = seriesDTO{
			Name:      sr.Name,
			BookCount: sr.BookCount,
			CoverURL:  fmt.Sprintf("/api/books/%s/cover", db.UUIDString(sr.CoverBookID)),
		}
	}
	respond(w, map[string]any{"items": dtos})
}

func (s *Server) getBookTags(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	tags, err := db.GetBookTags(r.Context(), s.db, id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tags == nil {
		tags = []string{}
	}
	respond(w, map[string]any{"items": tags})
}

func (s *Server) setBookTags(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Tags == nil {
		req.Tags = []string{}
	}
	if err := db.SetBookTags(r.Context(), s.db, id, req.Tags); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := db.ListTags(r.Context(), s.db)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tags == nil {
		tags = []string{}
	}
	respond(w, map[string]any{"items": tags})
}

func (s *Server) getSeriesBooks(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	result, err := db.ListBooks(r.Context(), s.db, db.BookFilter{
		Series: name,
		Sort:   "series",
		Limit:  200,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, map[string]any{
		"items": s.bookDTOsWithProgress(r, result.Items),
		"total": result.Total,
	})
}

// bookDTOsWithProgress converts books for the grid, batch-filling the current
// user's reading progress. Every endpoint that feeds a book grid must go
// through this — the series listing used to skip it, so a volume's progress bar
// (and its read state) never showed on the series page.
func (s *Server) bookDTOsWithProgress(r *http.Request, books []db.Book) []bookDTO {
	var progressMap map[[16]byte]float64
	if user, ok := userFromCtx(r); ok && len(books) > 0 {
		ids := make([][16]byte, len(books))
		for i, b := range books {
			ids[i] = b.ID
		}
		progressMap, _ = db.GetProgressBatch(r.Context(), s.db, user.ID, ids)
	}
	dtos := make([]bookDTO, len(books))
	for i, b := range books {
		d := toBookDTO(b)
		d.ProgressPct = progressMap[b.ID]
		dtos[i] = d
	}
	return dtos
}
