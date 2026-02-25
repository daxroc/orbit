package checksum

import (
	"testing"
)

func TestComputeAndVerify(t *testing.T) {
	data := []byte("hello orbit checksum test")
	sum := Compute(data)

	if len(sum) != 32 {
		t.Errorf("expected 32-byte SHA-256, got %d bytes", len(sum))
	}

	if !Verify(data, sum) {
		t.Error("Verify should return true for matching data")
	}

	if Verify([]byte("wrong data"), sum) {
		t.Error("Verify should return false for mismatched data")
	}

	if Verify(data, []byte("short")) {
		t.Error("Verify should return false for wrong-length checksum")
	}
}

func TestComputeHex(t *testing.T) {
	data := []byte("test")
	hex := ComputeHex(data)
	if len(hex) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars", len(hex))
	}
}

func TestComputeDeterministic(t *testing.T) {
	data := []byte("deterministic")
	a := Compute(data)
	b := Compute(data)
	if !Verify(data, a) || !Verify(data, b) {
		t.Error("repeated Compute should produce same result")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Error("Compute is not deterministic")
			break
		}
	}
}
