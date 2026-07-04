package scanner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/truebad0ur/yomekuro/internal/db"
	"github.com/truebad0ur/yomekuro/internal/epub"
	"github.com/truebad0ur/yomekuro/internal/htmlbook"
)

type Scanner struct {
	pool    *pgxpool.Pool
	dataDir string
}

func New(pool *pgxpool.Pool, dataDir string) *Scanner {
	return &Scanner{pool: pool, dataDir: dataDir}
}

// ScanLibrary walks lib.Path, upserts all EPUB files, and removes DB entries
// for files that no longer exist on disk.
func (s *Scanner) ScanLibrary(ctx context.Context, lib db.Library) error {
	start := time.Now()
	var found []string
	updated := 0

	err := filepath.WalkDir(lib.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			slog.Warn("scan: walk error", "path", path, "err", err)
			return nil
		}
		if d.IsDir() || (!isEPUB(d.Name()) && !isHTML(d.Name())) {
			return nil
		}
		found = append(found, path)
		if changed, err := s.processFile(ctx, lib, path); err != nil {
			slog.Warn("scan: skip file", "path", path, "err", err)
		} else if changed {
			updated++
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scanner: walk %s: %w", lib.Path, err)
	}

	if err := db.DeleteBooksNotIn(ctx, s.pool, lib.ID, found); err != nil {
		return fmt.Errorf("scanner: cleanup: %w", err)
	}

	slog.Info("scan complete",
		"library", lib.Name,
		"total", len(found),
		"updated", updated,
		"elapsed", time.Since(start).Round(time.Millisecond),
	)
	return nil
}

// processFile checks if a file needs updating and upserts it.
// Returns true if the DB was written, false if the file was skipped.
func (s *Scanner) processFile(ctx context.Context, lib db.Library, filePath string) (bool, error) {
	fi, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}

	existing, found, err := db.GetBookByPath(ctx, s.pool, filePath)
	if err != nil {
		return false, err
	}

	// Truncate to microseconds: both ext4 and APFS store nanosecond mtimes,
	// but PostgreSQL timestamptz has microsecond precision. Without truncation
	// the round-trip comparison would always fail after the first write.
	mtime := fi.ModTime().UTC().Truncate(time.Microsecond)

	// Cheap check: mtime + size match → no change.
	if found &&
		existing.FileSize == fi.Size() &&
		existing.FileModified.Equal(mtime) {
		return false, nil
	}

	hash, err := hashFile(filePath)
	if err != nil {
		return false, err
	}

	// Hash match → update only file stats (timestamp/size changed, content same).
	if found && existing.FileHash == hash {
		return true, db.UpdateFileStats(ctx, s.pool, existing.ID, fi.Size(), mtime, hash)
	}

	// Full parse needed.
	bookID := existing.ID
	if !found {
		bookID, err = db.NewUUID()
		if err != nil {
			return false, err
		}
	}

	var rec db.Book
	var tags []string

	if isHTML(filePath) {
		book, err := htmlbook.Open(filePath)
		if err != nil {
			return false, fmt.Errorf("parse html: %w", err)
		}
		rec = db.Book{
			ID:               bookID,
			LibraryID:        lib.ID,
			Path:             filePath,
			Filename:         filepath.Base(filePath),
			FileSize:         fi.Size(),
			FileHash:         hash,
			FileModified:     mtime,
			Title:            book.Title,
			SortTitle:        book.SortTitle,
			Authors:          book.Authors,
			Language:         book.Language,
			SeriesName:       strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath)),
			PageCount:        1,
			ReadingDirection: book.ReadingDirection,
			Format:           "html",
		}
	} else {
		book, err := epub.Open(filePath, lib.Path)
		if err != nil {
			return false, fmt.Errorf("parse epub: %w", err)
		}

		coverPath, coverMT := s.saveCover(bookID, book.CoverData, book.CoverMediaType)

		var pubAt *time.Time
		if book.PublishedAt != nil {
			t := book.PublishedAt.UTC()
			pubAt = &t
		}

		authorNames := make([]string, len(book.Authors))
		for i, a := range book.Authors {
			authorNames[i] = a.Name
		}

		rec = db.Book{
			ID:               bookID,
			LibraryID:        lib.ID,
			Path:             filePath,
			Filename:         filepath.Base(filePath),
			FileSize:         fi.Size(),
			FileHash:         hash,
			FileModified:     mtime,
			Title:            book.Title,
			SortTitle:        book.SortTitle,
			Authors:          authorNames,
			Language:         book.Language,
			Publisher:        book.Publisher,
			PublishedAt:      pubAt,
			Description:      book.Description,
			ISBN:             book.ISBN,
			SeriesName:       book.SeriesName,
			SeriesIndex:      book.SeriesIndex,
			PageCount:        book.PageCount,
			ReadingDirection: book.ReadingDirection,
			CoverPath:        coverPath,
			CoverMediaType:   coverMT,
			Format:           "epub",
		}
		tags = book.Tags
	}

	if err := db.UpsertBook(ctx, s.pool, rec); err != nil {
		return false, fmt.Errorf("upsert book: %w", err)
	}
	if err := db.SetBookTags(ctx, s.pool, bookID, tags); err != nil {
		slog.Warn("set tags", "path", filePath, "err", err)
	}

	return true, nil
}

func (s *Scanner) saveCover(bookID [16]byte, data []byte, mediaType string) (string, string) {
	if len(data) == 0 {
		return "", ""
	}
	ext := coverExt(mediaType)
	path := filepath.Join(s.dataDir, "covers", db.UUIDString(bookID)+ext)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("save cover", "path", path, "err", err)
		return "", ""
	}
	return path, mediaType
}

func isEPUB(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".epub")
}

func isHTML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".html" || ext == ".htm"
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func coverExt(mediaType string) string {
	switch mediaType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}
