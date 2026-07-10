package main

import (
	"bytes"
	"context"
	"encoding/json"
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

	// Series/SeriesIndex, set by Convert (not mokuro's own JSON — hence no
	// json tag), override the per-volume-name-derived series/index in
	// contentOPF. Used when a batch's volumes don't share a common
	// "Name vNN"-style naming pattern — see decideSeries in convert.go.
	Series      string
	SeriesIndex float64
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

// mokuroProcessingLine matches mokuro's own per-volume log line (run.py's
// "Processing i/n: path", via loguru to stderr) so onVolume can report which
// volume just started.
var mokuroProcessingLine = regexp.MustCompile(`Processing \d+/\d+: (.+)`)

// progressThrottleWriter forwards mokuro's output to dst. '\n'-terminated
// lines always pass through. '\r'-terminated segments (tqdm's redraws, which
// Docker's line-oriented log driver can't see anyway) are rewritten to real
// lines but throttled to one in every `every`, and checked against
// mokuroProcessingLine to report volume boundaries via onVolume.
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

// runMokuro runs mokuro OCR. ctx cancellation kills the subprocess
// (exec.CommandContext) — used to implement "Stop" on a running job, see
// watch.go's stop-poller goroutine — and skips the retry loop, since a
// cancellation is a deliberate stop, not a transient failure worth retrying.
func runMokuro(ctx context.Context, inputDir string, volumeDirs []string, noCache bool, onVolume func(string)) error {
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
	// tqdm's progress until the buffer fills or the process exits.
	args := []string{"-u", "-m", "mokuro", "--disable_confirmation", "--legacy_html=False"}
	if noCache && len(volumeDirs) == 0 {
		// global no-cache only when no specific volume selected
		args = append(args, "--no_cache")
	}
	// always use --parent_dir to avoid fire positional-arg issues with spaces in paths
	args = append(args, "--parent_dir="+inputDir)

	var err error
	for attempt := 1; attempt <= mokuroRetries; attempt++ {
		cmd := exec.CommandContext(ctx, "python", args...)
		cmd.Stdout = &progressThrottleWriter{dst: os.Stdout, every: progressEvery}
		cmd.Stderr = &progressThrottleWriter{dst: os.Stderr, every: progressEvery, onVolume: onVolume}
		slog.Info("exec", "cmd", cmd.String(), "attempt", attempt)
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
	return vol, nil
}
