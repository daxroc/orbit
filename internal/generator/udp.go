package generator

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	"golang.org/x/time/rate"
)

type UDPGenerator struct {
	flowID     string
	labels     Labels
	target     string
	packetRate int
	packetSize int
	duration   time.Duration
	validator  *auth.TokenValidator
	recorder   *recorder.AppRecorder

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewUDPGenerator(flowID string, labels Labels, target string, packetRate, packetSize int, duration time.Duration, validator *auth.TokenValidator, rec *recorder.AppRecorder) *UDPGenerator {
	if packetSize <= 0 {
		packetSize = 1400
	}
	if packetRate <= 0 {
		packetRate = 100
	}
	return &UDPGenerator{
		flowID:     flowID,
		labels:     labels,
		target:     target,
		packetRate: packetRate,
		packetSize: packetSize,
		duration:   duration,
		validator:  validator,
		recorder:   rec,
	}
}

func (g *UDPGenerator) Type() string   { return "udp-stream" }
func (g *UDPGenerator) FlowID() string { return g.flowID }

func (g *UDPGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	if g.duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, g.duration)
		defer c()
	}

	addr, err := net.ResolveUDPAddr("udp", g.target)
	if err != nil {
		return fmt.Errorf("resolve udp addr: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("dial udp: %w", err)
	}
	defer conn.Close()

	tokenBytes := g.validator.HandshakeBytes()
	buf := make([]byte, g.packetSize)
	copy(buf, tokenBytes)
	_, _ = rand.Read(buf[len(tokenBytes):])

	limiter := rate.NewLimiter(rate.Limit(g.packetRate), 10)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err := limiter.Wait(ctx); err != nil {
			return nil
		}

		n, err := conn.Write(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("udp write error", "flow_id", g.flowID, "error", err)
			metrics.RecordGeneratorError(g.labels.FlowType, g.labels.Source, g.labels.Target, metrics.ReasonWriteFailed)
			continue
		}

		g.recorder.AddBytesSent(int64(n))
		g.recorder.AddPacketsSent(1)
		metrics.AppBytesSent.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, g.labels.Direction,
		).Add(float64(n))
		metrics.GeneratorBytes.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Add(float64(n))
		metrics.AppPacketsSent.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
		).Inc()
	}
}

func (g *UDPGenerator) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}
