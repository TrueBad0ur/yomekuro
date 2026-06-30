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
	input   := flag.String("input", "", "Directory containing manga volume subdirectories")
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

	// Determine which volumes to process
	var volumeDirs []string
	if *volume != "" {
		volumeDirs = []string{filepath.Join(*input, *volume)}
		slog.Info("running mokuro OCR on single volume", "volume", *volume)
	} else {
		slog.Info("running mokuro OCR", "input", *input)
	}

	if err := runMokuro(*input, volumeDirs, *noCache); err != nil {
		slog.Error("mokuro failed", "err", err)
		os.Exit(1)
	}

	// collect .mokuro files: either the specific one or all in input dir
	var mokuroFiles []string
	if *volume != "" {
		mokuroFiles = []string{filepath.Join(*input, *volume+".mokuro")}
	} else {
		entries, err := os.ReadDir(*input)
		if err != nil {
			slog.Error("cannot read input dir", "err", err)
			os.Exit(1)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".mokuro") {
				mokuroFiles = append(mokuroFiles, filepath.Join(*input, e.Name()))
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
		if err := buildEPUB(vol, *input, outPath); err != nil {
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
