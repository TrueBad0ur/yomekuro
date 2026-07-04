package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	input := flag.String("input", "", "Directory containing manga volume subdirectories, or a single flat directory of page images (treated as one volume)")
	output := flag.String("output", "", "Output directory for EPUB files")
	volume := flag.String("volume", "", "Process only this volume subdirectory name (e.g. 'Dungeon Meshi v01')")
	noCache := flag.Bool("no-cache", false, "Re-run OCR even if cached results exist")
	watch := flag.Bool("watch", false, "Run as a persistent worker: drain yomekuro's upload job queue and poll --library for manually-created <name>-in/ folders")
	library := flag.String("library", "/library", "Library root to poll for manually-created <name>-in/ folders (--watch mode only)")
	pollInterval := flag.Duration("poll-interval", 10*time.Second, "Poll interval for --watch mode")
	dbDSN := flag.String("db", os.Getenv("CONVERTER_DB"), "PostgreSQL DSN for the upload job queue (--watch mode only; env CONVERTER_DB)")
	flag.Parse()

	if *watch {
		if *dbDSN == "" {
			fmt.Fprintln(os.Stderr, "--watch requires --db (or CONVERTER_DB) — the upload job queue lives in yomekuro's Postgres")
			os.Exit(1)
		}
		pool, err := pgxpool.New(context.Background(), *dbDSN)
		if err != nil {
			slog.Error("db connect failed", "err", err)
			os.Exit(1)
		}
		defer pool.Close()
		runWatch(pool, *library, *pollInterval)
		return
	}

	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: converter --input <dir> --output <dir> [--volume <name>] [--no-cache]")
		fmt.Fprintln(os.Stderr, "   or: converter --watch --db <dsn> [--library <dir>] [--poll-interval <duration>]")
		os.Exit(1)
	}

	ok, fail, err := Convert(*input, *output, *volume, *noCache, nil)
	if err != nil {
		slog.Error("mokuro failed", "err", err)
		os.Exit(1)
	}

	slog.Info("finished", "ok", ok, "failed", fail)
	if fail > 0 || ok == 0 {
		os.Exit(1)
	}
}
