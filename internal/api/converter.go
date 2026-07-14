package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

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

	// A standalone HTML file is already-finished content — no OCR, no archive,
	// no conversion_jobs row, just a straight copy into the library. fsnotify
	// picks it up and scans it in like any manually dropped-in .html file.
	if isHTMLFile(header.Filename) {
		s.uploadHTMLFile(w, r, lib, file, header)
		return
	}

	if !isSupportedArchive(header.Filename) && !isPDF(header.Filename) {
		respondError(w, http.StatusBadRequest, "unsupported format (need .zip/.tar/.tar.gz/.tar.xz/.7z/.rar, .pdf, or .html)")
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

	if _, err := db.CreateConversionJob(r.Context(), s.db, libID, name, inDir, outDir); err != nil {
		respondError(w, http.StatusInternalServerError, "cannot queue job: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	respond(w, map[string]string{"name": name, "status": "pending"})
}

// uploadHTMLFile copies a standalone HTML upload straight into the library
// root — no staging, no conversion_jobs row, nothing to wait for. fsnotify's
// existing watch on the library directory scans it in on its own, the same
// path a manually-dropped-in .html file already takes.
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
			UpdatedAt:     j.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	respond(w, map[string]any{"items": dtos})
}

var volNumRe = regexp.MustCompile(`\d+`)

// sortVolumeNames orders volume names by their last embedded number (e.g. "Volume
// 2" before "Volume 10"), not lexicographically — plain string sort would put
// "10" before "2". Falls back to a string compare when either name has none.
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
	// Real Japanese release filenames often use fullwidth digits (１２３, not
	// 123) — \d only matches ASCII, so normalize first or these silently fall
	// back to string sort, same gotcha noted for series numbering elsewhere.
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
	Name      string `json:"name"`
	HasImages bool   `json:"has_images"`
}

type reconvertCandidateDTO struct {
	Name       string               `json:"name"`
	Kind       string               `json:"kind"` // "epub" (folder of volumes) or "html" (one flat file)
	Volumes    []reconvertVolumeDTO `json:"volumes"`
	HasRawScan bool                 `json:"has_raw_scan"`
}

// epubHasImages reports whether a volume's EPUB has any page images at all —
// false for a plain reflowable EPUB (ranobe/HTML: born-digital text, never had
// page images to begin with), which is a different situation from a scanned
// book whose raw source images are simply gone.
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

// listReconvertable scans a library's own subfolders for "<name>" output
// folders — the converter job's actual unit of work — rather than going through
// /api/series, whose grouping is driven by EPUB metadata and need not match the
// on-disk folder name reconvert has to operate on. Every book with at least one
// built volume is listed (so page images can always be pulled back out of its
// EPUBs); HasRawScan additionally gates whether reconvert is actually possible.
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
		volumes := make([]reconvertVolumeDTO, len(names))
		for i, n := range names {
			volumes[i] = reconvertVolumeDTO{Name: n, HasImages: epubHasImages(byName[n])}
		}
		inDir := filepath.Join(lib.Path, e.Name()+"-in")
		hasRawScan := false
		if info, err := os.Stat(inDir); err == nil && info.IsDir() {
			hasRawScan = true
		}
		items = append(items, reconvertCandidateDTO{Name: e.Name(), Kind: "epub", Volumes: volumes, HasRawScan: hasRawScan})
	}

	// HTML-library books are standalone ".html" files directly in the library
	// root, not a "<name>/<volume>.epub" folder — never converted, never have a
	// raw scan, and there's exactly one file per book, so no volume list at all.
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
				{Name: name, HasImages: false},
			},
			HasRawScan: false,
		})
	}
	if items == nil {
		items = []reconvertCandidateDTO{}
	}
	respond(w, map[string]any{"items": items})
}

// reconvertSeries queues a full OCR re-run over a book/series already in the
// library, reusing the raw-scan "<name>-in" folder still on disk from its
// original conversion — a cache-reuse rebuild wouldn't pick up OCR improvements.
func (s *Server) reconvertSeries(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LibraryID string `json:"library_id"`
		Name      string `json:"name"`
		Volume    string `json:"volume"`
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

	if _, err := db.CreateReconvertJob(r.Context(), s.db, libID, name, inDir, outDir, volume); err != nil {
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
	// Only a job that never succeeded gets its input wiped — that's a disposable,
	// half-finished staging dir unique to the failed attempt. A 'done' job's
	// input_path is the book's real, shared "<name>-in" raw scan: once a book has
	// successfully converted at least once, its own row (or a later reconvert
	// row, which always has ForceOCR set) can point at that same shared path, so
	// deleting a leftover 'done' row must never delete it out from under
	// whatever else — a live reconvert included — is relying on it still being
	// there. Hit exactly this: deleting an old finished job mid-reconvert wiped
	// the folder mokuro was actively reading from.
	if inputPath != "" && !j.ForceOCR && j.Status != "done" {
		os.RemoveAll(inputPath)
	}
	w.WriteHeader(http.StatusNoContent)
}

// extractVolumeImages re-derives a raw page-image archive from an already-built
// volume's EPUB — useful when the original "-in" scan is gone (deleted, or never
// kept) but someone needs the page images back, e.g. to test the upload flow
// without re-sourcing the original scan.
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

// deleteBook removes a book from the library entirely: its output folder (all
// volumes' EPUBs) and, if present, its raw-scan "<name>-in" folder — a
// permanent, disk-level delete, not just a DB row removal. Refuses while a
// conversion job is queued or running for this name, since deleting files out
// from under a live mokuro process would corrupt or crash it.
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
