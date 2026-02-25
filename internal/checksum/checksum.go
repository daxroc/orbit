package checksum

import (
	"crypto/sha256"
	"encoding/hex"
)

func Compute(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func Verify(data, expected []byte) bool {
	actual := Compute(data)
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func ComputeHex(data []byte) string {
	return hex.EncodeToString(Compute(data))
}
