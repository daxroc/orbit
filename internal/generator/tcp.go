package generator

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	"golang.org/x/time/rate"
)

type TCPGenerator struct {
	flowID    string
	labels    Labels
	target    string
	bandwidth int64
	payload   int
	conns     int
	duration  time.Duration
	pattern   string
	validator *auth.TokenValidator
	recorder  *recorder.AppRecorder
	wireRec   *recorder.WireRecorder

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewTCPGenerator(flowID string, labels Labels, target string, bandwidthBps int64, payloadBytes, connections int, duration time.Duration, pattern string, validator *auth.TokenValidator, rec *recorder.AppRecorder, wireRec *recorder.WireRecorder) *TCPGenerator {
	if payloadBytes <= 0 {
		payloadBytes = 1400
	}
	if connections <= 0 {
		connections = 1
	}
	return &TCPGenerator{
		flowID:    flowID,
		labels:    labels,
		target:    target,
		bandwidth: bandwidthBps,
		payload:   payloadBytes,
		conns:     connections,
		duration:  duration,
		pattern:   pattern,
		validator: validator,
		recorder:  rec,
		wireRec:   wireRec,
	}
}

func (g *TCPGenerator) Type() string   { return "tcp-stream" }
func (g *TCPGenerator) FlowID() string { return g.flowID }

func (g *TCPGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	if g.duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, g.duration)
		defer c()
	}

	var wg sync.WaitGroup
	perConnBandwidth := g.bandwidth / int64(g.conns)

	for i := 0; i < g.conns; i++ {
		wg.Add(1)
		go func(connIdx int) {
			defer wg.Done()
			g.runConnection(ctx, connIdx, perConnBandwidth)
		}(i)
	}

	wg.Wait()
	return nil
}

func (g *TCPGenerator) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}

func (g *TCPGenerator) runConnection(ctx context.Context, idx int, bandwidthBps int64) {
	conn, err := net.DialTimeout("tcp", g.target, 5*time.Second)
	if err != nil {
		slog.Error("tcp connect failed", "flow_id", g.flowID, "target", g.target, "error", err)
		metrics.GeneratorErrors.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Inc()
		return
	}
	defer func() {
		if g.wireRec != nil {
			g.wireRec.RemoveConn(conn, g.target, g.labels.Protocol)
		}
		conn.Close()
	}()

	g.recorder.AddConnection()
	defer g.recorder.RemoveConnection()

	metrics.AppConnectionsTotal.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()
	metrics.AppConnectionsActive.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()
	defer metrics.AppConnectionsActive.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Dec()

	if _, err := conn.Write(g.validator.HandshakeBytes()); err != nil {
		slog.Error("tcp handshake failed", "flow_id", g.flowID, "error", err)
		metrics.GeneratorErrors.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Inc()
		return
	}

	buf := make([]byte, g.payload)
	_, _ = rand.Read(buf)

	var limiter *rate.Limiter
	if bandwidthBps > 0 {
		limiter = rate.NewLimiter(rate.Limit(float64(bandwidthBps)/float64(g.payload)), 10)
	}

	cw := recorder.NewCountingWriter(conn, g.recorder)
	lastReport := time.Now()
	var bytesSinceReport int64

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if limiter != nil {
			if err := limiter.Wait(ctx); err != nil {
				return
			}
		}

		n, err := cw.Write(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("tcp write error", "flow_id", g.flowID, "error", err)
			metrics.GeneratorErrors.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Inc()
			return
		}

		metrics.AppBytesSent.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, g.labels.Direction,
		).Add(float64(n))
		metrics.GeneratorBytes.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Add(float64(n))

		bytesSinceReport += int64(n)
		if time.Since(lastReport) >= time.Second {
			metrics.AppThroughput.WithLabelValues(
				g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
			).Set(float64(bytesSinceReport))
			bytesSinceReport = 0
			lastReport = time.Now()

			if g.wireRec != nil {
				g.wireRec.CollectTCPInfo(conn, g.target, g.labels.Protocol)
			}
		}

		_, _ = io.ReadFull(conn, make([]byte, 1))
	}
}

func TCPLabels(scenario, runID, source, target, direction string) Labels {
	return Labels{
		Scenario:  scenario,
		RunID:     runID,
		FlowType:  "tcp-stream",
		Protocol:  "tcp",
		Source:    source,
		Target:    target,
		Direction: direction,
	}
}
