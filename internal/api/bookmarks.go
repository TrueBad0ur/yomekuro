package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
)

var bookmarkColors = map[string]bool{
	"yellow": true, "pink": true, "blue": true, "green": true,
}

func bookmarkJSON(b db.Bookmark) map[string]any {
	return map[string]any{
		"id": db.UUIDString(b.ID), "book_id": db.UUIDString(b.BookID),
		"spine_index": b.SpineIndex, "elem_index": b.ElemIndex,
		"start_offset": b.StartOffset, "end_offset": b.EndOffset,
		"selected_text": b.SelectedText, "note": b.Note, "color": b.Color,
		"created_at": b.CreatedAt.UTC(), "updated_at": b.UpdatedAt.UTC(),
	}
}

func (s *Server) listBookmarks(w http.ResponseWriter, r *http.Request) {
	bookID, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	items, err := db.ListBookmarks(r.Context(), s.db, bookID, user.ID)
	if err != nil {
		respondInternal(w, "could not list bookmarks", err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, bookmarkJSON(item))
	}
	respond(w, out)
}

func validBookmarkPosition(spine, elem, start, end int) bool {
	return spine >= 0 && elem >= 0 && start >= 0 && end > start
}

func cleanBookmarkText(value string, max int) (string, bool) {
	value = strings.TrimSpace(value)
	return value, len([]rune(value)) <= max
}

func (s *Server) createBookmark(w http.ResponseWriter, r *http.Request) {
	bookID, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	var req struct {
		SpineIndex   int    `json:"spine_index"`
		ElemIndex    int    `json:"elem_index"`
		StartOffset  int    `json:"start_offset"`
		EndOffset    int    `json:"end_offset"`
		SelectedText string `json:"selected_text"`
		Note         string `json:"note"`
		Color        string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.SelectedText, ok = cleanBookmarkText(req.SelectedText, 2000)
	if !ok {
		respondError(w, http.StatusBadRequest, "selected text is too long")
		return
	}
	req.Note, ok = cleanBookmarkText(req.Note, 5000)
	if !ok {
		respondError(w, http.StatusBadRequest, "note is too long")
		return
	}
	if !validBookmarkPosition(req.SpineIndex, req.ElemIndex, req.StartOffset, req.EndOffset) {
		respondError(w, http.StatusBadRequest, "invalid bookmark position")
		return
	}
	if !bookmarkColors[req.Color] {
		respondError(w, http.StatusBadRequest, "invalid color")
		return
	}
	item, err := db.CreateBookmark(r.Context(), s.db, db.Bookmark{
		BookID: bookID, UserID: user.ID, SpineIndex: req.SpineIndex, ElemIndex: req.ElemIndex,
		StartOffset: req.StartOffset, EndOffset: req.EndOffset,
		SelectedText: req.SelectedText, Note: req.Note, Color: req.Color,
	})
	if err != nil {
		respondInternal(w, "could not create bookmark", err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	respond(w, bookmarkJSON(item))
}

func (s *Server) updateBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	var req struct {
		Note  string `json:"note"`
		Color string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	req.Note, ok = cleanBookmarkText(req.Note, 5000)
	if !ok {
		respondError(w, http.StatusBadRequest, "note is too long")
		return
	}
	if !bookmarkColors[req.Color] {
		respondError(w, http.StatusBadRequest, "invalid color")
		return
	}
	item, err := db.UpdateBookmark(r.Context(), s.db, id, user.ID, req.Note, req.Color)
	if err == pgx.ErrNoRows {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		respondInternal(w, "could not update bookmark", err)
		return
	}
	respond(w, bookmarkJSON(item))
}

func (s *Server) deleteBookmark(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	user, _ := userFromCtx(r)
	deleted, err := db.DeleteBookmark(r.Context(), s.db, id, user.ID)
	if err != nil {
		respondInternal(w, "could not delete bookmark", err)
		return
	}
	if !deleted {
		respondError(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
