package shared

import (
	"path/filepath"
	"strings"
)

// IsJunkName reports whether name (a path, absolute or relative, forward- or
// backslash-separated) is OS-generated cruft that should never be treated as
// real content: "._name" AppleDouble resource forks, ".DS_Store", "Thumbs.db",
// or anything under a "__MACOSX/" directory.
func IsJunkName(name string) bool {
	name = filepath.ToSlash(name)
	base := filepath.Base(name)
	if base == ".DS_Store" || base == "Thumbs.db" || strings.HasPrefix(base, "._") {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if part == "__MACOSX" {
			return true
		}
	}
	return false
}
