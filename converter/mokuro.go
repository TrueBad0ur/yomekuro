package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

type MokuroVolume struct {
	Version    string       `json:"version"`
	Title      string       `json:"title"`
	TitleUUID  string       `json:"title_uuid"`
	Volume     string       `json:"volume"`
	VolumeUUID string       `json:"volume_uuid"`
	Pages      []MokuroPage `json:"pages"`
}

type MokuroPage struct {
	ImgPath   string        `json:"img_path"`
	ImgWidth  int           `json:"img_width"`
	ImgHeight int           `json:"img_height"`
	Blocks    []MokuroBlock `json:"blocks"`
}

type MokuroBlock struct {
	Box      [4]int   `json:"box"`    // [x1, y1, x2, y2]
	Vertical bool     `json:"vertical"`
	FontSize float64  `json:"font_size"`
	Lines    []string `json:"lines"`
}

func runMokuro(inputDir string, volumeDirs []string, noCache bool) error {
	// When --no-cache and specific volumes are requested, delete only those caches.
	// This avoids fire CLI issues with space-containing paths as positional args.
	if noCache && len(volumeDirs) > 0 {
		for _, vd := range volumeDirs {
			// Delete the .mokuro result file — this is what mokuro checks for "already processed"
			mokuroFile := filepath.Join(inputDir, filepath.Base(vd)+".mokuro")
			if err := os.Remove(mokuroFile); err != nil && !os.IsNotExist(err) {
				slog.Warn("could not remove mokuro file", "path", mokuroFile, "err", err)
			} else {
				slog.Info("cleared mokuro cache", "path", mokuroFile)
			}
			// Also clear per-page OCR cache
			ocrDir := filepath.Join(inputDir, "_ocr", filepath.Base(vd))
			if err := os.RemoveAll(ocrDir); err != nil {
				slog.Warn("could not clear ocr dir", "path", ocrDir, "err", err)
			}
		}
	}

	args := []string{"-m", "mokuro", "--disable_confirmation", "--legacy_html=False"}
	if noCache && len(volumeDirs) == 0 {
		// global no-cache only when no specific volume selected
		args = append(args, "--no_cache")
	}
	// always use --parent_dir to avoid fire positional-arg issues with spaces in paths
	args = append(args, "--parent_dir="+inputDir)

	cmd := exec.Command("python", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	slog.Info("exec", "cmd", cmd.String())
	return cmd.Run()
}

func parseMokuroFile(path string) (MokuroVolume, error) {
	f, err := os.Open(path)
	if err != nil {
		return MokuroVolume{}, err
	}
	defer f.Close()

	var vol MokuroVolume
	if err := json.NewDecoder(f).Decode(&vol); err != nil {
		return MokuroVolume{}, err
	}
	return vol, nil
}
