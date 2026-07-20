package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Book struct {
	ID               [16]byte
	LibraryID        [16]byte
	Path             string
	Filename         string
	FileSize         int64
	FileHash         string
	FileModified     time.Time
	Title            string
	SortTitle        string
	Authors          []string
	Language         string
	Publisher        string
	PublishedAt      *time.Time
	Description      string
	ISBN             string
	SeriesName       string
	SeriesIndex      float64
	PageCount        int
	ReadingDirection string
	CoverPath        string
	CoverMediaType   string
	Format           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// BookFilter carries optional filter/sort/pagination params for ListBooks.
type BookFilter struct {
	LibraryID string
	Language  string
	Series    string
	Tag       string
	Query     string
	Sort      string
	Page      int
	Limit     int
}

var allowedSorts = map[string]string{
	"sort_title":   "sort_title",
	"created_at":   "created_at DESC",
	"published_at": "published_at DESC NULLS LAST",
	"series":       "series_name, series_index",
	"authors":      "authors[1]",
}

func orderByClause(sort string) string {
	if s, ok := allowedSorts[sort]; ok {
		return s
	}
	return "sort_title"
}

const selectBookCols = `
	id, library_id, path, filename, file_size, file_hash,
	title, sort_title, authors, language, publisher, published_at,
	description, isbn, series_name, series_index, page_count,
	reading_direction, cover_path, cover_media_type, format, created_at, updated_at`

func scanBook(row pgx.Row) (Book, error) {
	var b Book
	var pubAt pgtype.Date
	err := row.Scan(
		&b.ID, &b.LibraryID, &b.Path, &b.Filename, &b.FileSize, &b.FileHash,
		&b.Title, &b.SortTitle, &b.Authors, &b.Language, &b.Publisher, &pubAt,
		&b.Description, &b.ISBN, &b.SeriesName, &b.SeriesIndex, &b.PageCount,
		&b.ReadingDirection, &b.CoverPath, &b.CoverMediaType, &b.Format, &b.CreatedAt, &b.UpdatedAt,
	)
	if err == nil && pubAt.Valid {
		t := pubAt.Time
		b.PublishedAt = &t
	}
	return b, err
}

func GetBookByID(ctx context.Context, pool *pgxpool.Pool, id [16]byte) (Book, error) {
	row := pool.QueryRow(ctx,
		`SELECT`+selectBookCols+` FROM books WHERE id = $1`, id)
	b, err := scanBook(row)
	if err == pgx.ErrNoRows {
		return Book{}, pgx.ErrNoRows
	}
	return b, err
}

type PagedBooks struct {
	Items []Book
	Total int
	Page  int
	Limit int
}

func ListBooks(ctx context.Context, pool *pgxpool.Pool, f BookFilter) (PagedBooks, error) {
	var conds []string
	var args []any
	n := 1

	if f.LibraryID != "" {
		if id, err := ParseUUID(f.LibraryID); err == nil {
			conds = append(conds, fmt.Sprintf("library_id = $%d", n))
			args = append(args, id)
			n++
		}
	}
	if f.Language != "" {
		conds = append(conds, fmt.Sprintf("language = $%d", n))
		args = append(args, f.Language)
		n++
	}
	if f.Series != "" {
		conds = append(conds, fmt.Sprintf("series_name = $%d", n))
		args = append(args, f.Series)
		n++
	}
	if f.Tag != "" {
		conds = append(conds, fmt.Sprintf(
			`id IN (SELECT book_id FROM book_tags bt JOIN tags t ON t.id=bt.tag_id WHERE t.name=$%d)`, n))
		args = append(args, f.Tag)
		n++
	}
	if f.Query != "" {
		conds = append(conds, fmt.Sprintf(
			`(title ILIKE '%%'||$%d||'%%' OR $%d ILIKE ANY(authors))`, n, n))
		args = append(args, f.Query)
		n++
	}

	where := "1=1"
	if len(conds) > 0 {
		where = strings.Join(conds, " AND ")
	}

	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	page := f.Page
	if page < 1 {
		page = 1
	}

	var total int
	if err := pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM books WHERE %s`, where),
		args...,
	).Scan(&total); err != nil {
		return PagedBooks{}, err
	}

	selectArgs := append(append([]any{}, args...), limit, (page-1)*limit)
	rows, err := pool.Query(ctx,
		fmt.Sprintf(`SELECT %s FROM books WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`,
			selectBookCols, where, orderByClause(f.Sort), n, n+1),
		selectArgs...,
	)
	if err != nil {
		return PagedBooks{}, err
	}
	defer rows.Close()

	var books []Book
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return PagedBooks{}, err
		}
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return PagedBooks{}, err
	}
	return PagedBooks{Items: books, Total: total, Page: page, Limit: limit}, nil
}

// ── scanner helpers (unchanged) ───────────────────────────────────────────────

const upsertBookSQL = `
INSERT INTO books (
    id, library_id, path, filename,
    file_size, file_hash, file_modified,
    title, sort_title, authors, language, publisher, published_at,
    description, isbn, series_name, series_index, page_count,
    reading_direction, cover_path, cover_media_type, format
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    $8, $9, $10, $11, $12, $13::date,
    $14, $15, $16, $17, $18,
    $19, $20, $21, $22
)
ON CONFLICT (path) DO UPDATE SET
    filename          = EXCLUDED.filename,
    file_size         = EXCLUDED.file_size,
    file_hash         = EXCLUDED.file_hash,
    file_modified     = EXCLUDED.file_modified,
    title             = EXCLUDED.title,
    sort_title        = EXCLUDED.sort_title,
    authors           = EXCLUDED.authors,
    language          = EXCLUDED.language,
    publisher         = EXCLUDED.publisher,
    published_at      = EXCLUDED.published_at,
    description       = EXCLUDED.description,
    isbn              = EXCLUDED.isbn,
    series_name       = EXCLUDED.series_name,
    series_index      = EXCLUDED.series_index,
    page_count        = EXCLUDED.page_count,
    reading_direction = EXCLUDED.reading_direction,
    cover_path        = EXCLUDED.cover_path,
    cover_media_type  = EXCLUDED.cover_media_type,
    format            = EXCLUDED.format,
    updated_at        = NOW()`

func UpsertBook(ctx context.Context, pool *pgxpool.Pool, b Book) error {
	_, err := pool.Exec(ctx, upsertBookSQL,
		b.ID, b.LibraryID, b.Path, b.Filename,
		b.FileSize, b.FileHash, b.FileModified,
		b.Title, b.SortTitle, b.Authors, b.Language, b.Publisher, b.PublishedAt,
		b.Description, b.ISBN, b.SeriesName, b.SeriesIndex, b.PageCount,
		b.ReadingDirection, b.CoverPath, b.CoverMediaType, b.Format,
	)
	return err
}

func GetBookByPath(ctx context.Context, pool *pgxpool.Pool, path string) (Book, bool, error) {
	var b Book
	err := pool.QueryRow(ctx,
		`SELECT id, file_size, file_hash, file_modified, cover_path FROM books WHERE path = $1`, path,
	).Scan(&b.ID, &b.FileSize, &b.FileHash, &b.FileModified, &b.CoverPath)
	if err == pgx.ErrNoRows {
		return Book{}, false, nil
	}
	if err != nil {
		return Book{}, false, err
	}
	b.Path = path
	return b, true, nil
}

func UpdateFileStats(ctx context.Context, pool *pgxpool.Pool, id [16]byte, size int64, mtime time.Time, hash string) error {
	_, err := pool.Exec(ctx,
		`UPDATE books SET file_size=$2, file_modified=$3, file_hash=$4, updated_at=NOW() WHERE id=$1`,
		id, size, mtime, hash,
	)
	return err
}

// DeleteBooksNotIn removes books whose file vanished from disk and returns the
// cover_path of every deleted row (skipping empty ones) so the caller can also
// remove the now-orphaned cover files — cover images are stored on disk
// independently of the DB row and are never cleaned up otherwise.
func DeleteBooksNotIn(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, paths []string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`DELETE FROM books WHERE library_id = $1 AND NOT (path = ANY($2)) RETURNING cover_path`,
		libraryID, paths,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var covers []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		if c != "" {
			covers = append(covers, c)
		}
	}
	return covers, rows.Err()
}

func CountBooks(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM books WHERE library_id=$1`, libraryID).Scan(&n)
	return n, err
}

// DeleteBookByPath removes the book and returns its cover_path (empty if it
// had none, or if no row matched) so the caller can also delete the cover file.
func DeleteBookByPath(ctx context.Context, pool *pgxpool.Pool, path string) (string, error) {
	var cover string
	err := pool.QueryRow(ctx,
		`DELETE FROM books WHERE path = $1 RETURNING cover_path`, path,
	).Scan(&cover)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return cover, err
}

// RenameSeriesUnderPath sets series_name for every book at or under pathOrDir
// — DB only, never the file on disk.
func RenameSeriesUnderPath(ctx context.Context, pool *pgxpool.Pool, libraryID [16]byte, pathOrDir, newName string) (int64, error) {
	// Exact prefix comparison, not LIKE — a book name containing a literal "%"
	// or "_" would otherwise be interpreted as a SQL wildcard.
	tag, err := pool.Exec(ctx,
		`UPDATE books SET series_name = $1, updated_at = NOW()
		 WHERE library_id = $2 AND (path = $3 OR left(path, length($3) + 1) = $3 || '/')`,
		newName, libraryID, pathOrDir,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
