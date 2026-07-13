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

// Convert OCRs input (volume subdirs, or one flat image folder) into one EPUB
// per volume. Cancelling ctx kills mokuro; volumes already built are kept.
func Convert(ctx context.Context, input, output, volume string, noCache bool, onVolume func(string)) (ok, fail int, err error) {
	if err := os.MkdirAll(output, 0755); err != nil {
		return 0, 0, fmt.Errorf("create output dir: %w", err)
	}

	// Decided upfront: later steps restructure the directory, so deciding this
	// incrementally could change the answer partway through a batch. Always from
	// every candidate name in input, even when volume narrows the actual OCR run
	// to one of them — otherwise a single-volume reconvert would see itself as
	// the only volume and get treated as its own standalone one-book series.
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

	mokuroErrCh := make(chan error, 1)
	go func() { mokuroErrCh <- runMokuro(ctx, mokuroDir, volumeDirs, noCache, onVolume) }()

	// Re-running one volume leaves the others' ".mokuro" files in place; pre-mark
	// them built so the poll loop only picks up the targeted one.
	built := map[string]bool{}
	if volume != "" {
		if entries, err := os.ReadDir(mokuroDir); err == nil {
			target := volume + ".mokuro"
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".mokuro") && e.Name() != target {
					built[e.Name()] = true
				}
			}
		}
	}
	buildNewVolumes := func() {
		newOK, newFail := buildEPUBsForNewMokuroFiles(mokuroDir, volumesBaseDir, output, built, series, seriesIndex)
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

// Builds an EPUB for each ".mokuro" not yet in built, marking it built either
// way (a failure isn't retried). Non-empty series overrides the derived one.
func buildEPUBsForNewMokuroFiles(mokuroDir, volumesBaseDir, output string, built map[string]bool, series string, seriesIndex map[string]float64) (ok, fail int) {
	entries, err := os.ReadDir(mokuroDir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".mokuro") || built[e.Name()] {
			continue
		}
		mokuroPath := filepath.Join(mokuroDir, e.Name())

		vol, err := parseMokuroFile(mokuroPath)
		if err != nil {
			// mokuro writes the file then fills it in, so a parse failure may just
			// mean mid-write: retry next tick rather than counting it failed.
			continue
		}
		built[e.Name()] = true
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

// Groups a job's volumes into a series: names sharing a "Name vNN" pattern keep
// their own (returns ""); an anthology is grouped under the job's output name.
func decideSeries(names []string, output string) (series string, seriesIndex map[string]float64) {
	if len(names) == 0 {
		// Flat single volume — matches the naming already used for it
		// elsewhere in Convert (volumeName := filepath.Base(output)).
		names = []string{filepath.Base(output)}
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)

	shared := seriesName(sorted[0])
	agree := shared != sorted[0] || len(sorted) == 1
	for _, n := range sorted[1:] {
		if s := seriesName(n); s != shared || s == n {
			agree = false
			break
		}
	}
	// One new volume landing in a folder that already holds EPUBs joins that
	// series whatever its filename says — it's already a fact on disk.
	if agree && len(sorted) == 1 && hasExistingEPUBs(output) {
		agree = false
	}
	if agree {
		return "", nil
	}

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

// hasExistingEPUBs reports whether output already contains at least one
// ".epub" file.
func hasExistingEPUBs(output string) bool {
	matches, _ := filepath.Glob(filepath.Join(output, "*.epub"))
	return len(matches) > 0
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
