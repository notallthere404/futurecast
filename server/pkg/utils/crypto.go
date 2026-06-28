package utils

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/gob"
)

// CreateHash returns the SHA-256 over the gob-encoded form of obj.
// Used by the source-sync path to detect config-driven source changes
// without diffing every field.
func CreateHash(obj any) ([32]byte, error) {
	h := sha256.New()
	if err := gob.NewEncoder(h).Encode(obj); err != nil {
		return [32]byte{}, err
	}
	return [32]byte(h.Sum(nil)), nil
}

// CompareHash compares two hash digests in constant time. Use this
// (not bytes.Equal) for hashes that gate behavior so a timing-side
// channel can't leak structure of the expected value.
func CompareHash(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
