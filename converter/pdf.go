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

// textPDFMinCharsPerPage is the average non-whitespace character count per
// page above which a PDF is treated as having a real text layer rather than
// being scanned page images with no extractable text at all.
const textPDFMinCharsPerPage = 20

// textPDFMinJapaneseFraction guards against a text layer that's present but
// garbage — e.g. Internet Archive scans of Japanese books run through a
// non-Japanese-aware OCR engine produce a dense layer of Latin-letter/symbol
// noise (real characters, real per-page count, zero relation to the actual
// page content). Real Japanese text layers are overwhelmingly kana/kanji;
// this rejects anything below the threshold back to the OCR path.
const textPDFMinJapaneseFraction = 0.3

// processPDFVolumes handles every top-level "<name>.pdf" in input as its own
// volume, mirroring how a subfolder is one volume for images. Every PDF is
// rasterized to page images first, same as a real scan — a PDF with a real
// text layer then gets its EPUB built immediately from that text (positioned
// at its real coordinates over the page images, no OCR involved) and its
// volume folder is removed so mokuro never sees it; a scanned PDF (no text
// layer) is left as a plain page-image folder for the normal mokuro pipeline
// to OCR. Returns how many volumes it fully finished via the text path — the
// caller's own ok count.
func processPDFVolumes(input, output string) (textOK int, err error) {
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

// pdfHasTextLayer runs pdftotext and checks whether the extracted text is
// substantial enough to be a real text layer (rather than empty/near-empty
// scan artifacts) *and* is actually Japanese, not OCR noise from an engine
// that doesn't understand the script.
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

// isJapanese reports whether r falls in a script block used to write
// Japanese: hiragana, katakana, CJK ideographs (kanji), or CJK/fullwidth
// punctuation and forms.
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
