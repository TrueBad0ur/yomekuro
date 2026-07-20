package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/truebad0ur/yomekuro/internal/api"
	"github.com/truebad0ur/yomekuro/internal/auth"
	"github.com/truebad0ur/yomekuro/internal/config"
	"github.com/truebad0ur/yomekuro/internal/db"
	"github.com/truebad0ur/yomekuro/internal/scanner"
)

func main() {
	cfg := config.Load()
	if cfg.DB == "" {
		slog.Error("missing -db / YOMEKURO_DB")
		os.Exit(1)
	}

	ctx := context.Background()

	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		slog.Error("db.Open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := os.MkdirAll(filepath.Join(cfg.Data, "covers"), 0o755); err != nil {
		slog.Error("create data dir", "err", err)
		os.Exit(1)
	}

	if cfg.AdminPassword != "" {
		if err := auth.EnsureAdmin(ctx, pool, cfg.AdminUser, cfg.AdminPassword); err != nil {
			slog.Error("EnsureAdmin", "err", err)
			os.Exit(1)
		}
	} else {
		slog.Warn("YOMEKURO_ADMIN_PASSWORD not set — admin will not be created automatically")
	}

	// "Books" absorbs the former Ranobe/HTML/PDF split — never a useful
	// distinction for browsing, they're both just "not manga".
	for _, def := range []struct{ name, path string }{
		{"Books", "/library/books"},
		{"Manga", "/library/manga"},
	} {
		if _, err := db.EnsureDefaultLibrary(ctx, pool, def.name, def.path); err != nil {
			slog.Error("EnsureDefaultLibrary", "err", err)
			os.Exit(1)
		}
	}

	sc := scanner.New(pool, cfg.Data)

	watcher, err := scanner.NewWatcher(sc, pool)
	if err != nil {
		slog.Warn("watcher disabled", "err", err)
		watcher = nil
	}

	libs, err := db.ListLibraries(ctx, pool)
	if err != nil {
		slog.Error("ListLibraries", "err", err)
		os.Exit(1)
	}
	if watcher != nil {
		watcher.Start(ctx, libs)
	}

	if cfg.ScanOnStart {
		for _, lib := range libs {
			if err := sc.ScanLibrary(ctx, lib); err != nil {
				slog.Error("scan on start", "library", lib.Name, "err", err)
			}
		}
	}

	// Nothing else prunes the sessions table — without this, expired rows
	// (30-day TTL) accumulate forever.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if n, err := auth.SweepExpiredSessions(ctx, pool); err != nil {
				slog.Warn("session sweep failed", "err", err)
			} else if n > 0 {
				slog.Info("swept expired sessions", "count", n)
			}
		}
	}()

	router := api.NewRouter(pool, sc, watcher, cfg.Data, cfg.BackupDir, cfg.ZipCacheSize, cfg.JobsPollIntervalMS)

	// WriteTimeout is deliberately unset: it would cut off large downloads to slow
	// clients. ReadHeaderTimeout still guards against Slowloris.
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("yomekuro listening", "addr", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("ListenAndServe", "err", err)
		os.Exit(1)
	}
}
