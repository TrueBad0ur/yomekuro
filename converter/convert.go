package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// How often Convert looks for newly finished ".mokuro" files, so each volume's
// EPUB appears as it lands rather than only once the whole batch is OCR'd.
const mokuroPollInterval = 2 * time.Second

// gpuSem serializes mokuro across all callers — jobs run concurrently but there
// is one GPU. A channel, not a Mutex: the wait must be abandonable on Stop.
var gpuSem = make(chan struct{}, 1)

// acquireGPU takes the GPU slot, or gives up if the job is stopped while queued.
func acquireGPU(ctx context.Context) error {
	select {
	case gpuSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseGPU() { <-gpuSem }

// DefaultDetectorSize is the text-detector's input resolution unless a job (or
// the CLI) asks for something else — see CLAUDE.md's OCR-quality notes.
const DefaultDetectorSize = 3072

// Convert OCRs input (volume subdirs, or one flat image folder) into one EPUB
// per volume. Cancelling ctx kills mokuro; volumes already built are kept.
func Convert(ctx context.Context, input, output, volume string, noCache bool, detectorSize int, onVolume func(string)) (ok, fail int, err error) {
	if err := os.MkdirAll(output, 0755); err != nil {
		return 0, 0, fmt.Errorf("create output dir: %w", err)
	}

	// Always from every candidate name, even when volume narrows the OCR run to
	// one of them — else a single-volume reconvert looks like its own series.
	names, err := candidateVolumeNames(input)
	if err != nil {
		return 0, 0, fmt.Errorf("read input dir: %w", err)
	}
	series, seriesIndex := decideSeries(names, output)

	if err := convertJXLPages(input); err != nil {
		return 0, 0, fmt.Errorf("convert jxl pages: %w", err)
	}

	pdfTextOK, err := processPDFVolumes(input, output, series, seriesIndex)
	if err != nil {
		return 0, 0, fmt.Errorf("process pdf volumes: %w", err)
	}
	ok += pdfTextOK

	// Nothing but text-layer PDFs: already fully handled, so don't error on the
	// now-empty input dir.
	empty, err := isEmptyDir(input)
	if err != nil {
		return ok, 0, fmt.Errorf("read input dir: %w", err)
	}
	if empty {
		return ok, 0, nil
	}

	// mokuroDir is --parent_dir; volumesBaseDir holds the page images. They only
	// differ for the flat-volume symlink case below.
	mokuroDir := input
	volumesBaseDir := input

	var volumeDirs []string
	if volume != "" {
		volumeDirs = []string{filepath.Join(input, volume)}
		slog.Info("running mokuro OCR on single volume", "volume", volume)
	} else {
		flat, err := isFlatVolume(input)
		if err != nil {
			return ok, 0, fmt.Errorf("read input dir: %w", err)
		}
		if flat {
			// Name after output, not input — input is "<name>-in" for uploads,
			// output is the clean "<name>".
			volumeName := filepath.Base(output)
			absInput, err := filepath.Abs(input)
			if err != nil {
				return ok, 0, fmt.Errorf("resolve input path: %w", err)
			}
			tmpParent, err := os.MkdirTemp("", "mokuro-flat-")
			if err != nil {
				return ok, 0, fmt.Errorf("create temp dir: %w", err)
			}
			defer os.RemoveAll(tmpParent)
			// python-fire mis-tokenizes paths with spaces as positional args, so
			// symlink into a temp dir and point --parent_dir at that instead.
			if err := os.Symlink(absInput, filepath.Join(tmpParent, volumeName)); err != nil {
				return ok, 0, fmt.Errorf("create flat-volume symlink: %w", err)
			}
			mokuroDir = tmpParent
			volumesBaseDir = tmpParent
			slog.Info("treating input as a single flat volume", "volume", volumeName)
		} else {
			slog.Info("running mokuro OCR", "input", input)
		}
	}

	// mokuro writes each ".mokuro" as that volume finishes, long before the batch
	// exits — so poll and build each EPUB immediately instead of waiting.
	if err := acquireGPU(ctx); err != nil {
		return ok, fail, err
	}
	defer releaseGPU()

	reconcileOCRCache(mokuroDir)

	// Snapshot pre-existing ".mokuro" mtimes first, so the poll loop can tell a
	// genuine rewrite from stale leftover content from the previous run.
	builtMTime := snapshotMokuroMTimes(mokuroDir)

	mokuroErrCh := make(chan error, 1)
	go func() { mokuroErrCh <- runMokuro(ctx, mokuroDir, volumeDirs, noCache, detectorSize, onVolume) }()

	buildNewVolumes := func() {
		newOK, newFail := buildEPUBsForNewMokuroFiles(mokuroDir, volumesBaseDir, output, builtMTime, series, seriesIndex)
		ok += newOK
		fail += newFail
	}

	ticker := time.NewTicker(mokuroPollInterval)
	defer ticker.Stop()
pollLoop:
	for {
		select {
		case err = <-mokuroErrCh:
			break pollLoop
		case <-ticker.C:
			buildNewVolumes()
		}
	}
	// mokuro may have written its last file(s) between the final poll tick
	// and the process actually exiting — one more pass catches those.
	buildNewVolumes()
	if err != nil {
		return ok, fail, fmt.Errorf("mokuro: %w", err)
	}

	return ok, fail, nil
}

// snapshotMokuroMTimes lets buildEPUBsForNewMokuroFiles tell an untouched
// leftover from a genuine rewrite.
func snapshotMokuroMTimes(dir string) map[string]time.Time {
	m := map[string]time.Time{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mokuro") {
			continue
		}
		if info, err := e.Info(); err == nil {
			m[e.Name()] = info.ModTime()
		}
	}
	return m
}

// Builds an EPUB for each ".mokuro" whose mtime changed since builtMTime was
// last recorded, then updates it either way. Non-empty series overrides the derived one.
func buildEPUBsForNewMokuroFiles(mokuroDir, volumesBaseDir, output string, builtMTime map[string]time.Time, series string, seriesIndex map[string]float64) (ok, fail int) {
	entries, err := os.ReadDir(mokuroDir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mokuro") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mtime := info.ModTime()
		if prev, ok := builtMTime[e.Name()]; ok && prev.Equal(mtime) {
			continue
		}
		mokuroPath := filepath.Join(mokuroDir, e.Name())

		vol, err := parseMokuroFile(mokuroPath)
		if err != nil {
			// mokuro writes the file then fills it in, so a parse failure may just
			// mean mid-write: retry next tick rather than counting it failed.
			continue
		}
		builtMTime[e.Name()] = mtime
		vol.Series = series
		vol.SeriesIndex = seriesIndex[vol.Volume]

		outPath := filepath.Join(output, vol.Volume+".epub")
		slog.Info("building EPUB", "volume", vol.Volume, "pages", len(vol.Pages))
		if err := buildEPUB(vol, volumesBaseDir, outPath); err != nil {
			slog.Error("epub build failed", "volume", vol.Volume, "err", err)
			fail++
			continue
		}
		slog.Info("done", "output", outPath)
		ok++
	}
	return ok, fail
}

// The volume names input will produce: top-level subdirs and ".pdf" files. A
// flat image folder yields none — callers fall back to the output name.
func candidateVolumeNames(input string) ([]string, error) {
	entries, err := os.ReadDir(input)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		switch {
		case e.IsDir():
			if e.Name() != "_ocr" {
				names = append(names, e.Name())
			}
		case strings.EqualFold(filepath.Ext(e.Name()), ".pdf"):
			names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	return names, nil
}

// Groups a job's volumes into a series under output's name — always wins over
// the archive's own internal volume names.
func decideSeries(names []string, output string) (series string, seriesIndex map[string]float64) {
	if len(names) == 0 {
		// Flat single volume — matches the naming already used for it
		// elsewhere in Convert (volumeName := filepath.Base(output)).
		names = []string{filepath.Base(output)}
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	seriesIndex = make(map[string]float64, len(names))
	for i, n := range sorted {
		if idx, ok := leadingVolumeIndex(n); ok {
			seriesIndex[n] = idx
		} else {
			seriesIndex[n] = float64(i + 1)
		}
	}
	return filepath.Base(output), seriesIndex
}

// Whether dir holds page images directly (one volume) rather than a subdir per
// volume. "_ocr" is ignored so an already-converted flat volume still counts.
func isFlatVolume(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	hasImage := false
	for _, e := range entries {
		if e.IsDir() {
			if e.Name() != "_ocr" {
				return false, nil
			}
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".jpg", ".jpeg", ".png", ".webp", ".jxl":
			hasImage = true
		}
	}
	return hasImage, nil
}

// Whether mokuro has nothing left to process — i.e. the input was all
// text-layer PDFs, already handled by processPDFVolumes.
func isEmptyDir(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() == "_ocr" {
			continue
		}
		return false, nil
	}
	return true, nil
}

// Decodes .jxl pages to .png via djxl — mokuro only reads jpg/png/webp. PNG, not
// JPEG: djxl's lossless-JPEG-reconstruction path fails on some real JXLs.
func convertJXLPages(dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.ToLower(filepath.Ext(path)) != ".jxl" {
			return err
		}
		pngPath := strings.TrimSuffix(path, filepath.Ext(path)) + ".png"
		if out, err := exec.Command("djxl", path, pngPath).CombinedOutput(); err != nil {
			return fmt.Errorf("djxl %s: %w: %s", path, err, out)
		}
		return os.Remove(path)
	})
}
