//go:build !linux

package recorder

import (
	"net"
)

type WireRecorder struct {
	source string
}

func NewWireRecorder(source string) *WireRecorder {
	return &WireRecorder{source: source}
}

type TCPInfoSnapshot struct {
	RTT           uint32
	RTTVar        uint32
	SndMSS        uint32
	SndCwnd       uint32
	BytesSent     uint64
	BytesReceived uint64
	BytesRetrans  uint64
	SegsOut       uint32
	SegsIn        uint32
	RetransSegs   uint32
	Lost          uint32
}

func (w *WireRecorder) CollectTCPInfo(_ net.Conn, _, _ string) *TCPInfoSnapshot {
	return nil
}
