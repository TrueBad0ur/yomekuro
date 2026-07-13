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

// mokuroPollInterval controls how often Convert checks mokuroDir for newly
// finished ".mokuro" files while a multi-volume mokuro run is still in
// progress, so each volume's EPUB gets built as soon as it's ready instead
// of only after every volume in the batch finishes OCR (which, for a large
// batch, could mean nothing shows up in the library for an hour or more).
const mokuroPollInterval = 2 * time.Second

// gpuSem serializes every mokuro invocation across all callers (the DB-backed
// upload queue and the manual-folder scan now both run their own jobs
// concurrently — see watch.go — but there's still only one GPU). Work that
// never touches mokuro (JXL conversion, text-layer PDF extraction) doesn't
// need this and runs fully in parallel.
//
// It's a 1-slot channel rather than a sync.Mutex because waiting for the GPU
// has to be abandonable: a job stopped from the UI while queued behind another
// job's multi-hour OCR run would otherwise block in Lock() until that run
// finished, sitting there as "running"/"Stopping…" for the whole wait instead
// of stopping. A channel can be selected against the job's context.
var gpuSem = make(chan struct{}, 1)

// acquireGPU takes the GPU slot, or gives up if ctx is cancelled first (the job
// was stopped while queued).
func acquireGPU(ctx context.Context) error {
	select {
	case gpuSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseGPU() { <-gpuSem }

// Convert runs mokuro OCR over input (a folder of volume subdirectories, or a
// single flat folder of images treated as one volume) and writes one EPUB per
// volume into output. onVolume, if non-nil, is called with each volume's name
// as mokuro starts OCR'ing it. Cancelling ctx kills a running mokuro
// subprocess (used to implement "Stop" on a queued job — see watch.go);
// volumes already built before cancellation are kept, matching how a
// genuine mid-batch failure is handled.
func Convert(ctx context.Context, input, output, volume string, noCache bool, onVolume func(string)) (ok, fail int, err error) {
	if err := os.MkdirAll(output, 0755); err != nil {
		return 0, 0, fmt.Errorf("create output dir: %w", err)
	}

	// Decided once, upfront, from the untouched input listing — later steps
	// (JXL conversion, PDF rasterization) restructure the directory, and
	// deciding this incrementally as volumes finish would mean the answer
	// could change partway through a batch.
	var series string
	var seriesIndex map[string]float64
	if volume == "" {
		names, err := candidateVolumeNames(input)
		if err != nil {
			return 0, 0, fmt.Errorf("read input dir: %w", err)
		}
		series, seriesIndex = decideSeries(names, output)
	}

	if err := convertJXLPages(input); err != nil {
		return 0, 0, fmt.Errorf("convert jxl pages: %w", err)
	}

	pdfTextOK, err := processPDFVolumes(input, output, series, seriesIndex)
	if err != nil {
		return 0, 0, fmt.Errorf("process pdf volumes: %w", err)
	}
	ok += pdfTextOK

	// A folder of nothing but text-layer PDFs is fully handled above with no
	// image volumes left for mokuro — skip straight to done instead of
	// erroring out on an empty input dir.
	empty, err := isEmptyDir(input)
	if err != nil {
		return ok, 0, fmt.Errorf("read input dir: %w", err)
	}
	if empty {
		return ok, 0, nil
	}

	// mokuroDir is what --parent_dir points at; volumesBaseDir holds the actual
	// page images. Normally both are input — differ only for the flat-volume
	// symlink case below.
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

	// mokuro writes each volume's ".mokuro" file as soon as that volume's OCR
	// finishes, well before the whole batch's process exits — so instead of
	// waiting for runMokuro to return before building any EPUBs (which, for a
	// large batch, could mean nothing appears in the library for an hour or
	// more), poll for newly-written files while it's still running and build
	// each one's EPUB immediately.
	if err := acquireGPU(ctx); err != nil {
		return ok, fail, err
	}
	defer releaseGPU()

	mokuroErrCh := make(chan error, 1)
	go func() { mokuroErrCh <- runMokuro(ctx, mokuroDir, volumeDirs, noCache, onVolume) }()

	// Forcing a single volume's re-run leaves every other volume's ".mokuro"
	// file sitting untouched in the same dir from earlier runs — pre-mark
	// them "built" so the poll loop only ever picks up the targeted one.
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

// buildEPUBsForNewMokuroFiles scans mokuroDir for ".mokuro" files not yet in
// built, builds each one's EPUB, and marks it built regardless of success —
// a parse/build failure isn't retried on the next poll tick, matching the
// original one-pass-at-the-end behavior's error handling. series/seriesIndex,
// if series is non-empty, override the volume's own name-derived series (see
// decideSeries).
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
			// mokuro writes the file then fills it in — a parse failure here
			// may just mean it's mid-write, so don't mark it built yet and
			// retry on the next tick instead of counting it as failed.
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

// candidateVolumeNames lists the volume names input's contents will produce
// before anything is processed: top-level subdirectories (excluding mokuro's
// own "_ocr" cache) and top-level ".pdf" files, by name without extension.
// Loose image files at the top level (the flat-single-volume case) produce
// no candidates here — decideSeries's caller falls back to the output name
// for that case, same as the existing flat-volume naming already does.
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

// decideSeries looks at every volume name a job's input will produce and
// decides how to group them into one series. If they share a common
// "Name vNN"/"Name（NN）"-style naming pattern, each volume keeps its own
// name-derived series (seriesName/volumeIndex) — this preserves the common
// case where names already encode the real series ("Dungeon Meshi v01",
// "Dungeon Meshi v02"), so the returned series is "" and seriesIndex is nil,
// telling callers to fall back to per-volume derivation as before. Otherwise
// (no shared pattern — e.g. an anthology upload of differently-titled
// PDFs/subfolders, "1 Kage no koibito", "2 Kowai hanashi", ...) there's no
// per-volume series signal at all, so every volume in the batch is grouped
// under the job's own output name instead, indexed by whatever leading
// number is in its name (or its sorted position if it has none).
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
	// A single new volume landing in an output folder that already holds
	// other EPUBs — e.g. adding one more volume to an existing book via the
	// "add to existing" upload flow — belongs to that established series
	// regardless of what its own filename looks like; there's nothing to
	// "agree" with, it's already a fact on disk.
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

// isFlatVolume reports whether dir holds page images directly (a single volume)
// rather than one subdirectory per volume. "_ocr" (mokuro's own cache dir) is
// ignored so a previously-converted flat volume still detects as flat.
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

// isEmptyDir reports whether dir has nothing left for mokuro to process —
// used after processPDFVolumes to detect an input made entirely of
// text-layer PDFs, which are already fully handled by that point.
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

// convertJXLPages decodes .jxl page images to .png via djxl (libjxl-tools) and
// removes the originals — mokuro's page scanner only recognizes
// jpg/jpeg/png/webp. PNG rather than JPEG output avoids djxl's "lossless JPEG
// reconstruction" path, which fails on some real-world JXLs.
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
