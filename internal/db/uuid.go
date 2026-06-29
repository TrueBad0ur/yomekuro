package db

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// NewUUID generates a random UUID v4.
func NewUUID() ([16]byte, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return u, err
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return u, nil
}

// UUIDString formats a [16]byte as a standard UUID string.
func UUIDString(u [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// ParseUUID parses a UUID string into [16]byte.
func ParseUUID(s string) ([16]byte, error) {
	clean := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(clean)
	if err != nil || len(b) != 16 {
		return [16]byte{}, fmt.Errorf("invalid UUID: %q", s)
	}
	var u [16]byte
	copy(u[:], b)
	return u, nil
}
