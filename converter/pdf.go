package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// Average non-whitespace chars per page above which a PDF counts as having a
// real text layer rather than being scanned images.
const textPDFMinCharsPerPage = 20

// Guards against a present-but-garbage text layer: scans run through a non-
// Japanese-aware OCR carry dense Latin noise that passes a raw char count.
const textPDFMinJapaneseFraction = 0.3

// Each top-level "<name>.pdf" is one volume, rasterized to page images. One with
// a real text layer is built straight from that text; a scan is left to mokuro.
func processPDFVolumes(input, output, series string, seriesIndex map[string]float64) (textOK int, err error) {
	entries, err := os.ReadDir(input)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".pdf" {
			continue
		}
		pdfPath := filepath.Join(input, e.Name())
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))

		isText, err := pdfHasTextLayer(pdfPath)
		if err != nil {
			return textOK, fmt.Errorf("inspect %s: %w", pdfPath, err)
		}

		volDir := filepath.Join(input, name)
		if err := os.MkdirAll(volDir, 0755); err != nil {
			return textOK, fmt.Errorf("create volume dir for %s: %w", pdfPath, err)
		}
		if err := rasterizePDF(pdfPath, volDir); err != nil {
			return textOK, fmt.Errorf("rasterize %s: %w", pdfPath, err)
		}

		if !isText {
			slog.Info("pdf has no text layer, queued for OCR", "file", e.Name())
			if err := os.Remove(pdfPath); err != nil {
				return textOK, fmt.Errorf("remove %s: %w", pdfPath, err)
			}
			continue
		}

		slog.Info("pdf has a text layer, positioning it directly (no OCR)", "file", e.Name())
		vol, err := buildTextVolume(pdfPath, volDir, name)
		if err != nil {
			return textOK, fmt.Errorf("build text volume %s: %w", pdfPath, err)
		}
		vol.Series = series
		vol.SeriesIndex = seriesIndex[name]
		if err := os.Remove(pdfPath); err != nil {
			return textOK, fmt.Errorf("remove %s: %w", pdfPath, err)
		}
		outPath := filepath.Join(output, name+".epub")
		if err := buildEPUB(vol, input, outPath); err != nil {
			return textOK, fmt.Errorf("build epub %s: %w", pdfPath, err)
		}
		if err := os.RemoveAll(volDir); err != nil {
			return textOK, fmt.Errorf("remove %s: %w", volDir, err)
		}
		textOK++
	}
	return textOK, nil
}

// Whether pdftotext yields enough text to be a real layer — and that it is
// actually Japanese, not noise from an OCR engine blind to the script.
func pdfHasTextLayer(pdfPath string) (bool, error) {
	out, err := exec.Command("pdftotext", "-layout", pdfPath, "-").Output()
	if err != nil {
		return false, fmt.Errorf("pdftotext: %w", err)
	}
	pages, err := pdfPageCount(pdfPath)
	if err != nil {
		return false, err
	}
	if pages == 0 {
		return false, nil
	}
	chars, jaChars := 0, 0
	for _, r := range string(out) {
		if unicode.IsSpace(r) {
			continue
		}
		chars++
		if isJapanese(r) {
			jaChars++
		}
	}
	if chars/pages < textPDFMinCharsPerPage {
		return false, nil
	}
	return float64(jaChars)/float64(chars) >= textPDFMinJapaneseFraction, nil
}

// Whether r is hiragana, katakana, kanji, or CJK/fullwidth punctuation.
func isJapanese(r rune) bool {
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

func pdfPageCount(pdfPath string) (int, error) {
	out, err := exec.Command("pdfinfo", pdfPath).Output()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		after, ok := strings.CutPrefix(line, "Pages:")
		if !ok {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(after), "%d", &n); err == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("pdfinfo: page count not found")
}

// rasterizePDF renders every page of pdfPath to a zero-padded JPEG in destDir
// at a DPI reasonable for both OCR accuracy and on-screen manga reading.
func rasterizePDF(pdfPath, destDir string) error {
	out, err := exec.Command("pdftoppm", "-jpeg", "-r", "300", pdfPath, filepath.Join(destDir, "page")).CombinedOutput()
	if err != nil {
		return fmt.Errorf("pdftoppm: %w: %s", err, out)
	}
	return nil
}
