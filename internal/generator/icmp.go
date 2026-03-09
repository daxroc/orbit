package generator

import (
	"context"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type ICMPGenerator struct {
	flowID   string
	labels   Labels
	target   string
	interval time.Duration
	duration time.Duration
	recorder *recorder.AppRecorder

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewICMPGenerator(flowID string, labels Labels, target string, intervalMs int, duration time.Duration, rec *recorder.AppRecorder) *ICMPGenerator {
	if intervalMs <= 0 {
		intervalMs = 1000
	}
	return &ICMPGenerator{
		flowID:   flowID,
		labels:   labels,
		target:   target,
		interval: time.Duration(intervalMs) * time.Millisecond,
		duration: duration,
		recorder: rec,
	}
}

func (g *ICMPGenerator) Type() string   { return "icmp" }
func (g *ICMPGenerator) FlowID() string { return g.flowID }

func (g *ICMPGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	if g.duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, g.duration)
		defer c()
	}

	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return err
	}
	defer conn.Close()

	dst, err := net.ResolveIPAddr("ip4", g.target)
	if err != nil {
		return err
	}

	seq := 0
	ticker := time.NewTicker(g.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			seq++
			g.ping(conn, dst, seq)
		}
	}
}

func (g *ICMPGenerator) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}

func (g *ICMPGenerator) ping(conn *icmp.PacketConn, dst *net.IPAddr, seq int) {
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   os.Getpid() & 0xffff,
			Seq:  seq,
			Data: []byte("orbit-ping"),
		},
	}

	wb, err := msg.Marshal(nil)
	if err != nil {
		slog.Warn("icmp marshal error", "flow_id", g.flowID, "error", err)
		metrics.RecordGeneratorError(g.labels.FlowType, g.labels.Source, g.labels.Target, metrics.ReasonMarshalFailed)
		return
	}

	start := time.Now()
	if _, err := conn.WriteTo(wb, dst); err != nil {
		slog.Warn("icmp write error", "flow_id", g.flowID, "error", err)
		metrics.RecordGeneratorError(g.labels.FlowType, g.labels.Source, g.labels.Target, metrics.ReasonWriteFailed)
		return
	}

	g.recorder.AddPacketsSent(1)
	g.recorder.AddBytesSent(int64(len(wb)))
	metrics.GeneratorBytes.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Add(float64(len(wb)))
	metrics.AppPacketsSent.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()

	rb := make([]byte, 1500)
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return
	}

	n, _, err := conn.ReadFrom(rb)
	if err != nil {
		slog.Warn("icmp read error", "flow_id", g.flowID, "error", err)
		metrics.RecordGeneratorError(g.labels.FlowType, g.labels.Source, g.labels.Target, metrics.ReasonReadFailed)
		return
	}

	elapsed := time.Since(start)
	metrics.GeneratorLatency.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Observe(elapsed.Seconds())
	g.recorder.AddPacketsReceived(1)
	g.recorder.AddBytesReceived(int64(n))

	metrics.AppPacketsReceived.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()
	metrics.AppRequestDuration.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Observe(elapsed.Seconds())
}
