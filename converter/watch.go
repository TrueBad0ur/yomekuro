package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// runWatch is a persistent worker: each tick drains yomekuro's upload job
// queue, then scans the library for manually-created "<name>-in" folders with
// no DB row.
func runWatch(pool *pgxpool.Pool, library string, interval time.Duration) {
	slog.Info("watch mode started", "library", library, "poll_interval", interval)
	ctx := context.Background()
	for {
		drainQueue(ctx, pool)
		scanManualFolders(ctx, pool, library)
		time.Sleep(interval)
	}
}

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

		slog.Info("watch: converting", "job", j.Name, "input", j.InputPath, "output", j.OutputPath)
		onVolume := func(volume string) {
			if err := updateJobVolume(ctx, pool, j.ID, volume); err != nil {
				slog.Warn("watch: update current volume failed", "job", j.Name, "err", err)
			}
		}
		ok, fail, err := Convert(j.InputPath, j.OutputPath, "", false, onVolume)
		switch {
		case err != nil:
			slog.Error("watch: conversion failed", "job", j.Name, "err", err)
			markJobFailed(ctx, pool, j.ID, err.Error())
			cleanupFailedJob(j.Name, j.InputPath, j.OutputPath)
		case fail > 0 || ok == 0:
			msg := "no volumes were converted"
			if fail > 0 {
				msg = "some volumes failed to convert"
			}
			slog.Error("watch: conversion unsuccessful", "job", j.Name, "detail", msg)
			markJobFailed(ctx, pool, j.ID, msg)
			// A partial failure (some volumes did convert) leaves real output in
			// place — only wipe the staging folders when *nothing* succeeded,
			// otherwise this would delete a partially-finished manga.
			if ok == 0 {
				cleanupFailedJob(j.Name, j.InputPath, j.OutputPath)
			}
		default:
			slog.Info("watch: conversion done", "job", j.Name, "volumes", ok)
			markJobDone(ctx, pool, j.ID)
		}
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

func scanManualFolders(ctx context.Context, pool *pgxpool.Pool, library string) {
	entries, err := os.ReadDir(library)
	if err != nil {
		slog.Error("watch: cannot read library", "err", err)
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasSuffix(e.Name(), "-in") {
			continue
		}
		inputDir := filepath.Join(library, e.Name())

		tracked, err := jobExistsForPath(ctx, pool, inputDir)
		if err != nil {
			slog.Error("watch: check job existence failed", "path", inputDir, "err", err)
			continue
		}
		if tracked {
			continue // owned by the DB queue, already handled by drainQueue
		}

		name := strings.TrimSuffix(e.Name(), "-in")
		outputDir := filepath.Join(library, name)

		if !manualFolderNeedsConversion(inputDir, outputDir) {
			continue
		}

		slog.Info("watch: converting manual folder", "input", inputDir, "output", outputDir)
		if _, _, err := Convert(inputDir, outputDir, "", false, nil); err != nil {
			slog.Error("watch: manual folder conversion failed", "input", inputDir, "err", err)
		}
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

// countVolumes mirrors isFlatVolume's detection in convert.go: a folder of
// images directly is one volume, a folder of subdirectories is one volume per
// subdirectory (ignoring mokuro's own "_ocr" cache dir).
func countVolumes(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	subdirs, hasImage := 0, false
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
		}
	}
	if subdirs == 0 && hasImage {
		return 1, nil
	}
	return subdirs, nil
}
