package generator

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	"golang.org/x/time/rate"
)

type ChurnGenerator struct {
	flowID         string
	labels         Labels
	target         string
	connsPerSecond int
	holdDuration   time.Duration
	duration       time.Duration
	validator      *auth.TokenValidator
	recorder       *recorder.AppRecorder

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewChurnGenerator(flowID string, labels Labels, target string, connsPerSecond int, holdDurationMs int, duration time.Duration, validator *auth.TokenValidator, rec *recorder.AppRecorder) *ChurnGenerator {
	if connsPerSecond <= 0 {
		connsPerSecond = 100
	}
	if holdDurationMs <= 0 {
		holdDurationMs = 50
	}
	return &ChurnGenerator{
		flowID:         flowID,
		labels:         labels,
		target:         target,
		connsPerSecond: connsPerSecond,
		holdDuration:   time.Duration(holdDurationMs) * time.Millisecond,
		duration:       duration,
		validator:      validator,
		recorder:       rec,
	}
}

func (g *ChurnGenerator) Type() string   { return "connection-churn" }
func (g *ChurnGenerator) FlowID() string { return g.flowID }

func (g *ChurnGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	if g.duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, g.duration)
		defer c()
	}

	limiter := rate.NewLimiter(rate.Limit(g.connsPerSecond), 10)
	sem := make(chan struct{}, g.connsPerSecond*2)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err := limiter.Wait(ctx); err != nil {
			return nil
		}

		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			g.churnOnce(ctx)
		}()
	}
}

func (g *ChurnGenerator) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}

func (g *ChurnGenerator) churnOnce(ctx context.Context) {
	conn, err := net.DialTimeout("tcp", g.target, 2*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.Warn("churn connect failed", "flow_id", g.flowID, "error", err)
		return
	}

	g.recorder.AddConnection()
	metrics.AppConnectionsTotal.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()
	metrics.AppConnectionsActive.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()

	if _, err := conn.Write(g.validator.HandshakeBytes()); err != nil {
		conn.Close()
		g.recorder.RemoveConnection()
		metrics.AppConnectionsActive.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
		).Dec()
		return
	}

	g.recorder.AddBytesSent(int64(len(g.validator.HandshakeBytes())))
	metrics.AppBytesSent.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, "east-west",
	).Add(float64(len(g.validator.HandshakeBytes())))

	select {
	case <-ctx.Done():
	case <-time.After(g.holdDuration):
	}

	conn.Close()
	g.recorder.RemoveConnection()
	metrics.AppConnectionsActive.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Dec()
}
