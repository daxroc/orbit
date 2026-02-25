package recorder

import (
	"io"
	"sync/atomic"
)

type AppRecorder struct {
	bytesSent     atomic.Int64
	bytesReceived atomic.Int64
	packetsSent   atomic.Int64
	packetsRecv   atomic.Int64
	connTotal     atomic.Int64
	connActive    atomic.Int64
}

func NewAppRecorder() *AppRecorder {
	return &AppRecorder{}
}

func (r *AppRecorder) AddBytesSent(n int64)     { r.bytesSent.Add(n) }
func (r *AppRecorder) AddBytesReceived(n int64)  { r.bytesReceived.Add(n) }
func (r *AppRecorder) AddPacketsSent(n int64)    { r.packetsSent.Add(n) }
func (r *AppRecorder) AddPacketsReceived(n int64) { r.packetsRecv.Add(n) }
func (r *AppRecorder) AddConnection()            { r.connTotal.Add(1); r.connActive.Add(1) }
func (r *AppRecorder) RemoveConnection()         { r.connActive.Add(-1) }

func (r *AppRecorder) BytesSent() int64     { return r.bytesSent.Load() }
func (r *AppRecorder) BytesReceived() int64  { return r.bytesReceived.Load() }
func (r *AppRecorder) PacketsSent() int64    { return r.packetsSent.Load() }
func (r *AppRecorder) PacketsReceived() int64 { return r.packetsRecv.Load() }
func (r *AppRecorder) ConnectionsTotal() int64  { return r.connTotal.Load() }
func (r *AppRecorder) ConnectionsActive() int64 { return r.connActive.Load() }

type CountingWriter struct {
	w        io.Writer
	recorder *AppRecorder
}

func NewCountingWriter(w io.Writer, rec *AppRecorder) *CountingWriter {
	return &CountingWriter{w: w, recorder: rec}
}

func (cw *CountingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		cw.recorder.AddBytesSent(int64(n))
	}
	return n, err
}

type CountingReader struct {
	r        io.Reader
	recorder *AppRecorder
}

func NewCountingReader(r io.Reader, rec *AppRecorder) *CountingReader {
	return &CountingReader{r: r, recorder: rec}
}

func (cr *CountingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		cr.recorder.AddBytesReceived(int64(n))
	}
	return n, err
}
