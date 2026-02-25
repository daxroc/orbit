//go:build linux

package recorder

import (
	"log/slog"
	"net"
	"syscall"
	"unsafe"

	"github.com/daxroc/orbit/internal/metrics"
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

func (w *WireRecorder) CollectTCPInfo(conn net.Conn, target, protocol string) *TCPInfoSnapshot {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil
	}

	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		slog.Debug("failed to get syscall conn", "error", err)
		return nil
	}

	var info syscall.TCPInfo
	var sysErr error

	err = rawConn.Control(func(fd uintptr) {
		size := uint32(unsafe.Sizeof(info))
		_, _, errno := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT,
			fd,
			syscall.SOL_TCP,
			syscall.TCP_INFO,
			uintptr(unsafe.Pointer(&info)),
			uintptr(unsafe.Pointer(&size)),
			0,
		)
		if errno != 0 {
			sysErr = errno
		}
	})

	if err != nil || sysErr != nil {
		slog.Debug("failed to get TCP_INFO", "error", err, "sysErr", sysErr)
		return nil
	}

	snapshot := &TCPInfoSnapshot{
		RTT:         info.Rtt,
		RTTVar:      info.Rttvar,
		SndMSS:      info.Snd_mss,
		SndCwnd:     info.Snd_cwnd,
		RetransSegs: uint32(info.Retransmits),
		Lost:        info.Lost,
	}

	labels := []string{w.source, target, protocol}

	metrics.WireRTT.WithLabelValues(labels...).Set(float64(snapshot.RTT) / 1e6)
	metrics.WireRTTVariance.WithLabelValues(labels...).Set(float64(snapshot.RTTVar) / 1e6)
	metrics.WireMSS.WithLabelValues(labels...).Set(float64(snapshot.SndMSS))
	metrics.WireCwnd.WithLabelValues(labels...).Set(float64(snapshot.SndCwnd))

	if snapshot.BytesSent > 0 {
		metrics.WireBytesSent.WithLabelValues(labels...).Add(float64(snapshot.BytesSent))
	}
	if snapshot.BytesReceived > 0 {
		metrics.WireBytesReceived.WithLabelValues(labels...).Add(float64(snapshot.BytesReceived))
	}
	if snapshot.BytesRetrans > 0 {
		metrics.WireBytesRetransmitted.WithLabelValues(labels...).Add(float64(snapshot.BytesRetrans))
	}
	if snapshot.RetransSegs > 0 {
		metrics.WireSegmentsRetransmitted.WithLabelValues(labels...).Add(float64(snapshot.RetransSegs))
	}
	if snapshot.Lost > 0 {
		metrics.WireLostPackets.WithLabelValues(labels...).Add(float64(snapshot.Lost))
	}

	return snapshot
}
