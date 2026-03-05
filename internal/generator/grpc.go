package generator

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	orbitv1 "github.com/daxroc/orbit/proto/orbit/v1"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type GRPCGenerator struct {
	flowID       string
	labels       Labels
	target       string
	rps          int
	payloadBytes int
	duration     time.Duration
	validator    *auth.TokenValidator
	recorder     *recorder.AppRecorder

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewGRPCGenerator(flowID string, labels Labels, target string, rps, payloadBytes int, duration time.Duration, validator *auth.TokenValidator, rec *recorder.AppRecorder) *GRPCGenerator {
	if rps <= 0 {
		rps = 10
	}
	if payloadBytes <= 0 {
		payloadBytes = 512
	}
	return &GRPCGenerator{
		flowID:       flowID,
		labels:       labels,
		target:       target,
		rps:          rps,
		payloadBytes: payloadBytes,
		duration:     duration,
		validator:    validator,
		recorder:     rec,
	}
}

func (g *GRPCGenerator) Type() string   { return "grpc" }
func (g *GRPCGenerator) FlowID() string { return g.flowID }

func (g *GRPCGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	if g.duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, g.duration)
		defer c()
	}

	conn, err := grpc.NewClient(g.target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		g.validator.GRPCDialOption(),
		g.validator.GRPCStreamDialOption(),
	)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := orbitv1.NewOrbitServiceClient(conn)
	payload := make([]byte, g.payloadBytes)
	_, _ = rand.Read(payload)

	limiter := rate.NewLimiter(rate.Limit(g.rps), 10)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		if err := limiter.Wait(ctx); err != nil {
			return nil
		}

		start := time.Now()
		resp, err := client.Echo(ctx, &orbitv1.EchoRequest{
			Payload:  payload,
			SentAt:   timestamppb.Now(),
			SourceId: g.labels.Source,
		})
		elapsed := time.Since(start)

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			slog.Warn("grpc echo failed", "flow_id", g.flowID, "error", err)
			continue
		}

		g.recorder.AddBytesSent(int64(len(payload)))
		g.recorder.AddBytesReceived(int64(len(resp.Payload)))

		metrics.AppBytesSent.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, g.labels.Direction,
		).Add(float64(len(payload)))
		metrics.AppBytesReceived.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, g.labels.Direction,
		).Add(float64(len(resp.Payload)))
		metrics.AppRequestDuration.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
		).Observe(elapsed.Seconds())
		metrics.AppConnectionsTotal.WithLabelValues(
			g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
		).Inc()
	}
}

func (g *GRPCGenerator) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}
