package checksum

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

func Compute(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func Verify(data, expected []byte) bool {
	return bytes.Equal(Compute(data), expected)
}

func ComputeHex(data []byte) string {
	return hex.EncodeToString(Compute(data))
}
