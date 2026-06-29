package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/truebad0ur/yomekuro/internal/db"
)

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := db.ListLibraries(r.Context(), s.db)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type libDTO struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Path      string `json:"path"`
		CreatedAt string `json:"created_at"`
	}
	dtos := make([]libDTO, len(libs))
	for i, l := range libs {
		dtos[i] = libDTO{
			ID:        db.UUIDString(l.ID),
			Name:      l.Name,
			Path:      l.Path,
			CreatedAt: l.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	respond(w, map[string]any{"items": dtos})
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Name == "" || req.Path == "" {
		respondError(w, http.StatusBadRequest, "name and path are required")
		return
	}
	lib, err := db.CreateLibrary(r.Context(), s.db, req.Name, req.Path)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.watcher != nil {
		s.watcher.AddLibrary(lib)
	}
	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]string{
		"id":   db.UUIDString(lib.ID),
		"name": lib.Name,
		"path": lib.Path,
	})
}

func (s *Server) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if err := db.DeleteLibrary(r.Context(), s.db, id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) triggerScan(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, id)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}
	go func() {
		if err := s.scanner.ScanLibrary(context.Background(), lib); err != nil {
			slog.Error("scan failed", "library", lib.Name, "err", err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
	respond(w, map[string]string{"status": "scanning"})
}
