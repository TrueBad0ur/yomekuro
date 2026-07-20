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

	shared "github.com/truebad0ur/yomekuro-shared"
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
// reconcileOCRCache removes stale entries from "_ocr/<volume>/" left over from
// a previous OCR pass whose raw images have since been added, removed, or
// renamed on disk. mokuro's own generate_mokuro_file() globs the entire cache
// dir unconditionally and looks up each cached page against a fresh image
// scan — a leftover entry with no matching image throws an uncaught KeyError
// and crashes that volume's build. Hit for real: a volume's cover image was
// replaced by hand (e.g. to fix a missing/blank one) after its first OCR
// pass, leaving the old cache entry orphaned. Cheap no-op for every volume
// that hasn't changed, so safe to run unconditionally before every mokuro
// invocation, not just on an explicit single-volume reconvert.
func reconcileOCRCache(mokuroDir string) {
	entries, err := os.ReadDir(mokuroDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "_ocr" {
			continue
		}
		volDir := filepath.Join(mokuroDir, e.Name())
		cacheDir := filepath.Join(mokuroDir, "_ocr", e.Name())
		if info, err := os.Stat(cacheDir); err != nil || !info.IsDir() {
			continue
		}

		current := map[string]bool{}
		filepath.WalkDir(volDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !shared.IsImageExt(filepath.Ext(path)) {
				return nil
			}
			if rel, err := filepath.Rel(volDir, path); err == nil {
				current[strings.TrimSuffix(rel, filepath.Ext(rel))] = true
			}
			return nil
		})

		filepath.WalkDir(cacheDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
				return nil
			}
			rel, err := filepath.Rel(cacheDir, path)
			if err != nil || current[strings.TrimSuffix(rel, ".json")] {
				return nil
			}
			if rmErr := os.Remove(path); rmErr == nil {
				slog.Info("removed stale OCR cache entry", "path", path)
			}
			return nil
		})

		reconcileStaleMokuroFile(mokuroDir, e.Name(), current)
	}
}

// reconcileStaleMokuroFile drops page entries from an existing "<volume>.mokuro"
// file whose image no longer exists on disk. mokuro's own process_volume() reads
// this file *first* and re-seeds the per-page OCR cache from its embedded page
// data before checking what's actually on disk (see mokuro_generator.py) — so a
// stale .mokuro file undoes the cache cleanup right above, recreating exactly
// the entries just removed, and generate_mokuro_file() then throws an uncaught
// KeyError trying to match a phantom entry against the current (smaller) set of
// images. Hit for real: BE BLUES v14 shrank from 196 to 193 pages between OCR
// passes (raw scan cleanup), leaving 3 stale pages in the old .mokuro file.
func reconcileStaleMokuroFile(mokuroDir, volume string, current map[string]bool) {
	path := filepath.Join(mokuroDir, volume+".mokuro")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return
	}
	pages, ok := doc["pages"].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(pages))
	dropped := 0
	for _, p := range pages {
		page, ok := p.(map[string]any)
		if !ok {
			kept = append(kept, p)
			continue
		}
		imgPath, _ := page["img_path"].(string)
		key := strings.TrimSuffix(imgPath, filepath.Ext(imgPath))
		if imgPath == "" || current[key] {
			kept = append(kept, p)
		} else {
			dropped++
		}
	}
	if dropped == 0 {
		return
	}
	doc["pages"] = kept
	out, err := json.Marshal(doc)
	if err != nil {
		return
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		slog.Warn("could not rewrite stale mokuro file", "path", path, "err", err)
		return
	}
	slog.Info("removed stale pages from mokuro file", "path", path, "dropped", dropped)
}

func runMokuro(ctx context.Context, inputDir string, volumeDirs []string, noCache bool, detectorSize int, onVolume func(string)) error {
	// --no-cache for specific volumes: clear just their cached results, since
	// mokuro checks the .mokuro file's existence to skip "already processed".
	if noCache && len(volumeDirs) > 0 {
		for _, vd := range volumeDirs {
			mokuroFile := filepath.Join(inputDir, filepath.Base(vd)+".mokuro")
			if err := os.Remove(mokuroFile); err == nil {
				slog.Info("cleared mokuro cache", "path", mokuroFile)
			} else if os.IsNotExist(err) {
				slog.Debug("mokuro cache already absent", "path", mokuroFile)
			} else {
				slog.Warn("could not remove mokuro file", "path", mokuroFile, "err", err)
			}
			ocrDir := filepath.Join(inputDir, "_ocr", filepath.Base(vd))
			if err := os.RemoveAll(ocrDir); err != nil {
				slog.Warn("could not clear ocr dir", "path", ocrDir, "err", err)
			}
		}
	}

	// -u: unbuffered, so tqdm's progress isn't hidden once stdout isn't a TTY.
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
