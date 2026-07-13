package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// runWatch is a persistent worker with two independent poll loops — the
// DB-backed upload queue and the manual-folder scan — so neither blocks the
// other (a multi-hour manga OCR job used to stall every other upload behind
// it). Each loop also dispatches its own jobs as they're found rather than
// processing them one at a time in sequence: a fast text-PDF job (no OCR
// needed, see pdf.go) finishes in seconds regardless of how many slow OCR
// jobs are already running. The only thing still serialized is the GPU
// itself, via gpuSem in convert.go.
func runWatch(pool *pgxpool.Pool, library string, interval time.Duration) {
	slog.Info("watch mode started", "library", library, "poll_interval", interval)
	ctx := context.Background()

	// A previous process may have died mid-job, leaving rows marked 'running'
	// that nothing owns any more.
	if err := reclaimOrphanedJobs(ctx, pool); err != nil {
		slog.Error("watch: reclaim orphaned jobs failed", "err", err)
	}

	go func() {
		for {
			drainQueue(ctx, pool)
			time.Sleep(interval)
		}
	}()

	for {
		scanManualFolders(ctx, pool, library)
		time.Sleep(interval)
	}
}

// drainQueue claims every pending job and hands each to its own goroutine
// immediately, rather than waiting for one to finish before claiming the
// next — claiming is a fast atomic DB update either way, so this just
// removes the "next job waits for the previous job's whole conversion"
// bottleneck.
func drainQueue(ctx context.Context, pool *pgxpool.Pool) {
	for {
		j, err := claimNextJob(ctx, pool)
		if err != nil {
			slog.Error("watch: claim job failed", "err", err)
			return
		}
		if j == nil {
			return // queue empty
		}
		go processQueuedJob(ctx, pool, j)
	}
}

// stopPollInterval controls how often a running job checks whether it's been
// flagged for cancellation (see stopRequested) — same cadence as Convert's
// own incremental-EPUB poll, no reason for it to be more eager than that.
const stopPollInterval = 2 * time.Second

func processQueuedJob(ctx context.Context, pool *pgxpool.Pool, j *job) {
	slog.Info("watch: converting", "job", j.Name, "input", j.InputPath, "output", j.OutputPath)

	// Cancelling jobCtx kills the mokuro subprocess (see Convert/runMokuro) —
	// this goroutine is the only thing that ever cancels it, by polling
	// stop_requested (set by the UI's "Stop" button, internal/api/converter.go)
	// while the conversion is running. The deferred cancel() below also
	// guarantees this goroutine exits promptly once Convert returns on its
	// own, canceled or not.
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		ticker := time.NewTicker(stopPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				stop, err := stopRequested(ctx, pool, j.ID)
				if err != nil {
					slog.Warn("watch: check stop_requested failed", "job", j.Name, "err", err)
					continue
				}
				if stop {
					slog.Info("watch: stop requested, cancelling", "job", j.Name)
					cancel()
					return
				}
			}
		}
	}()

	onVolume := func(volume string) {
		if err := updateJobVolume(ctx, pool, j.ID, volume); err != nil {
			slog.Warn("watch: update current volume failed", "job", j.Name, "err", err)
		}
	}
	ok, fail, err := Convert(jobCtx, j.InputPath, j.OutputPath, "", false, onVolume)

	switch {
	case jobCtx.Err() != nil:
		// Cancelled via the stop-poller above, not a parent-context timeout
		// (ctx here is always context.Background() — see runWatch) — a
		// deliberate user action, not a failure.
		slog.Info("watch: conversion stopped", "job", j.Name, "volumes_done", ok)
		markJobStopped(ctx, pool, j.ID)
		if ok == 0 {
			cleanupFailedJob(j.Name, j.InputPath, j.OutputPath)
		}
	case err != nil || fail > 0 || ok == 0:
		msg := "no volumes were converted"
		switch {
		case err != nil:
			msg = err.Error()
		case fail > 0:
			msg = "some volumes failed to convert"
		}
		slog.Error("watch: conversion unsuccessful", "job", j.Name, "detail", msg)
		markJobFailed(ctx, pool, j.ID, msg)
		// ok > 0 here means some volumes (e.g. text-layer PDFs, which never
		// touch mokuro) already finished successfully before the error/failure
		// — a text-PDF volume can succeed independently of an OCR volume
		// failing in the same job. Only wipe the staging folders when
		// *nothing* succeeded, otherwise this would delete real output.
		if ok == 0 {
			cleanupFailedJob(j.Name, j.InputPath, j.OutputPath)
		}
	default:
		slog.Info("watch: conversion done", "job", j.Name, "volumes", ok)
		markJobDone(ctx, pool, j.ID)
	}
}

// cleanupFailedJob removes a completely-failed upload's staging folders so
// the same name can be retried — only called when nothing at all converted,
// so the output dir is guaranteed empty.
func cleanupFailedJob(name, inputPath, outputPath string) {
	if err := os.RemoveAll(inputPath); err != nil {
		slog.Warn("watch: cleanup failed job input", "job", name, "path", inputPath, "err", err)
	}
	if err := os.RemoveAll(outputPath); err != nil {
		slog.Warn("watch: cleanup failed job output", "job", name, "path", outputPath, "err", err)
	}
}

// manualInProgress tracks input dirs currently being converted by a
// goroutine spawned from scanManualFoldersIn. A manual folder has no DB row
// to atomically claim (unlike the upload queue), so without this a folder
// mid-conversion would look identical to one that just hasn't started yet
// (manualFolderNeedsConversion can't tell "in progress" from "not started")
// and get relaunched on every subsequent poll tick.
var (
	manualInProgressMu sync.Mutex
	manualInProgress   = map[string]bool{}
)

// scanManualFolders looks for "<name>-in" folders one level below library —
// library itself is just the mount point (/library); the actual registered
// libraries are its subfolders (ranobe/manga/html, see cmd/yomekuro/main.go),
// and that's where a manually staged "-in" folder actually lives.
func scanManualFolders(ctx context.Context, pool *pgxpool.Pool, library string) {
	roots, err := os.ReadDir(library)
	if err != nil {
		slog.Error("watch: cannot read library", "err", err)
		return
	}
	for _, root := range roots {
		if !root.IsDir() {
			continue
		}
		scanManualFoldersIn(ctx, pool, filepath.Join(library, root.Name()))
	}
}

func scanManualFoldersIn(ctx context.Context, pool *pgxpool.Pool, libraryRoot string) {
	entries, err := os.ReadDir(libraryRoot)
	if err != nil {
		slog.Error("watch: cannot read library root", "root", libraryRoot, "err", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), "-in") {
			continue
		}
		inputDir := filepath.Join(libraryRoot, e.Name())

		tracked, err := jobExistsForPath(ctx, pool, inputDir)
		if err != nil {
			slog.Error("watch: check job existence failed", "path", inputDir, "err", err)
			continue
		}
		if tracked {
			continue // owned by the DB queue, already handled by drainQueue
		}

		name := strings.TrimSuffix(e.Name(), "-in")
		outputDir := filepath.Join(libraryRoot, name)

		if !manualFolderNeedsConversion(inputDir, outputDir) {
			continue
		}

		manualInProgressMu.Lock()
		if manualInProgress[inputDir] {
			manualInProgressMu.Unlock()
			continue
		}
		manualInProgress[inputDir] = true
		manualInProgressMu.Unlock()

		slog.Info("watch: converting manual folder", "input", inputDir, "output", outputDir)
		go func() {
			defer func() {
				manualInProgressMu.Lock()
				delete(manualInProgress, inputDir)
				manualInProgressMu.Unlock()
			}()
			if _, _, err := Convert(context.Background(), inputDir, outputDir, "", false, nil); err != nil {
				slog.Error("watch: manual folder conversion failed", "input", inputDir, "err", err)
			}
		}()
	}
}

// manualFolderNeedsConversion reports whether outputDir is missing EPUBs for
// one or more of inputDir's volumes. A manual folder has no job-status marker
// to check instead, so without this every already-converted manual folder
// left in the library would get fully rebuilt on every single poll tick.
func manualFolderNeedsConversion(inputDir, outputDir string) bool {
	volumes, err := countVolumes(inputDir)
	if err != nil || volumes == 0 {
		return false
	}
	epubs, _ := filepath.Glob(filepath.Join(outputDir, "*.epub"))
	return len(epubs) < volumes
}

// countVolumes mirrors isFlatVolume's detection in convert.go, plus
// processPDFVolumes': a folder of images directly is one volume, a folder of
// subdirectories is one volume per subdirectory (ignoring mokuro's own
// "_ocr" cache dir), and each top-level ".pdf" file is its own volume too.
func countVolumes(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	subdirs, pdfs, hasImage := 0, 0, false
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() != "_ocr" {
				subdirs++
			}
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".jpg", ".jpeg", ".png", ".webp", ".jxl":
			hasImage = true
		case ".pdf":
			pdfs++
		}
	}
	if subdirs == 0 && hasImage {
		return 1 + pdfs, nil
	}
	return subdirs + pdfs, nil
}
