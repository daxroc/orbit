//go:build linux

package recorder

import (
	"log/slog"
	"net"
	"sync"
	"syscall"
	"unsafe"

	"github.com/daxroc/orbit/internal/metrics"
)

// tcpInfoExtended mirrors the Linux tcp_info struct including fields added in
// kernel 4.2+ (bytes_acked, bytes_received, segs_out, segs_in) and 4.6+
// (bytes_sent, bytes_retrans). On older kernels getsockopt will simply return
// fewer bytes and the trailing fields remain zero.
type tcpInfoExtended struct {
	syscall.TCPInfo
	Pacing_rate     uint64
	Max_pacing_rate uint64
	Bytes_acked     uint64
	Bytes_received  uint64
	Segs_out        uint32
	Segs_in         uint32
	Notsent_lowat   uint32
	Min_rtt         uint32
	Data_segs_in    uint32
	Data_segs_out   uint32
	Delivery_rate   uint64
	Busy_time       uint64
	Rwnd_limited    uint64
	Sndbuf_limited  uint64
	Delivered       uint32
	Delivered_ce    uint32
	Bytes_sent      uint64
	Bytes_retrans   uint64
}

type prevCounters struct {
	bytesSent     uint64
	bytesReceived uint64
	bytesRetrans  uint64
	segsOut       uint32
	totalRetrans  uint32
	lost          uint32
}

type WireRecorder struct {
	source string
	mu     sync.Mutex
	prev   map[string]prevCounters
}

func NewWireRecorder(source string) *WireRecorder {
	return &WireRecorder{
		source: source,
		prev:   make(map[string]prevCounters),
	}
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

	var info tcpInfoExtended
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
		RTT:           info.Rtt,
		RTTVar:        info.Rttvar,
		SndMSS:        info.Snd_mss,
		SndCwnd:       info.Snd_cwnd,
		BytesSent:     info.Bytes_sent,
		BytesReceived: info.Bytes_received,
		BytesRetrans:  info.Bytes_retrans,
		SegsOut:       info.Segs_out,
		SegsIn:        info.Segs_in,
		RetransSegs:   info.Total_retrans,
		Lost:          info.Lost,
	}

	if host, _, err := net.SplitHostPort(target); err == nil {
		target = host
	}
	labels := []string{w.source, target, protocol}

	metrics.WireRTT.WithLabelValues(labels...).Set(float64(snapshot.RTT) / 1e6)
	metrics.WireRTTVariance.WithLabelValues(labels...).Set(float64(snapshot.RTTVar) / 1e6)
	metrics.WireMSS.WithLabelValues(labels...).Set(float64(snapshot.SndMSS))
	metrics.WireCwnd.WithLabelValues(labels...).Set(float64(snapshot.SndCwnd))

	connKey := conn.LocalAddr().String() + "|" + target + "|" + protocol

	w.mu.Lock()
	prev := w.prev[connKey]

	if snapshot.BytesSent > prev.bytesSent {
		metrics.WireBytesSent.WithLabelValues(labels...).Add(float64(snapshot.BytesSent - prev.bytesSent))
	}
	if snapshot.BytesReceived > prev.bytesReceived {
		metrics.WireBytesReceived.WithLabelValues(labels...).Add(float64(snapshot.BytesReceived - prev.bytesReceived))
	}
	if snapshot.BytesRetrans > prev.bytesRetrans {
		metrics.WireBytesRetransmitted.WithLabelValues(labels...).Add(float64(snapshot.BytesRetrans - prev.bytesRetrans))
	}
	if snapshot.SegsOut > prev.segsOut {
		metrics.WireSegmentsSent.WithLabelValues(labels...).Add(float64(snapshot.SegsOut - prev.segsOut))
	}
	if snapshot.RetransSegs > prev.totalRetrans {
		metrics.WireSegmentsRetransmitted.WithLabelValues(labels...).Add(float64(snapshot.RetransSegs - prev.totalRetrans))
	}
	if snapshot.Lost > prev.lost {
		metrics.WireLostPackets.WithLabelValues(labels...).Add(float64(snapshot.Lost - prev.lost))
	}

	w.prev[connKey] = prevCounters{
		bytesSent:     snapshot.BytesSent,
		bytesReceived: snapshot.BytesReceived,
		bytesRetrans:  snapshot.BytesRetrans,
		segsOut:       snapshot.SegsOut,
		totalRetrans:  snapshot.RetransSegs,
		lost:          snapshot.Lost,
	}
	w.mu.Unlock()

	return snapshot
}

func (w *WireRecorder) RemoveConn(conn net.Conn, target, protocol string) {
	if host, _, err := net.SplitHostPort(target); err == nil {
		target = host
	}
	connKey := conn.LocalAddr().String() + "|" + target + "|" + protocol
	w.mu.Lock()
	delete(w.prev, connKey)
	w.mu.Unlock()
}
