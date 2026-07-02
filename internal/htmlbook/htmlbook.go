// Package htmlbook treats a single standalone .html file as a one-page book.
package htmlbook

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Book struct {
	Path             string
	FileSize         int64
	Title            string
	SortTitle        string
	Authors          []string
	Language         string
	ReadingDirection string // "ltr" | "rtl"
}

var (
	headRe     = regexp.MustCompile(`(?is)<head[^>]*>(.*?)</head>`)
	titleRe    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlLangRe = regexp.MustCompile(`(?is)<html[^>]*\blang=["']([^"']+)["']`)
	metaRe     = regexp.MustCompile(`(?is)<meta\b([^>]*)>`)
	attrRe     = regexp.MustCompile(`(?is)([a-zA-Z0-9_-]+)\s*=\s*"([^"]*)"|([a-zA-Z0-9_-]+)\s*=\s*'([^']*)'`)
)

// Open reads path and extracts title/author/reading-direction/language
// from the <head>. Falls back to the filename when no <title> is present.
func Open(path string) (*Book, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := string(data)

	title := ""
	if m := titleRe.FindStringSubmatch(content); m != nil {
		title = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if title == "" {
		base := filepath.Base(path)
		title = strings.TrimSuffix(base, filepath.Ext(base))
	}

	head := content
	if m := headRe.FindStringSubmatch(content); m != nil {
		head = m[1]
	}

	var authors []string
	direction := "ltr"
	for _, m := range metaRe.FindAllStringSubmatch(head, -1) {
		attrs := parseAttrs(m[1])
		switch strings.ToLower(attrs["name"]) {
		case "author":
			if v := strings.TrimSpace(attrs["content"]); v != "" {
				authors = append(authors, v)
			}
		case "reading-direction":
			if strings.ToLower(strings.TrimSpace(attrs["content"])) == "rtl" {
				direction = "rtl"
			}
		}
	}

	language := ""
	if m := htmlLangRe.FindStringSubmatch(content); m != nil {
		language = m[1]
	}

	return &Book{
		Path:             path,
		FileSize:         fi.Size(),
		Title:            title,
		SortTitle:        strings.ToLower(title),
		Authors:          authors,
		Language:         language,
		ReadingDirection: direction,
	}, nil
}

func parseAttrs(s string) map[string]string {
	out := map[string]string{}
	for _, m := range attrRe.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			out[strings.ToLower(m[1])] = m[2]
		} else if m[3] != "" {
			out[strings.ToLower(m[3])] = m[4]
		}
	}
	return out
}
