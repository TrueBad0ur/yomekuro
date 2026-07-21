package api

import (
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/truebad0ur/yomekuro/frontend"
	"github.com/truebad0ur/yomekuro/internal/auth"
	"github.com/truebad0ur/yomekuro/internal/scanner"
	"github.com/truebad0ur/yomekuro/internal/sysstats"
)

type Server struct {
	db                 *pgxpool.Pool
	scanner            *scanner.Scanner
	watcher            *scanner.Watcher
	dataDir            string
	zips               *zipCache
	jobsPollIntervalMS int
	sysStats           *sysstats.Collector
	loginLimiter       *loginLimiter
}

func NewRouter(pool *pgxpool.Pool, sc *scanner.Scanner, w *scanner.Watcher, dataDir string, zipCacheSize, jobsPollIntervalMS int) http.Handler {
	// 15s samples, 4h retained — enough for the Settings status graph without
	// an unbounded in-memory history; this is live telemetry, not persisted.
	stats := sysstats.NewCollector(15*time.Second, 4*time.Hour)
	go stats.Run(nil)

	s := &Server{
		db:                 pool,
		scanner:            sc,
		watcher:            w,
		dataDir:            dataDir,
		zips:               newZipCache(zipCacheSize),
		jobsPollIntervalMS: jobsPollIntervalMS,
		sysStats:           stats,
		loginLimiter:       newLoginLimiter(),
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
		r.Get("/api/config", s.getConfig)

		r.Get("/api/libraries", s.listLibraries)

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

		r.Get("/api/books/{id}/progress", s.getProgress)
		r.Put("/api/books/{id}/progress", s.putProgress)
		r.Put("/api/books/{id}/read", s.setReadState)

		// Admin-only: settings-page features. Regular users get read-only access to
		// everything above (browse, read, track progress).
		r.Group(func(r chi.Router) {
			r.Use(s.adminRequired)

			r.Post("/api/libraries", s.createLibrary)
			r.Delete("/api/libraries/{id}", s.deleteLibrary)
			r.Post("/api/libraries/{id}/scan", s.triggerScan)

			r.Post("/api/converter/upload", s.uploadArchive)
			r.Get("/api/converter/reconvertable", s.listReconvertable)
			r.Post("/api/converter/reconvert", s.reconvertSeries)
			r.Get("/api/converter/extract-images", s.extractVolumeImages)
			r.Delete("/api/converter/books", s.deleteBook)
			r.Patch("/api/converter/books", s.renameBook)
			r.Get("/api/converter/jobs", s.listConversionJobs)
			r.Delete("/api/converter/jobs/{id}", s.deleteConversionJob)
			r.Post("/api/converter/queue/pause", s.pauseQueue)
			r.Post("/api/converter/queue/resume", s.resumeQueue)

			r.Put("/api/books/{id}/tags", s.setBookTags)

			r.Get("/api/system-status", s.systemStatus)

			r.Get("/api/users", s.listUsers)
			r.Post("/api/users", s.createUser)
			r.Patch("/api/users/{id}", s.updateUser)
			r.Delete("/api/users/{id}", s.deleteUser)
		})
	})

	// no-cache (not no-store): embedded files carry no ETag, so this is the
	// only thing stopping a stale reader.js surviving a redeploy.
	sub, _ := fs.Sub(frontend.FS, "dist")
	fileServer := http.FileServer(http.FS(sub))
	noCache := func(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-cache") }
	r.Get("/reader", func(w http.ResponseWriter, r *http.Request) {
		noCache(w)
		http.ServeFileFS(w, r, sub, "reader.html")
	})
	r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
		user, err := auth.GetUserBySession(r.Context(), s.db, sessionToken(r))
		if err != nil || !user.IsAdmin {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		noCache(w)
		http.ServeFileFS(w, r, sub, "settings.html")
	})
	r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
		noCache(w)
		http.ServeFileFS(w, r, sub, "login.html")
	})
	r.Handle("/*", func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			noCache(w)
			fileServer.ServeHTTP(w, r)
		})
	}())

	return r
}

// Server-side config the frontend needs but can't read from container env.
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	respond(w, map[string]any{"jobs_poll_interval_ms": s.jobsPollIntervalMS})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(r.Context()); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	respond(w, map[string]string{"status": "ok"})
}
