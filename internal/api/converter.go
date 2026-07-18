package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/truebad0ur/yomekuro/internal/archive"
	"github.com/truebad0ur/yomekuro/internal/db"
)

const maxUploadBytes = 5 << 30 // 5GiB — raw manga scans can be large

var archiveExts = []string{".tar.gz", ".tar.xz", ".tgz", ".txz", ".tar", ".zip", ".7z", ".rar"}

// matchedArchiveExt returns the matched (possibly compound) extension, since
// filepath.Ext() would truncate ".tar.gz" down to ".gz".
func matchedArchiveExt(filename string) string {
	lower := strings.ToLower(filename)
	for _, ext := range archiveExts {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	return ""
}

func stripArchiveExt(filename string) string {
	if ext := matchedArchiveExt(filename); ext != "" {
		return filename[:len(filename)-len(ext)]
	}
	return strings.TrimSuffix(filename, filepath.Ext(filename))
}

func isSupportedArchive(filename string) bool {
	return matchedArchiveExt(filename) != ""
}

// A standalone PDF upload: no extraction, staged as-is for processPDFVolumes to
// route to OCR or direct text extraction.
func isPDF(filename string) bool {
	return strings.EqualFold(filepath.Ext(filename), ".pdf")
}

func isHTMLFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	return ext == ".html" || ext == ".htm"
}

// An "unconverted" EPUB upload: page images already packaged as an EPUB with
// no OCR text layer yet — handled like a single-volume archive.
func isEPUB(filename string) bool {
	return strings.EqualFold(filepath.Ext(filename), ".epub")
}

// backupRawScan mirrors inDir's raw, pre-OCR content into <backupDir>/<libraryName>/<name>/,
// replacing any prior backup under that name rather than mixing old and new.
func backupRawScan(backupDir, libraryName, name, inDir string) error {
	dest := filepath.Join(backupDir, libraryName, name)
	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("backup: clear old copy: %w", err)
	}
	if err := copyDir(inDir, dest); err != nil {
		os.RemoveAll(dest)
		return fmt.Errorf("backup: copy: %w", err)
	}
	return nil
}

// copyDir recursively copies src into dst, creating dst if needed.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}

// extractEPUBImages pulls page images out of an EPUB's "/images/" entries into
// destDir, renumbered page-001.<ext>, page-002.<ext>, ... in page order.
func extractEPUBImages(epubPath, destDir string) error {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return fmt.Errorf("open epub: %w", err)
	}
	defer zr.Close()

	var images []*zip.File
	for _, f := range zr.File {
		if strings.Contains(f.Name, "/images/") {
			images = append(images, f)
		}
	}
	if len(images) == 0 {
		return fmt.Errorf("no page images found in this EPUB")
	}
	sort.Slice(images, func(i, j int) bool {
		ni, oki := lastNumber(filepath.Base(images[i].Name))
		nj, okj := lastNumber(filepath.Base(images[j].Name))
		if oki && okj && ni != nj {
			return ni < nj
		}
		if oki != okj {
			return oki
		}
		return images[i].Name < images[j].Name
	})

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}
	for i, f := range images {
		ext := strings.ToLower(filepath.Ext(f.Name))
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("read %s: %w", f.Name, err)
		}
		out, err := os.Create(filepath.Join(destDir, fmt.Sprintf("page-%03d%s", i+1, ext)))
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return fmt.Errorf("write page %d: %w", i+1, copyErr)
		}
	}
	return nil
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

const (
	epubTextMinCharsPerPage     = 20
	epubTextMinJapaneseFraction = 0.3
)

// epubHasTextLayer tells an already-digitized EPUB from a raw-scan upload with
// no text yet — same thresholds as converter/pdf.go's pdfHasTextLayer.
func epubHasTextLayer(epubPath string) (bool, error) {
	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		return false, fmt.Errorf("open epub: %w", err)
	}
	defer zr.Close()

	var pageFiles []*zip.File
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		if !strings.HasSuffix(lower, ".xhtml") && !strings.HasSuffix(lower, ".html") {
			continue
		}
		if strings.Contains(lower, "nav") {
			continue // table of contents, not page content
		}
		pageFiles = append(pageFiles, f)
	}
	if len(pageFiles) == 0 {
		return false, nil
	}

	chars, jaChars := 0, 0
	for _, f := range pageFiles {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		text := htmlTagRe.ReplaceAllString(string(data), "")
		for _, r := range text {
			if unicode.IsSpace(r) {
				continue
			}
			chars++
			if isJapaneseRune(r) {
				jaChars++
			}
		}
	}
	if chars/len(pageFiles) < epubTextMinCharsPerPage {
		return false, nil
	}
	return float64(jaChars)/float64(chars) >= epubTextMinJapaneseFraction, nil
}

// Mirrors converter/pdf.go's isJapanese (separate module, no shared package).
func isJapaneseRune(r rune) bool {
	switch {
	case r >= 0x3040 && r <= 0x30FF: // Hiragana + Katakana
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK punctuation
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // Halfwidth/fullwidth forms
		return true
	default:
		return false
	}
}

// Rejects path separators and leading dots, so the "<name>-in"/"<name>" folders
// can't escape the library or look hidden.
func sanitizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty name")
	}
	if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid name")
	}
	return name, nil
}

func (s *Server) uploadArchive(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		respondError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}

	libID, ok := parseID(w, r.FormValue("library_id"))
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, libID)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		respondError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	// Already-finished content — no OCR, straight copy, fsnotify scans it in.
	if isHTMLFile(header.Filename) {
		s.uploadHTMLFile(w, r, lib, file, header)
		return
	}

	if !isSupportedArchive(header.Filename) && !isPDF(header.Filename) && !isEPUB(header.Filename) {
		respondError(w, http.StatusBadRequest, "unsupported format (need .zip/.tar/.tar.gz/.tar.xz/.7z/.rar, .pdf, .epub, or .html)")
		return
	}

	// "existing_series" switches this from staging a new book to adding a volume
	// to one already in the library — same pipeline, just a different output dir.
	existingSeries := strings.TrimSpace(r.FormValue("existing_series"))
	addToExisting := existingSeries != ""

	var name, inDir, outDir string
	if addToExisting {
		name, err = sanitizeName(existingSeries)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid book name")
			return
		}
		outDir = filepath.Join(lib.Path, name)
		if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
			respondError(w, http.StatusNotFound, "book not found in this library")
			return
		}
	} else {
		name = r.FormValue("name")
		if name == "" {
			name = stripArchiveExt(header.Filename)
		}
		name, err = sanitizeName(name)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid name")
			return
		}
		outDir = filepath.Join(lib.Path, name)
		if _, err := os.Stat(outDir); err == nil {
			respondError(w, http.StatusConflict, "a folder with this name already exists in the library")
			return
		}
		inDir = filepath.Join(lib.Path, name+"-in")
		if _, err := os.Stat(inDir); err == nil {
			respondError(w, http.StatusConflict, "a manga with this name is already uploaded")
			return
		}
	}

	// New books share one "<name>-in" staging folder, so a same-named job would
	// extract over it. Adds get their own dir, so several may queue for one book.
	if !addToExisting {
		taken, err := db.ConversionJobNameTaken(r.Context(), s.db, libID, name)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if taken {
			respondError(w, http.StatusConflict, "a conversion job with this name is already queued")
			return
		}
	}

	// Staged inside lib.Path, not /tmp, so the move into place is same-filesystem
	// and atomic. Adds keep the randomized name; new books get renamed to inDir.
	stagingPrefix := ".upload-staging-*"
	if addToExisting {
		stagingPrefix = name + "-add-*-in"
	}
	stagingDir, err := os.MkdirTemp(lib.Path, stagingPrefix)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "cannot create staging dir: "+err.Error())
		return
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			os.RemoveAll(stagingDir)
		}
	}()

	if isPDF(header.Filename) {
		// Named after the upload's own filename: in add mode `name` is the target
		// book, not this volume's title.
		volName, err := sanitizeName(stripArchiveExt(header.Filename))
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid file name")
			return
		}
		if addToExisting {
			if _, err := os.Stat(filepath.Join(outDir, volName+".epub")); err == nil {
				respondError(w, http.StatusConflict, "this book already has a volume with that name")
				return
			}
		}
		dst, err := os.Create(filepath.Join(stagingDir, volName+".pdf"))
		if err != nil {
			respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
			return
		}
		if _, err := io.Copy(dst, file); err != nil {
			dst.Close()
			respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
			return
		}
		dst.Close()
	} else if isEPUB(header.Filename) {
		volName, err := sanitizeName(stripArchiveExt(header.Filename))
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid file name")
			return
		}
		if addToExisting {
			if _, err := os.Stat(filepath.Join(outDir, volName+".epub")); err == nil {
				respondError(w, http.StatusConflict, "this book already has a volume with that name")
				return
			}
		}

		tmpEPUBPath := filepath.Join(stagingDir, volName+".epub")
		dst, err := os.Create(tmpEPUBPath)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
			return
		}
		if _, err := io.Copy(dst, file); err != nil {
			dst.Close()
			respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
			return
		}
		dst.Close()

		hasText, err := epubHasTextLayer(tmpEPUBPath)
		if err != nil {
			respondError(w, http.StatusBadRequest, "cannot read epub: "+err.Error())
			return
		}
		if hasText {
			// Already has real text — place as a finished volume, no OCR needed.
			if err := os.MkdirAll(outDir, 0755); err != nil {
				respondError(w, http.StatusInternalServerError, "cannot create book folder: "+err.Error())
				return
			}
			if err := os.Rename(tmpEPUBPath, filepath.Join(outDir, volName+".epub")); err != nil {
				respondError(w, http.StatusInternalServerError, "cannot move upload: "+err.Error())
				return
			}
			w.WriteHeader(http.StatusCreated)
			respond(w, map[string]string{"name": name, "status": "done"})
			return
		}

		// No usable text layer: treat like a raw scan, fall through to OCR queue.
		if err := extractEPUBImages(tmpEPUBPath, filepath.Join(stagingDir, volName)); err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		os.Remove(tmpEPUBPath)
	} else {
		tmpArchive, err := os.CreateTemp("", "upload-*"+matchedArchiveExt(header.Filename))
		if err != nil {
			respondError(w, http.StatusInternalServerError, "cannot buffer upload")
			return
		}
		defer os.Remove(tmpArchive.Name())
		if _, err := io.Copy(tmpArchive, file); err != nil {
			tmpArchive.Close()
			respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
			return
		}
		tmpArchive.Close()

		if err := archive.Extract(tmpArchive.Name(), stagingDir); err != nil {
			respondError(w, http.StatusBadRequest, "extraction failed: "+err.Error())
			return
		}
		if !containsSupportedContent(stagingDir) {
			respondError(w, http.StatusBadRequest, "archive contains no page images or PDFs")
			return
		}
	}

	if addToExisting {
		// The randomized staging dir is already the final input path: outDir exists
		// and there's no "<name>-in" to rename to.
		inDir = stagingDir
		keepStaging = true
	} else if err := os.Rename(stagingDir, inDir); err != nil {
		respondError(w, http.StatusInternalServerError, "cannot move extracted files: "+err.Error())
		return
	}

	// Best-effort backup mirror — never blocks or fails the upload.
	if s.backupDir != "" {
		if err := backupRawScan(s.backupDir, lib.Name, name, inDir); err != nil {
			slog.Warn("backup raw scan failed", "name", name, "err", err)
		}
	}

	if _, err := db.CreateConversionJob(r.Context(), s.db, libID, name, inDir, outDir); err != nil {
		respondError(w, http.StatusInternalServerError, "cannot queue job: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]string{"name": name, "status": "pending"})
}

// uploadHTMLFile copies a standalone HTML upload straight into the library
// root — no staging, no conversion_jobs row; fsnotify scans it in on its own.
func (s *Server) uploadHTMLFile(w http.ResponseWriter, r *http.Request, lib db.Library, file multipart.File, header *multipart.FileHeader) {
	name := r.FormValue("name")
	if name == "" {
		name = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}
	name, err := sanitizeName(name)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid name")
		return
	}

	destPath := filepath.Join(lib.Path, name+".html")
	if _, err := os.Stat(destPath); err == nil {
		respondError(w, http.StatusConflict, "a file with this name already exists in the library")
		return
	}

	dst, err := os.Create(destPath)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
		return
	}
	defer dst.Close()
	if _, err := io.Copy(dst, file); err != nil {
		respondError(w, http.StatusInternalServerError, "cannot save upload: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]string{"name": name, "status": "done"})
}

func containsSupportedContent(dir string) bool {
	found := false
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(d.Name())) {
		case ".jpg", ".jpeg", ".png", ".webp", ".jxl", ".pdf":
			found = true
		}
		return nil
	})
	return found
}

type conversionJobDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	LibraryID     string `json:"library_id"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	CurrentVolume string `json:"current_volume,omitempty"`
	StopRequested bool   `json:"stop_requested,omitempty"`
	ForceOCR      bool   `json:"force_ocr,omitempty"`
	Volume        string `json:"volume,omitempty"`
	DetectorSize  int    `json:"detector_size,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

func (s *Server) listConversionJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := db.ListConversionJobs(r.Context(), s.db)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dtos := make([]conversionJobDTO, len(jobs))
	for i, j := range jobs {
		dtos[i] = conversionJobDTO{
			ID:            db.UUIDString(j.ID),
			Name:          j.Name,
			LibraryID:     db.UUIDString(j.LibraryID),
			Status:        j.Status,
			Error:         j.Error,
			CurrentVolume: j.CurrentVolume,
			StopRequested: j.StopRequested,
			ForceOCR:      j.ForceOCR,
			Volume:        j.Volume,
			DetectorSize:  j.DetectorSize,
			UpdatedAt:     j.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	respond(w, map[string]any{"items": dtos})
}

var volNumRe = regexp.MustCompile(`\d+`)

// sortVolumeNames orders by last embedded number, not lexicographically.
func sortVolumeNames(names []string) {
	sort.Slice(names, func(i, j int) bool {
		ni, oki := lastNumber(names[i])
		nj, okj := lastNumber(names[j])
		if oki && okj && ni != nj {
			return ni < nj
		}
		if oki != okj {
			return oki
		}
		return names[i] < names[j]
	})
}

func lastNumber(name string) (int, bool) {
	// Fullwidth digits (１２３) don't match \d — normalize to ASCII first.
	halfwidth := strings.Map(func(r rune) rune {
		if r >= '０' && r <= '９' {
			return r - '０' + '0'
		}
		return r
	}, name)
	matches := volNumRe.FindAllString(halfwidth, -1)
	if len(matches) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(matches[len(matches)-1])
	if err != nil {
		return 0, false
	}
	return n, true
}

type reconvertVolumeDTO struct {
	Name           string `json:"name"`
	HasImages      bool   `json:"has_images"`
	ModifiedAt     string `json:"modified_at,omitempty"`
	NeedsReconvert bool   `json:"needs_reconvert,omitempty"`
}

// rawScanNewerThanEPUB flags a hand-edited raw scan as stale even though OCR
// never re-ran. Directory mtimes count too: renaming a file in place doesn't
// touch its own mtime, but does touch its parent directory's.
func rawScanNewerThanEPUB(volRawDir string, epubModTime time.Time) bool {
	info, err := os.Stat(volRawDir)
	if err != nil || !info.IsDir() {
		return false
	}
	newer := false
	filepath.WalkDir(volRawDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || newer {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.ModTime().After(epubModTime) {
			newer = true
		}
		return nil
	})
	return newer
}

// fileModTime returns a file's mtime (RFC3339, UTC) — for a converter-produced
// EPUB, effectively "last analyzed".
func fileModTime(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}

type reconvertCandidateDTO struct {
	Name       string               `json:"name"`
	Kind       string               `json:"kind"` // "epub" (folder of volumes) or "html" (one flat file)
	Volumes    []reconvertVolumeDTO `json:"volumes"`
	HasRawScan bool                 `json:"has_raw_scan"`
}

// epubHasImages is false for a plain reflowable (born-digital) EPUB — distinct
// from a scanned book whose raw source images are simply gone.
func epubHasImages(path string) bool {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer zr.Close()
	for _, f := range zr.File {
		if strings.Contains(f.Name, "/images/") {
			return true
		}
	}
	return false
}

// listReconvertable scans on-disk "<name>" output folders directly, not
// /api/series, since reconvert operates on folder names, not EPUB metadata.
func (s *Server) listReconvertable(w http.ResponseWriter, r *http.Request) {
	libID, ok := parseID(w, r.URL.Query().Get("library"))
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, libID)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}

	entries, err := os.ReadDir(lib.Path)
	if err != nil {
		respond(w, map[string]any{"items": []reconvertCandidateDTO{}})
		return
	}

	var items []reconvertCandidateDTO
	for _, e := range entries {
		if !e.IsDir() || strings.HasSuffix(e.Name(), "-in") {
			continue
		}
		epubs, _ := filepath.Glob(filepath.Join(lib.Path, e.Name(), "*.epub"))
		if len(epubs) == 0 {
			continue
		}
		names := make([]string, len(epubs))
		byName := make(map[string]string, len(epubs))
		for i, ep := range epubs {
			n := strings.TrimSuffix(filepath.Base(ep), ".epub")
			names[i] = n
			byName[n] = ep
		}
		sortVolumeNames(names)
		inDir := filepath.Join(lib.Path, e.Name()+"-in")
		hasRawScan := false
		if info, err := os.Stat(inDir); err == nil && info.IsDir() {
			hasRawScan = true
		}
		volumes := make([]reconvertVolumeDTO, len(names))
		for i, n := range names {
			needsReconvert := false
			if hasRawScan {
				if epubInfo, err := os.Stat(byName[n]); err == nil {
					needsReconvert = rawScanNewerThanEPUB(filepath.Join(inDir, n), epubInfo.ModTime())
				}
			}
			volumes[i] = reconvertVolumeDTO{Name: n, HasImages: epubHasImages(byName[n]), ModifiedAt: fileModTime(byName[n]), NeedsReconvert: needsReconvert}
		}
		items = append(items, reconvertCandidateDTO{Name: e.Name(), Kind: "epub", Volumes: volumes, HasRawScan: hasRawScan})
	}

	// HTML-library books: one standalone .html file each, never converted.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		lower := strings.ToLower(e.Name())
		if !strings.HasSuffix(lower, ".html") && !strings.HasSuffix(lower, ".htm") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		items = append(items, reconvertCandidateDTO{
			Name: name,
			Kind: "html",
			Volumes: []reconvertVolumeDTO{
				{Name: name, HasImages: false, ModifiedAt: fileModTime(filepath.Join(lib.Path, e.Name()))},
			},
			HasRawScan: false,
		})
	}
	if items == nil {
		items = []reconvertCandidateDTO{}
	}
	respond(w, map[string]any{"items": items})
}

// reconvertSeries queues a full OCR re-run reusing the "<name>-in" raw scan
// still on disk — a cache-reuse rebuild wouldn't pick up OCR improvements.
func (s *Server) reconvertSeries(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LibraryID    string `json:"library_id"`
		Name         string `json:"name"`
		Volume       string `json:"volume"`
		DetectorSize int    `json:"detector_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// Only these two are UI-offered; 4096 OOMs the detector on this GPU's 8GB VRAM.
	detectorSize := 3072
	if req.DetectorSize == 2048 || req.DetectorSize == 3584 {
		detectorSize = req.DetectorSize
	}
	libID, ok := parseID(w, req.LibraryID)
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, libID)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}
	name, err := sanitizeName(req.Name)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid name")
		return
	}

	outDir := filepath.Join(lib.Path, name)
	if info, err := os.Stat(outDir); err != nil || !info.IsDir() {
		respondError(w, http.StatusNotFound, "book not found in this library")
		return
	}
	inDir := filepath.Join(lib.Path, name+"-in")
	if info, err := os.Stat(inDir); err != nil || !info.IsDir() {
		respondError(w, http.StatusConflict, "raw scan data no longer available — re-upload to reconvert")
		return
	}

	volume := ""
	if req.Volume != "" {
		volume, err = sanitizeName(req.Volume)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid volume name")
			return
		}
		if _, err := os.Stat(filepath.Join(outDir, volume+".epub")); err != nil {
			respondError(w, http.StatusNotFound, "volume not found in this book")
			return
		}
	}

	taken, err := db.ConversionJobNameTaken(r.Context(), s.db, libID, name)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if taken {
		respondError(w, http.StatusConflict, "a conversion job with this name is already queued")
		return
	}

	if _, err := db.CreateReconvertJob(r.Context(), s.db, libID, name, inDir, outDir, volume, detectorSize); err != nil {
		respondError(w, http.StatusInternalServerError, "cannot queue job: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]string{"name": name, "status": "pending"})
}

// Stops or removes a job by state: a running one is only flagged (deleting its
// files would race the live mokuro), anything terminal is deleted outright.
func (s *Server) deleteConversionJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	j, err := db.GetConversionJob(r.Context(), s.db, id)
	if err != nil {
		respondError(w, http.StatusNotFound, "job not found")
		return
	}

	if j.Status == "running" {
		if err := db.RequestStopConversionJob(r.Context(), s.db, id); err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
		return
	}

	inputPath, _, err := db.DeleteConversionJob(r.Context(), s.db, id)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Only a never-succeeded job's input is disposable; wiping a 'done'/'paused'
	// job's shared raw scan has deleted a folder a live reconvert was reading.
	if inputPath != "" && !j.ForceOCR && j.Status != "done" && j.Status != "paused" {
		os.RemoveAll(inputPath)
	}
	w.WriteHeader(http.StatusNoContent)
}

// pauseQueue pauses every queued job except the one actively converting —
// unlike Stop, this never touches any file.
func (s *Server) pauseQueue(w http.ResponseWriter, r *http.Request) {
	n, err := db.PauseQueue(r.Context(), s.db)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, map[string]any{"affected": n})
}

func (s *Server) resumeQueue(w http.ResponseWriter, r *http.Request) {
	n, err := db.ResumeQueue(r.Context(), s.db)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respond(w, map[string]any{"affected": n})
}

// extractVolumeImages re-derives raw page images from a built EPUB — useful
// when the original "-in" scan is gone.
func (s *Server) extractVolumeImages(w http.ResponseWriter, r *http.Request) {
	libID, ok := parseID(w, r.URL.Query().Get("library"))
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, libID)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}
	name, err := sanitizeName(r.URL.Query().Get("name"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid name")
		return
	}
	volume, err := sanitizeName(r.URL.Query().Get("volume"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid volume")
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "images"
	}

	// HTML-library book: a single standalone file directly in the library root,
	// not a "<name>/<volume>.epub" folder — nothing to extract, just serve it.
	if r.URL.Query().Get("kind") == "html" {
		htmlPath := filepath.Join(lib.Path, name+".html")
		if _, err := os.Stat(htmlPath); err != nil {
			htmlPath = filepath.Join(lib.Path, name+".htm")
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.html"`, name))
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, htmlPath)
		return
	}

	epubPath := filepath.Join(lib.Path, name, volume+".epub")

	// The original EPUB itself — no extraction needed, just serve the file.
	if format == "epub" {
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.epub"`, volume))
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, epubPath)
		return
	}

	if format != "images" && format != "pdf" {
		respondError(w, http.StatusBadRequest, "format must be images, pdf, or epub")
		return
	}

	zr, err := zip.OpenReader(epubPath)
	if err != nil {
		respondError(w, http.StatusNotFound, "volume not found")
		return
	}
	defer zr.Close()

	var images []*zip.File
	for _, f := range zr.File {
		if strings.Contains(f.Name, "/images/") {
			images = append(images, f)
		}
	}
	if len(images) == 0 {
		respondError(w, http.StatusNotFound, "no page images found in this volume")
		return
	}
	// The zip's own central-directory order isn't guaranteed to be page order —
	// sort by the number embedded in each filename (page-01.jpg, page-02.jpg, ...).
	sort.Slice(images, func(i, j int) bool {
		ni, oki := lastNumber(filepath.Base(images[i].Name))
		nj, okj := lastNumber(filepath.Base(images[j].Name))
		if oki && okj && ni != nj {
			return ni < nj
		}
		if oki != okj {
			return oki
		}
		return images[i].Name < images[j].Name
	})

	if format == "images" {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, volume))
		w.Header().Set("Cache-Control", "no-store")

		zw := zip.NewWriter(w)
		defer zw.Close()
		for _, f := range images {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			out, err := zw.Create(filepath.Base(f.Name))
			if err == nil {
				io.Copy(out, rc)
			}
			rc.Close()
		}
		return
	}

	// format == "pdf"
	pages := make([]pdfPage, 0, len(images))
	for _, f := range images {
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		jpegData, w2, h2, err := imageToJPEG(data)
		if err != nil {
			continue
		}
		pages = append(pages, pdfPage{jpegData: jpegData, width: w2, height: h2})
	}
	if len(pages) == 0 {
		respondError(w, http.StatusInternalServerError, "could not decode any page image")
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.pdf"`, volume))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(buildImagesPDF(pages))
}

// deleteBook permanently removes a book's output and raw-scan folders. Refuses
// while a conversion job is queued/running for this name.
func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request) {
	libID, ok := parseID(w, r.URL.Query().Get("library"))
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, libID)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}
	name, err := sanitizeName(r.URL.Query().Get("name"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid name")
		return
	}

	// HTML-library book: a single standalone file, nothing else to clean up.
	if r.URL.Query().Get("kind") == "html" {
		htmlPath := filepath.Join(lib.Path, name+".html")
		if _, err := os.Stat(htmlPath); err != nil {
			htmlPath = filepath.Join(lib.Path, name+".htm")
		}
		db.DeleteBookByPath(r.Context(), s.db, htmlPath)
		if err := os.Remove(htmlPath); err != nil && !os.IsNotExist(err) {
			respondError(w, http.StatusInternalServerError, "failed to delete file: "+err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	taken, err := db.ConversionJobNameTaken(r.Context(), s.db, libID, name)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if taken {
		respondError(w, http.StatusConflict, "a conversion job is queued or running for this book — stop/remove it first")
		return
	}

	// A single volume, not the whole book — leave its siblings (and the
	// shared "-in" scan folder) alone, only drop this one volume's own files.
	if volParam := r.URL.Query().Get("volume"); volParam != "" {
		volume, err := sanitizeName(volParam)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid volume")
			return
		}
		epubPath := filepath.Join(lib.Path, name, volume+".epub")
		if _, err := os.Stat(epubPath); err != nil {
			respondError(w, http.StatusNotFound, "volume not found")
			return
		}
		db.DeleteBookByPath(r.Context(), s.db, epubPath)
		if err := os.Remove(epubPath); err != nil {
			respondError(w, http.StatusInternalServerError, "failed to delete volume: "+err.Error())
			return
		}

		inDir := filepath.Join(lib.Path, name+"-in")
		os.RemoveAll(filepath.Join(inDir, volume))
		os.Remove(filepath.Join(inDir, volume+".mokuro"))
		os.RemoveAll(filepath.Join(inDir, "_ocr", volume))

		w.WriteHeader(http.StatusNoContent)
		return
	}

	outDir := filepath.Join(lib.Path, name)
	inDir := filepath.Join(lib.Path, name+"-in")

	epubs, _ := filepath.Glob(filepath.Join(outDir, "*.epub"))
	for _, ep := range epubs {
		db.DeleteBookByPath(r.Context(), s.db, ep)
	}

	if err := os.RemoveAll(outDir); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to delete book folder: "+err.Error())
		return
	}
	os.RemoveAll(inDir)

	w.WriteHeader(http.StatusNoContent)
}

// renameBook overrides books.series_name in the DB only — never the file or
// folders, which keep their original name everywhere else.
func (s *Server) renameBook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LibraryID   string `json:"library_id"`
		Name        string `json:"name"`
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	libID, ok := parseID(w, req.LibraryID)
	if !ok {
		return
	}
	lib, err := db.GetLibraryByID(r.Context(), s.db, libID)
	if err != nil {
		respondError(w, http.StatusNotFound, "library not found")
		return
	}
	name, err := sanitizeName(req.Name)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid name")
		return
	}
	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		respondError(w, http.StatusBadRequest, "display name cannot be empty")
		return
	}

	pathOrDir := filepath.Join(lib.Path, name)
	if req.Kind == "html" {
		pathOrDir = filepath.Join(lib.Path, name+".html")
		if _, err := os.Stat(pathOrDir); err != nil {
			pathOrDir = filepath.Join(lib.Path, name+".htm")
		}
	} else if info, err := os.Stat(pathOrDir); err != nil || !info.IsDir() {
		respondError(w, http.StatusNotFound, "book not found in this library")
		return
	}

	n, err := db.RenameSeriesUnderPath(r.Context(), s.db, libID, pathOrDir, displayName)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		respondError(w, http.StatusNotFound, "no books found for this name — try scanning the library first")
		return
	}

	respond(w, map[string]string{"display_name": displayName})
}
