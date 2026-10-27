package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/truebad0ur/yomekuro/internal/coverimage"
	"github.com/truebad0ur/yomekuro/internal/epub"
)

func main() {
	dsn := os.Getenv("YOMEKURO_DB")
	dataDir := os.Getenv("YOMEKURO_DATA")
	if dsn == "" || dataDir == "" {
		log.Fatal("YOMEKURO_DB and YOMEKURO_DATA must be set")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	limitClause := ""
	if lim := os.Getenv("REGEN_LIMIT"); lim != "" {
		limitClause = " LIMIT " + lim
	}
	whereClause := ""
	var args []any
	if series := os.Getenv("REGEN_SERIES"); series != "" {
		whereClause = " AND series_name = $1"
		args = append(args, series)
	}
	rows, err := pool.Query(context.Background(),
		`SELECT id, path, cover_path FROM books WHERE format = 'epub' AND path IS NOT NULL AND path != ''`+whereClause+` ORDER BY series_name, series_index`+limitClause, args...)
	if err != nil {
		log.Fatalf("query: %v", err)
	}

	type row struct {
		id        string
		path      string
		coverPath string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path, &r.coverPath); err != nil {
			log.Fatalf("scan: %v", err)
		}
		all = append(all, r)
	}
	rows.Close()

	log.Printf("regenerating covers for %d epub books", len(all))

	dryRun := os.Getenv("REGEN_DRY_RUN") == "1"

	changed, skipped, failed := 0, 0, 0
	for i, r := range all {
		b, err := epub.Open(r.path, "")
		if err != nil {
			log.Printf("[%d/%d] OPEN FAIL %s: %v", i+1, len(all), r.path, err)
			failed++
			continue
		}
		if len(b.CoverData) == 0 {
			skipped++
			continue
		}

		newData, newMediaType := coverimage.Downscale(b.CoverData, b.CoverMediaType)

		if dryRun {
			same := newMediaType == b.CoverMediaType && len(newData) == len(b.CoverData)
			log.Printf("[%d/%d] %s: origMT=%s origLen=%d newMT=%s newLen=%d unchanged=%v",
				i+1, len(all), r.path, b.CoverMediaType, len(b.CoverData), newMediaType, len(newData), same)
			changed++
			continue
		}

		ext := extFor(newMediaType)
		newFileName := randomID() + ext
		newPath := filepath.Join(dataDir, "covers", newFileName)

		if err := os.WriteFile(newPath, newData, 0o600); err != nil {
			log.Printf("[%d/%d] WRITE FAIL %s: %v", i+1, len(all), newPath, err)
			failed++
			continue
		}

		oldCoverPath := r.coverPath
		_, err = pool.Exec(context.Background(),
			`UPDATE books SET cover_path = $1, cover_media_type = $2 WHERE id = $3`,
			newPath, newMediaType, r.id)
		if err != nil {
			log.Printf("[%d/%d] DB UPDATE FAIL %s: %v", i+1, len(all), r.path, err)
			os.Remove(newPath)
			failed++
			continue
		}

		if oldCoverPath != "" && oldCoverPath != newPath {
			os.Remove(oldCoverPath)
		}
		changed++
		if changed%100 == 0 {
			log.Printf("progress: %d/%d done", i+1, len(all))
		}
	}

	log.Printf("done: %d regenerated, %d skipped (no cover), %d failed", changed, skipped, failed)
}

func extFor(mediaType string) string {
	switch mediaType {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// RFC4122-ish uuid v4 formatting, doesn't need to be a "real" UUID, just unique+safe
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
