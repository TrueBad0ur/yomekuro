package api

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/truebad0ur/yomekuro/internal/db"
	"github.com/truebad0ur/yomekuro/internal/epub"
	"github.com/truebad0ur/yomekuro/internal/htmlbook"
)

type integrityIssue struct {
	Severity string `json:"severity"`
	Library  string `json:"library"`
	Book     string `json:"book,omitempty"`
	Path     string `json:"path,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type integrityReport struct {
	Status       string           `json:"status"`
	StartedAt    string           `json:"started_at"`
	DurationMS   int64            `json:"duration_ms"`
	Libraries    int              `json:"libraries"`
	Books        int              `json:"books"`
	FilesChecked int              `json:"files_checked"`
	Errors       int              `json:"errors"`
	Warnings     int              `json:"warnings"`
	Issues       []integrityIssue `json:"issues"`
}

func cleanRelativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

func supportedLibraryFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".epub" || ext == ".html" || ext == ".htm"
}

func (s *Server) checkLibraryIntegrity(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	libs, err := db.ListLibraries(r.Context(), s.db)
	if err != nil {
		respondInternal(w, "could not load libraries", err)
		return
	}
	books, err := db.ListAllBooks(r.Context(), s.db)
	if err != nil {
		respondInternal(w, "could not load books", err)
		return
	}

	report := integrityReport{
		StartedAt: start.UTC().Format(time.RFC3339),
		Libraries: len(libs), Books: len(books), Issues: []integrityIssue{},
	}
	libByID := make(map[[16]byte]db.Library, len(libs))
	dbPaths := make(map[string]bool, len(books))
	referencedCovers := make(map[string]bool, len(books))
	for _, lib := range libs {
		libByID[lib.ID] = lib
	}
	add := func(issue integrityIssue) {
		report.Issues = append(report.Issues, issue)
		if issue.Severity == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}

	for _, book := range books {
		lib, knownLibrary := libByID[book.LibraryID]
		if !knownLibrary {
			add(integrityIssue{Severity: "error", Book: book.Title, Code: "unknown_library",
				Message: "Database entry points to a library that no longer exists."})
			continue
		}
		dbPaths[filepath.Clean(book.Path)] = true
		if book.CoverPath != "" {
			referencedCovers[filepath.Clean(book.CoverPath)] = true
		}
		rel := cleanRelativePath(lib.Path, book.Path)
		fullRel, relErr := filepath.Rel(lib.Path, book.Path)
		if relErr != nil || fullRel == ".." || strings.HasPrefix(fullRel, ".."+string(filepath.Separator)) {
			add(integrityIssue{Severity: "error", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "outside_library", Message: "Book path is outside its configured library."})
			continue
		}

		info, statErr := os.Stat(book.Path)
		if statErr != nil {
			add(integrityIssue{Severity: "error", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "missing_file", Message: "Book file is missing or inaccessible."})
			continue
		}
		if !info.Mode().IsRegular() {
			add(integrityIssue{Severity: "error", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "not_regular_file", Message: "Book path is not a regular file."})
			continue
		}
		report.FilesChecked++
		if info.Size() != book.FileSize {
			add(integrityIssue{Severity: "warning", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "size_changed", Message: "File size differs from the indexed value; run a library scan."})
		}

		switch book.Format {
		case "epub":
			spine, _, _, _, openErr := epub.OpenManifest(book.Path)
			if openErr != nil {
				add(integrityIssue{Severity: "error", Library: lib.Name, Book: book.Title, Path: rel,
					Code: "invalid_epub", Message: "EPUB structure or package metadata cannot be read."})
			} else if len(spine) == 0 {
				add(integrityIssue{Severity: "error", Library: lib.Name, Book: book.Title, Path: rel,
					Code: "empty_spine", Message: "EPUB has no readable spine entries."})
			}
		case "html":
			if _, openErr := htmlbook.Open(book.Path); openErr != nil {
				add(integrityIssue{Severity: "error", Library: lib.Name, Book: book.Title, Path: rel,
					Code: "invalid_html", Message: "HTML file cannot be read."})
			}
		default:
			add(integrityIssue{Severity: "warning", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "unknown_format", Message: "Database entry has an unsupported format."})
		}

		if book.CoverPath == "" {
			add(integrityIssue{Severity: "warning", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "missing_cover", Message: "Book has no generated or extracted cover."})
		} else if coverInfo, coverErr := os.Stat(book.CoverPath); coverErr != nil || !coverInfo.Mode().IsRegular() {
			add(integrityIssue{Severity: "warning", Library: lib.Name, Book: book.Title, Path: rel,
				Code: "missing_cover_file", Message: "Indexed cover file is missing or inaccessible."})
		}
	}

	for _, lib := range libs {
		info, statErr := os.Stat(lib.Path)
		if statErr != nil || !info.IsDir() {
			add(integrityIssue{Severity: "error", Library: lib.Name, Code: "library_unavailable",
				Message: "Configured library directory is missing or inaccessible."})
			continue
		}
		walkErr := filepath.WalkDir(lib.Path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				add(integrityIssue{Severity: "error", Library: lib.Name,
					Path: cleanRelativePath(lib.Path, path), Code: "path_inaccessible",
					Message: "A library path could not be read."})
				return nil
			}
			if entry.IsDir() || !supportedLibraryFile(entry.Name()) || dbPaths[filepath.Clean(path)] {
				return nil
			}
			add(integrityIssue{Severity: "warning", Library: lib.Name,
				Path: cleanRelativePath(lib.Path, path), Code: "unindexed_file",
				Message: "Supported book file exists on disk but is not indexed."})
			return nil
		})
		if walkErr != nil {
			add(integrityIssue{Severity: "error", Library: lib.Name, Code: "library_walk_failed",
				Message: "Library directory could not be fully inspected."})
		}
	}

	// Covers live outside the configured library roots, so the library walks
	// above cannot see files left behind by an old deletion/reconversion bug.
	// Report them only; an integrity check must never remove user data.
	coversDir := filepath.Join(s.dataDir, "covers")
	if info, statErr := os.Stat(coversDir); statErr == nil && info.IsDir() {
		walkErr := filepath.WalkDir(coversDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				add(integrityIssue{Severity: "warning", Path: filepath.Base(path),
					Code: "cover_path_inaccessible", Message: "A generated-cover path could not be read."})
				return nil
			}
			if entry.IsDir() || referencedCovers[filepath.Clean(path)] {
				return nil
			}
			add(integrityIssue{Severity: "warning", Path: filepath.Base(path),
				Code: "orphan_cover", Message: "Generated cover file is no longer referenced by any book."})
			return nil
		})
		if walkErr != nil {
			add(integrityIssue{Severity: "warning", Code: "covers_walk_failed",
				Message: "Generated-cover directory could not be fully inspected."})
		}
	}

	report.Status = "healthy"
	if report.Errors > 0 {
		report.Status = "errors"
	} else if report.Warnings > 0 {
		report.Status = "warnings"
	}
	report.DurationMS = time.Since(start).Milliseconds()
	respond(w, report)
}
