package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Convert runs mokuro OCR over input (a folder of volume subdirectories, or a
// single flat folder of images treated as one volume) and writes one EPUB per
// volume into output. onVolume, if non-nil, is called with each volume's name
// as mokuro starts OCR'ing it.
func Convert(input, output, volume string, noCache bool, onVolume func(string)) (ok, fail int, err error) {
	if err := os.MkdirAll(output, 0755); err != nil {
		return 0, 0, fmt.Errorf("create output dir: %w", err)
	}

	if err := convertJXLPages(input); err != nil {
		return 0, 0, fmt.Errorf("convert jxl pages: %w", err)
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
			return 0, 0, fmt.Errorf("read input dir: %w", err)
		}
		if flat {
			// Name after output, not input — input is "<name>-in" for uploads,
			// output is the clean "<name>".
			volumeName := filepath.Base(output)
			absInput, err := filepath.Abs(input)
			if err != nil {
				return 0, 0, fmt.Errorf("resolve input path: %w", err)
			}
			tmpParent, err := os.MkdirTemp("", "mokuro-flat-")
			if err != nil {
				return 0, 0, fmt.Errorf("create temp dir: %w", err)
			}
			defer os.RemoveAll(tmpParent)
			// python-fire mis-tokenizes paths with spaces as positional args, so
			// symlink into a temp dir and point --parent_dir at that instead.
			if err := os.Symlink(absInput, filepath.Join(tmpParent, volumeName)); err != nil {
				return 0, 0, fmt.Errorf("create flat-volume symlink: %w", err)
			}
			mokuroDir = tmpParent
			volumesBaseDir = tmpParent
			slog.Info("treating input as a single flat volume", "volume", volumeName)
		} else {
			slog.Info("running mokuro OCR", "input", input)
		}
	}

	if err := runMokuro(mokuroDir, volumeDirs, noCache, onVolume); err != nil {
		return 0, 0, fmt.Errorf("mokuro: %w", err)
	}

	// collect .mokuro files: either the specific one or all in mokuroDir
	var mokuroFiles []string
	if volume != "" {
		mokuroFiles = []string{filepath.Join(mokuroDir, volume+".mokuro")}
	} else {
		entries, err := os.ReadDir(mokuroDir)
		if err != nil {
			return 0, 0, fmt.Errorf("read input dir: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mokuro") {
				mokuroFiles = append(mokuroFiles, filepath.Join(mokuroDir, e.Name()))
			}
		}
	}

	for _, mokuroPath := range mokuroFiles {
		vol, err := parseMokuroFile(mokuroPath)
		if err != nil {
			slog.Error("parse failed", "file", mokuroPath, "err", err)
			fail++
			continue
		}

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

	return ok, fail, nil
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
