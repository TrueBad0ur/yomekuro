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

// Two independent poll loops — the upload queue and the manual-folder scan —
// each dispatching jobs as goroutines. Only the GPU is serialized (gpuSem).
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

// Claims every pending job straight into its own goroutine, so one conversion
// never blocks the next from starting.
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

// How often a running job checks whether it's been flagged for cancellation.
const stopPollInterval = 2 * time.Second

func processQueuedJob(ctx context.Context, pool *pgxpool.Pool, j *job) {
	slog.Info("watch: converting", "job", j.Name, "input", j.InputPath, "output", j.OutputPath)

	// Cancelling jobCtx kills mokuro. This poller is the only thing that does so,
	// watching stop_requested; the deferred cancel also stops it on normal exit.
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
	ok, fail, err := Convert(jobCtx, j.InputPath, j.OutputPath, j.Volume, j.ForceOCR, onVolume)

	switch {
	case jobCtx.Err() != nil:
		// Cancelled by the stop-poller above: a deliberate user action, not a
		// failure (the parent ctx is always Background).
		slog.Info("watch: conversion stopped", "job", j.Name, "volumes_done", ok)
		markJobStopped(ctx, pool, j.ID)
		if ok == 0 && !j.ForceOCR {
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
		// ok > 0 means some volumes already converted, so only wipe the staging
		// folders when nothing at all succeeded — else this deletes real output.
		// A reconvert job's paths are the book's real, shared dirs, not a fresh
		// staging dir, so never wipe those even on total failure.
		if ok == 0 && !j.ForceOCR {
			cleanupFailedJob(j.Name, j.InputPath, j.OutputPath)
		}
	default:
		slog.Info("watch: conversion done", "job", j.Name, "volumes", ok)
		markJobDone(ctx, pool, j.ID)
	}
}

// Removes a wholly-failed upload's staging folders so the name can be retried.
// Only called when nothing converted, so the output dir is empty.
func cleanupFailedJob(name, inputPath, outputPath string) {
	if err := os.RemoveAll(inputPath); err != nil {
		slog.Warn("watch: cleanup failed job input", "job", name, "path", inputPath, "err", err)
	}
	if err := os.RemoveAll(outputPath); err != nil {
		slog.Warn("watch: cleanup failed job output", "job", name, "path", outputPath, "err", err)
	}
}

// Manual folders have no DB row to claim, so without this a folder mid-
// conversion looks unstarted and gets relaunched on every poll tick.
var (
	manualInProgressMu sync.Mutex
	manualInProgress   = map[string]bool{}
)

// Looks for "<name>-in" folders one level below library: /library is only the
// mount point, the registered libraries are its subfolders.
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

// Whether outputDir is missing EPUBs for any of inputDir's volumes — a manual
// folder has no job status, so without this it would rebuild on every tick.
func manualFolderNeedsConversion(inputDir, outputDir string) bool {
	volumes, err := countVolumes(inputDir)
	if err != nil || volumes == 0 {
		return false
	}
	epubs, _ := filepath.Glob(filepath.Join(outputDir, "*.epub"))
	return len(epubs) < volumes
}

// Images directly in the folder are one volume; each subdirectory (bar "_ocr")
// and each top-level ".pdf" is one more.
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
