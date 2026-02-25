package recorder

import (
	"bytes"
	"testing"
)

func TestAppRecorder_Counters(t *testing.T) {
	r := NewAppRecorder()

	r.AddBytesSent(100)
	r.AddBytesSent(50)
	if got := r.BytesSent(); got != 150 {
		t.Errorf("expected BytesSent 150, got %d", got)
	}

	r.AddBytesReceived(200)
	if got := r.BytesReceived(); got != 200 {
		t.Errorf("expected BytesReceived 200, got %d", got)
	}

	r.AddPacketsSent(10)
	r.AddPacketsSent(5)
	if got := r.PacketsSent(); got != 15 {
		t.Errorf("expected PacketsSent 15, got %d", got)
	}

	r.AddPacketsReceived(20)
	if got := r.PacketsReceived(); got != 20 {
		t.Errorf("expected PacketsReceived 20, got %d", got)
	}
}

func TestCountingWriter(t *testing.T) {
	var buf bytes.Buffer
	r := NewAppRecorder()
	cw := NewCountingWriter(&buf, r)

	data := []byte("hello world")
	n, err := cw.Write(data)
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if buf.String() != "hello world" {
		t.Errorf("expected 'hello world', got %q", buf.String())
	}
	if got := r.BytesSent(); got != int64(len(data)) {
		t.Errorf("expected BytesSent %d, got %d", len(data), got)
	}
}

func TestCountingReader(t *testing.T) {
	buf := bytes.NewBufferString("test data")
	r := NewAppRecorder()
	cr := NewCountingReader(buf, r)

	out := make([]byte, 9)
	n, err := cr.Read(out)
	if err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if n != 9 {
		t.Errorf("expected 9 bytes read, got %d", n)
	}
	if string(out) != "test data" {
		t.Errorf("expected 'test data', got %q", string(out))
	}
	if got := r.BytesReceived(); got != 9 {
		t.Errorf("expected BytesReceived 9, got %d", got)
	}
}
