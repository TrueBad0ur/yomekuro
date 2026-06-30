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

func NewRouter(pool *pgxpool.Pool, sc *scanner.Scanner, w *scanner.Watcher, dataDir string) http.Handler {
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

	r.Get("/api/health", s.health)

	// Public auth endpoints
	r.Post("/api/auth/login", s.login)
	r.Post("/api/auth/register", s.register)

	// Protected API endpoints
	r.Group(func(r chi.Router) {
		r.Use(s.authRequired)

		r.Post("/api/auth/logout", s.logout)
		r.Get("/api/auth/me", s.me)

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

		r.Get("/api/tags", s.listTags)
		r.Get("/api/books/{id}/tags", s.getBookTags)
		r.Put("/api/books/{id}/tags", s.setBookTags)

		r.Get("/api/books/{id}/progress", s.getProgress)
		r.Put("/api/books/{id}/progress", s.putProgress)

		// Admin-only
		r.Group(func(r chi.Router) {
			r.Use(s.adminRequired)
			r.Get("/api/users", s.listUsers)
			r.Post("/api/users", s.createUser)
			r.Patch("/api/users/{id}", s.updateUser)
			r.Delete("/api/users/{id}", s.deleteUser)
		})
	})

	// Serve frontend — clean URLs + static assets
	sub, _ := fs.Sub(frontend.FS, "dist")
	fileServer := http.FileServer(http.FS(sub))
	r.Get("/reader", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, sub, "reader.html")
	})
	r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, sub, "settings.html")
	})
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, sub, "login.html")
	})
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
