package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// mokuroRetries, mokuroRetryDelay, and progressEvery are defined in config.go
// (env-configurable — see there for what each one does).

type MokuroVolume struct {
	Version    string       `json:"version"`
	Title      string       `json:"title"`
	TitleUUID  string       `json:"title_uuid"`
	Volume     string       `json:"volume"`
	VolumeUUID string       `json:"volume_uuid"`
	Pages      []MokuroPage `json:"pages"`

	// Set by Convert (not mokuro's JSON, hence no tag): overrides the name-derived
	// series when a batch's volumes share no "Name vNN" pattern. See decideSeries.
	Series      string
	SeriesIndex float64

	// Quads came from mokuro's detector, not pdftotext: the two pad differently,
	// so writeLineDiv only applies its calibrated offsets when this is set.
	OCR bool
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

// Matches mokuro's own "Processing i/n: path" log line, so onVolume can report
// which volume just started.
var mokuroProcessingLine = regexp.MustCompile(`Processing \d+/\d+: (.+)`)

// Forwards mokuro's output to dst. '\r' segments (tqdm redraws, invisible to
// Docker's line-oriented logger) become real lines, throttled to one in `every`.
type progressThrottleWriter struct {
	dst      *os.File
	every    int
	onVolume func(string)
	buf      []byte
	crCount  int
}

func (t *progressThrottleWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexAny(t.buf, "\r\n")
		if i < 0 {
			break
		}
		line := t.buf[:i]
		sep := t.buf[i]
		t.buf = t.buf[i+1:]

		if sep == '\n' {
			if t.onVolume != nil {
				if m := mokuroProcessingLine.FindSubmatch(line); m != nil {
					t.onVolume(filepath.Base(strings.TrimSpace(string(m[1]))))
				}
			}
			if err := t.emit(line); err != nil {
				return 0, err
			}
			continue
		}

		// '\r'-terminated: a tqdm redraw — sample every Nth one, drop the rest.
		t.crCount++
		if t.every <= 1 || t.crCount%t.every == 0 {
			if err := t.emit(line); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}

func (t *progressThrottleWriter) emit(line []byte) error {
	if _, err := t.dst.Write(line); err != nil {
		return err
	}
	_, err := t.dst.Write([]byte{'\n'})
	return err
}

// Runs mokuro OCR. Cancelling ctx kills the subprocess (that's how Stop works)
// and skips the retry loop — a stop is deliberate, not a transient failure.
func runMokuro(ctx context.Context, inputDir string, volumeDirs []string, noCache bool, detectorSize int, onVolume func(string)) error {
	// --no-cache for specific volumes: clear just their cached results, since
	// mokuro checks the .mokuro file's existence to skip "already processed".
	if noCache && len(volumeDirs) > 0 {
		for _, vd := range volumeDirs {
			mokuroFile := filepath.Join(inputDir, filepath.Base(vd)+".mokuro")
			if err := os.Remove(mokuroFile); err != nil && !os.IsNotExist(err) {
				slog.Warn("could not remove mokuro file", "path", mokuroFile, "err", err)
			} else {
				slog.Info("cleared mokuro cache", "path", mokuroFile)
			}
			ocrDir := filepath.Join(inputDir, "_ocr", filepath.Base(vd))
			if err := os.RemoveAll(ocrDir); err != nil {
				slog.Warn("could not clear ocr dir", "path", ocrDir, "err", err)
			}
		}
	}

	// -u: Python fully-buffers stdout once it's not a TTY, which would hide
	// tqdm's progress until the buffer fills or the process exits. mokuro_run.py
	// (not "-m mokuro" directly) applies our higher-quality detector settings —
	// see its own comment for why.
	args := []string{"-u", "/mokuro_run.py", "--disable_confirmation", "--legacy_html=False"}
	if noCache && len(volumeDirs) == 0 {
		// global no-cache only when no specific volume selected
		args = append(args, "--no_cache")
	}
	// always use --parent_dir to avoid fire positional-arg issues with spaces in paths
	args = append(args, "--parent_dir="+inputDir)

	var err error
	for attempt := 1; attempt <= mokuroRetries; attempt++ {
		cmd := exec.CommandContext(ctx, "python", args...)
		// mokuro_run.py reads this to pick MokuroGenerator's detector_input_size —
		// see its own comment for why this is configurable per job rather than fixed.
		cmd.Env = append(os.Environ(), fmt.Sprintf("MOKURO_DETECTOR_INPUT_SIZE=%d", detectorSize))
		cmd.Stdout = &progressThrottleWriter{dst: os.Stdout, every: progressEvery}
		cmd.Stderr = &progressThrottleWriter{dst: os.Stderr, every: progressEvery, onVolume: onVolume}
		slog.Info("exec", "cmd", cmd.String(), "attempt", attempt, "detector_size", detectorSize)
		if err = cmd.Run(); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			// Cancelled (Stop requested) — a deliberate stop, not a transient
			// failure, so don't burn through retries waiting it out.
			return ctx.Err()
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
	vol.OCR = true
	return vol, nil
}
