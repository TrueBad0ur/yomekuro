package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	input   := flag.String("input", "", "Directory containing manga volume subdirectories, or a single flat directory of page images (treated as one volume)")
	output  := flag.String("output", "", "Output directory for EPUB files")
	volume  := flag.String("volume", "", "Process only this volume subdirectory name (e.g. 'Dungeon Meshi v01')")
	noCache := flag.Bool("no-cache", false, "Re-run OCR even if cached results exist")
	flag.Parse()

	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: converter --input <dir> --output <dir> [--volume <name>] [--no-cache]")
		os.Exit(1)
	}

	if err := os.MkdirAll(*output, 0755); err != nil {
		slog.Error("cannot create output dir", "err", err)
		os.Exit(1)
	}

	// mokuroDir is what --parent_dir points at, and where mokuro writes .mokuro/_ocr
	// output. volumesBaseDir is the directory whose "<volumeName>/" subfolders hold
	// the actual page images (used to build EPUBs afterward). Normally these are
	// both *input. A flat --input (images directly inside it, no volume subfolders)
	// is treated as a single volume via a throwaway symlink dir — see below.
	mokuroDir := *input
	volumesBaseDir := *input

	var volumeDirs []string
	if *volume != "" {
		volumeDirs = []string{filepath.Join(*input, *volume)}
		slog.Info("running mokuro OCR on single volume", "volume", *volume)
	} else {
		flat, err := isFlatVolume(*input)
		if err != nil {
			slog.Error("cannot read input dir", "err", err)
			os.Exit(1)
		}
		if flat {
			volumeName := filepath.Base(*input)
			absInput, err := filepath.Abs(*input)
			if err != nil {
				slog.Error("cannot resolve input path", "err", err)
				os.Exit(1)
			}
			tmpParent, err := os.MkdirTemp("", "mokuro-flat-")
			if err != nil {
				slog.Error("cannot create temp dir", "err", err)
				os.Exit(1)
			}
			defer os.RemoveAll(tmpParent)
			// python-fire (mokuro's CLI) mis-tokenizes bare positional path
			// arguments containing spaces, so we can't pass *input directly —
			// keep using --parent_dir (already spaces-safe as a single
			// --flag=value token) pointed at a symlink dir instead.
			if err := os.Symlink(absInput, filepath.Join(tmpParent, volumeName)); err != nil {
				slog.Error("cannot create flat-volume symlink", "err", err)
				os.Exit(1)
			}
			mokuroDir = tmpParent
			volumesBaseDir = tmpParent
			slog.Info("treating input as a single flat volume", "volume", volumeName)
		} else {
			slog.Info("running mokuro OCR", "input", *input)
		}
	}

	if err := runMokuro(mokuroDir, volumeDirs, *noCache); err != nil {
		slog.Error("mokuro failed", "err", err)
		os.Exit(1)
	}

	// collect .mokuro files: either the specific one or all in mokuroDir
	var mokuroFiles []string
	if *volume != "" {
		mokuroFiles = []string{filepath.Join(mokuroDir, *volume+".mokuro")}
	} else {
		entries, err := os.ReadDir(mokuroDir)
		if err != nil {
			slog.Error("cannot read input dir", "err", err)
			os.Exit(1)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mokuro") {
				mokuroFiles = append(mokuroFiles, filepath.Join(mokuroDir, e.Name()))
			}
		}
	}

	ok, fail := 0, 0
	for _, mokuroPath := range mokuroFiles {
		vol, err := parseMokuroFile(mokuroPath)
		if err != nil {
			slog.Error("parse failed", "file", mokuroPath, "err", err)
			fail++
			continue
		}

		outPath := filepath.Join(*output, vol.Volume+".epub")
		slog.Info("building EPUB", "volume", vol.Volume, "pages", len(vol.Pages))
		if err := buildEPUB(vol, volumesBaseDir, outPath); err != nil {
			slog.Error("epub build failed", "volume", vol.Volume, "err", err)
			fail++
			continue
		}
		slog.Info("done", "output", outPath)
		ok++
	}

	slog.Info("finished", "ok", ok, "failed", fail)
	if fail > 0 {
		os.Exit(1)
	}
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
		case ".jpg", ".jpeg", ".png", ".webp":
			hasImage = true
		}
	}
	return hasImage, nil
}
