// Package archive extracts user-uploaded manga archives (zip/tar/tar.gz/tar.xz/7z/rar)
// onto disk, skipping OS junk files and guarding against path traversal.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"
)

// Extract detects the archive format from archivePath's extension and extracts
// its contents into destDir (created if needed), skipping OS junk and
// rejecting zip-slip entries. A single top-level wrapping directory is
// collapsed away afterward — see collapseSingleRoot.
func Extract(archivePath, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("archive: mkdir dest: %w", err)
	}

	if err := extractByFormat(archivePath, destDir); err != nil {
		return err
	}

	return collapseSingleRoot(destDir)
}

func extractByFormat(archivePath, destDir string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, destDir)
	case strings.HasSuffix(lower, ".tar"):
		return withFile(archivePath, func(r io.Reader) error { return extractTar(r, destDir) })
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return withFile(archivePath, func(r io.Reader) error {
			gz, err := gzip.NewReader(r)
			if err != nil {
				return fmt.Errorf("archive: gzip: %w", err)
			}
			defer gz.Close()
			return extractTar(gz, destDir)
		})
	case strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz"):
		return withFile(archivePath, func(r io.Reader) error {
			xr, err := xz.NewReader(r)
			if err != nil {
				return fmt.Errorf("archive: xz: %w", err)
			}
			return extractTar(xr, destDir)
		})
	case strings.HasSuffix(lower, ".7z"):
		return extract7z(archivePath, destDir)
	case strings.HasSuffix(lower, ".rar"):
		return extractRar(archivePath, destDir)
	default:
		return fmt.Errorf("archive: unsupported format: %s", filepath.Ext(archivePath))
	}
}

func withFile(path string, fn func(io.Reader) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("archive: open: %w", err)
	}
	defer f.Close()
	return fn(f)
}

func extractZip(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("archive: open zip: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if isJunk(f.Name) {
			continue
		}
		destPath, err := safeJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := writeEntry(destPath, f, f.Mode()); err != nil {
			return fmt.Errorf("archive: %s: %w", f.Name, err)
		}
	}
	return nil
}

type opener interface {
	Open() (io.ReadCloser, error)
}

func writeEntry(destPath string, o opener, mode os.FileMode) error {
	rc, err := o.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

func extractTar(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive: tar: %w", err)
		}
		if isJunk(hdr.Name) {
			continue
		}
		destPath, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
				return err
			}
			// Mask to plain rwx bits: hdr.Mode is attacker-controlled and
			// archive/tar's int64 could carry setuid/setgid/sticky bits or
			// overflow on truncation to os.FileMode otherwise.
			mode := os.FileMode(hdr.Mode & 0o777)
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("archive: %s: %w", hdr.Name, err)
			}
			out.Close()
		}
		// symlinks and other special types are silently skipped — not expected
		// in manga image archives, and not worth the path-safety analysis.
	}
}

func extract7z(archivePath, destDir string) error {
	zr, err := sevenzip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("archive: open 7z: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if isJunk(f.Name) {
			continue
		}
		destPath, err := safeJoin(destDir, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := writeEntry(destPath, f, f.Mode()); err != nil {
			return fmt.Errorf("archive: %s: %w", f.Name, err)
		}
	}
	return nil
}

func extractRar(archivePath, destDir string) error {
	rc, err := rardecode.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("archive: open rar: %w", err)
	}
	defer rc.Close()

	for {
		hdr, err := rc.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("archive: rar: %w", err)
		}
		if isJunk(hdr.Name) {
			continue
		}
		destPath, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		if hdr.IsDir {
			if err := os.MkdirAll(destPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		mode := hdr.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return fmt.Errorf("archive: %s: %w", hdr.Name, err)
		}
		out.Close()
	}
}

// isJunk reports whether an archive entry is OS-generated cruft that should
// never end up on disk: macOS AppleDouble resource forks ("._name"), Finder's
// ".DS_Store", and anything under a top-level "__MACOSX/" directory (zip's
// equivalent of AppleDouble storage).
func isJunk(name string) bool {
	name = filepath.ToSlash(name)
	base := filepath.Base(name)
	if base == ".DS_Store" || strings.HasPrefix(base, "._") {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if part == "__MACOSX" {
			return true
		}
	}
	return false
}

// safeJoin joins destDir with an archive-provided relative path, rejecting
// anything that would escape destDir (zip-slip: "../" traversal or an
// absolute path baked into the entry name).
func safeJoin(destDir, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("archive: unsafe entry path: %q", name)
	}
	return filepath.Join(destDir, clean), nil
}

// collapseSingleRoot strips a redundant top-level wrapping directory (e.g.
// `zip -r "Name.zip" "Name/"`) — left uncollapsed, the "one subfolder = one
// volume" detection sees a single wrapper folder and merges all real volumes
// into one. Loops in case an archive nests more than one such wrapper.
func collapseSingleRoot(dir string) error {
	for {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("archive: collapse root: %w", err)
		}
		if len(entries) != 1 || !entries[0].IsDir() {
			return nil
		}

		inner := filepath.Join(dir, entries[0].Name())
		innerEntries, err := os.ReadDir(inner)
		if err != nil {
			return fmt.Errorf("archive: collapse root: %w", err)
		}

		// Only collapse if the subdirectory itself holds further subdirectories
		// (a multi-volume wrapper) or multiple top-level PDFs (each PDF is its
		// own volume, same idea) — if it holds images directly, it's a
		// legitimate single volume named after that folder.
		hasSubdir, pdfCount := false, 0
		for _, e := range innerEntries {
			if e.IsDir() {
				hasSubdir = true
				break
			}
			if strings.EqualFold(filepath.Ext(e.Name()), ".pdf") {
				pdfCount++
			}
		}
		if !hasSubdir && pdfCount < 2 {
			return nil
		}

		for _, e := range innerEntries {
			if err := os.Rename(filepath.Join(inner, e.Name()), filepath.Join(dir, e.Name())); err != nil {
				return fmt.Errorf("archive: collapse root: %w", err)
			}
		}
		if err := os.Remove(inner); err != nil {
			return fmt.Errorf("archive: collapse root: %w", err)
		}
	}
}
