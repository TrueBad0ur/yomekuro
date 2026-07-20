package shared

import "strings"

// ImageExts is the canonical set of page-image extensions this project
// understands, including ".jxl" (decoded to .png by the converter before
// mokuro ever sees it — see convertJXLPages — but still needs to be
// recognized everywhere a raw scan folder is scanned for image files, e.g.
// counting volumes or reconciling OCR cache before that decode step runs).
var ImageExts = []string{".jpg", ".jpeg", ".png", ".webp", ".jxl"}

// IsImageExt reports whether ext (as returned by filepath.Ext, case-
// insensitive) is one of ImageExts.
func IsImageExt(ext string) bool {
	ext = strings.ToLower(ext)
	for _, e := range ImageExts {
		if ext == e {
			return true
		}
	}
	return false
}
