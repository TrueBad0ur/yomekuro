package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// mokuroRetries/mokuroRetryDelay bound retrying the whole mokuro invocation
// on failure — mokuro lazily downloads model weights (e.g. comictextdetector.pt)
// on first use, and a transient DNS/network blip during that download fails
// the entire run even though nothing is actually wrong. Safe to just retry:
// mokuro skips any volume that already has a .mokuro result file, so a retry
// after a partial run only redoes what didn't finish, never reprocesses
// already-successful volumes.
const (
	mokuroRetries    = 3
	mokuroRetryDelay = 5 * time.Second
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
	Box         [4]int        `json:"box"` // [x1, y1, x2, y2] — bounding box of all lines
	Vertical    bool          `json:"vertical"`
	FontSize    float64       `json:"font_size"`
	LinesCoords [][][]float64 `json:"lines_coords"` // per-line quadrilateral: [line][point][x,y]
	Lines       []string      `json:"lines"`        // one entry per line/column, index-aligned with LinesCoords
}

// mokuroProcessingLine matches mokuro's own per-volume log line (run.py:
// `logger.info(f"Processing {i + 1}/{len(vc)}: {volume.path_in}")`, emitted
// via loguru to stderr) so onVolume can be told which volume just started
// without mokuro needing any awareness of this project's job tracking.
var mokuroProcessingLine = regexp.MustCompile(`Processing \d+/\d+: (.+)`)

// volumeLogWriter forwards mokuro's stderr through unchanged (so it still
// shows up in container logs) while watching line-by-line for volume
// boundaries to report via onVolume.
type volumeLogWriter struct {
	buf      []byte
	onVolume func(string)
}

func (w *volumeLogWriter) Write(p []byte) (int, error) {
	os.Stderr.Write(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		if m := mokuroProcessingLine.FindSubmatch(line); m != nil {
			w.onVolume(filepath.Base(strings.TrimSpace(string(m[1]))))
		}
	}
	return len(p), nil
}

func runMokuro(inputDir string, volumeDirs []string, noCache bool, onVolume func(string)) error {
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

	// -u: Python fully-buffers stdout by default once it's not a TTY (i.e. always,
	// under Docker/exec.Command) — tqdm's page-progress bar then only actually
	// reaches the log once its buffer fills or the process exits, making a 6-minute
	// OCR run look completely frozen until it's done. Unbuffered makes every write
	// (including tqdm's \r-updates) show up live.
	args := []string{"-u", "-m", "mokuro", "--disable_confirmation", "--legacy_html=False"}
	if noCache && len(volumeDirs) == 0 {
		// global no-cache only when no specific volume selected
		args = append(args, "--no_cache")
	}
	// always use --parent_dir to avoid fire positional-arg issues with spaces in paths
	args = append(args, "--parent_dir="+inputDir)

	var err error
	for attempt := 1; attempt <= mokuroRetries; attempt++ {
		cmd := exec.Command("python", args...)
		cmd.Stdout = os.Stdout
		if onVolume != nil {
			cmd.Stderr = &volumeLogWriter{onVolume: onVolume}
		} else {
			cmd.Stderr = os.Stderr
		}
		slog.Info("exec", "cmd", cmd.String(), "attempt", attempt)
		if err = cmd.Run(); err == nil {
			return nil
		}
		if attempt < mokuroRetries {
			slog.Warn("mokuro failed, retrying", "attempt", attempt, "err", err)
			time.Sleep(mokuroRetryDelay)
		}
	}
	return err
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
