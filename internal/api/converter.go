package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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

// isPDF reports whether filename is a standalone PDF upload — handled
// separately from archives: no extraction, just staged as-is for
// processPDFVolumes (converter/pdf.go) to route to OCR or direct text
// extraction depending on whether it already has a text layer.
func isPDF(filename string) bool {
	return strings.EqualFold(filepath.Ext(filename), ".pdf")
}

// sanitizeName rejects path separators and leading dots so the resulting
// "<name>-in"/"<name>" folders can't escape the library or collide with the
// hidden-file convention.
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

	if !isSupportedArchive(header.Filename) && !isPDF(header.Filename) {
		respondError(w, http.StatusBadRequest, "unsupported format (need .zip/.tar/.tar.gz/.tar.xz/.7z/.rar or .pdf)")
		return
	}

	// "existing_series" switches this from "stage a new book" to "add one
	// more volume/PDF to a book that's already in the library" — same
	// pipeline either way (extract-or-stage, queue, convert), just landing
	// in the existing output folder instead of a fresh one. See
	// converter/convert.go's decideSeries/hasExistingEPUBs: a single new
	// volume landing in a folder that already has other EPUBs in it
	// automatically joins that folder's series, no extra flag needed there.
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

	// Only new books need this: their staging folder is "<name>-in", so a second
	// job under the same name would extract straight into the first one's files.
	// Adds to an existing book each get their own randomized staging dir (below)
	// and write differently-named EPUBs into the shared output folder, so several
	// may legitimately be queued for the same book at once — which is exactly
	// what uploading two volumes to it means. Enforcing the name here would
	// instead reject the second file, and reject any add while the book's own
	// original conversion was still running.
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

	// Staging dir lives inside lib.Path itself (not the system temp dir) so the
	// final move into place is same-filesystem and atomic — /library is
	// typically its own bind-mounted volume, and a cross-device rename would
	// fail. For a new book this becomes inDir (the persistent "<name>-in"
	// staging folder); for "add to existing" there's no meaningful fixed name
	// for it (the target folder — outDir — already exists under "name"), so
	// it just keeps its randomized temp name.
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
		// No extraction — staged as-is; processPDFVolumes (converter/pdf.go)
		// decides OCR vs direct text extraction once the job runs. Named
		// after the upload's own filename, not the job/series name — for
		// "add to existing", `name` is the target book, not this one new
		// volume's own title.
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
		// stagingDir's randomized name already is the final input path —
		// outDir (the target book) already exists, there's no separate
		// "<name>-in" to rename it to.
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
			UpdatedAt:     j.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	respond(w, map[string]any{"items": dtos})
}

// deleteConversionJob stops or removes a job depending on its current state:
//
//   - pending/done/failed/stopped: nothing is actively reading the staged
//     files right now, so it's safe to delete the row and clean up
//     immediately (see DeleteConversionJob's own doc comment for why only
//     input_path, never output_path, gets removed).
//   - running: a converter-worker goroutine has a mokuro subprocess actively
//     reading from input_path *right now*. Deleting the row and calling
//     os.RemoveAll on it here would race that subprocess — mid-batch volumes
//     started disappearing out from under it, which is exactly the bug this
//     replaces. Instead this only flags stop_requested; converter-worker
//     polls that flag, cancels the subprocess cleanly, and does the actual
//     row deletion + cleanup itself once the process has genuinely exited
//     (converter/watch.go's processQueuedJob).
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
	if inputPath != "" {
		os.RemoveAll(inputPath)
	}
	w.WriteHeader(http.StatusNoContent)
}
