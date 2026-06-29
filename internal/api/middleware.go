package api

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func parseUUID(s string) ([16]byte, error) {
	clean := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != 16 {
		return [16]byte{}, fmt.Errorf("invalid UUID: %q", s)
	}
	var u [16]byte
	copy(u[:], b)
	return u, nil
}
