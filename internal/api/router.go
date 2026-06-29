package api

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/truebad0ur/yomekuro/frontend"
	"github.com/truebad0ur/yomekuro/internal/scanner"
)

type Server struct {
	db      *pgxpool.Pool
	scanner *scanner.Scanner
	watcher *scanner.Watcher
	dataDir string
	zips    *zipCache
}

func NewRouter(pool *pgxpool.Pool, sc *scanner.Scanner, w *scanner.Watcher, dataDir, apiKey string) http.Handler {
	s := &Server{
		db:      pool,
		scanner: sc,
		watcher: w,
		dataDir: dataDir,
		zips:    newZipCache(20),
	}

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	if apiKey != "" {
		r.Use(apiKeyMiddleware(apiKey))
	}

	r.Get("/api/health", s.health)

	r.Get("/api/libraries", s.listLibraries)
	r.Post("/api/libraries", s.createLibrary)
	r.Delete("/api/libraries/{id}", s.deleteLibrary)
	r.Post("/api/libraries/{id}/scan", s.triggerScan)

	r.Get("/api/books", s.listBooks)
	r.Get("/api/books/{id}", s.getBook)
	r.Get("/api/books/{id}/cover", s.getBookCover)
	r.Get("/api/books/{id}/file", s.getBookFile)
	r.Get("/api/books/{id}/manifest", s.getBookManifest)
	r.Get("/api/books/{id}/content/*", s.getBookContent)

	r.Get("/api/series", s.listSeries)
	r.Get("/api/series/{name}/books", s.getSeriesBooks)

	r.Get("/api/books/{id}/progress", s.getProgress)
	r.Put("/api/books/{id}/progress", s.putProgress)

	// Serve frontend static files
	sub, _ := fs.Sub(frontend.FS, "dist")
	fileServer := http.FileServer(http.FS(sub))
	r.Handle("/*", fileServer)

	return r
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	respond(w, map[string]string{"status": "ok"})
}

func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := r.Header.Get("X-API-Key")
			if k == "" {
				k = r.URL.Query().Get("api_key")
			}
			if k != key {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
