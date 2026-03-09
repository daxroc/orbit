package generator

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/checksum"
	"github.com/daxroc/orbit/internal/metrics"
	"github.com/daxroc/orbit/internal/recorder"
	"golang.org/x/time/rate"
)

type HTTPGenerator struct {
	flowID       string
	labels       Labels
	target       string
	rps          int
	payloadBytes int
	method       string
	keepAlive    bool
	workers      int
	duration     time.Duration
	validator    *auth.TokenValidator
	recorder     *recorder.AppRecorder

	bufPool sync.Pool
	mu      sync.Mutex
	cancel  context.CancelFunc
}

func NewHTTPGenerator(flowID string, labels Labels, target string, rps, payloadBytes int, method string, keepAlive bool, workers int, duration time.Duration, validator *auth.TokenValidator, rec *recorder.AppRecorder) *HTTPGenerator {
	if rps <= 0 {
		rps = 10
	}
	if payloadBytes <= 0 {
		payloadBytes = 512
	}
	if method == "" {
		method = http.MethodPost
	}
	if workers <= 0 {
		workers = 4
	}
	return &HTTPGenerator{
		flowID:       flowID,
		labels:       labels,
		target:       target,
		rps:          rps,
		payloadBytes: payloadBytes,
		method:       method,
		keepAlive:    keepAlive,
		workers:      workers,
		duration:     duration,
		validator:    validator,
		recorder:     rec,
		bufPool: sync.Pool{
			New: func() any { b := make([]byte, 0, payloadBytes*2); return &b },
		},
	}
}

func (g *HTTPGenerator) Type() string   { return "http" }
func (g *HTTPGenerator) FlowID() string { return g.flowID }

func (g *HTTPGenerator) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	g.mu.Lock()
	g.cancel = cancel
	g.mu.Unlock()

	if g.duration > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, g.duration)
		defer c()
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives:   !g.keepAlive,
			MaxIdleConns:        g.workers + 2,
			MaxIdleConnsPerHost: g.workers + 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	payload := make([]byte, g.payloadBytes)
	_, _ = rand.Read(payload)

	burst := g.rps / g.workers
	if burst < 1 {
		burst = 1
	}
	limiter := rate.NewLimiter(rate.Limit(g.rps), burst)

	var wg sync.WaitGroup
	for i := 0; i < g.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if err := limiter.Wait(ctx); err != nil {
					return
				}
				g.sendRequest(ctx, client, payload)
			}
		}()
	}
	wg.Wait()
	return nil
}

func (g *HTTPGenerator) Stop() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cancel != nil {
		g.cancel()
	}
	return nil
}

func (g *HTTPGenerator) sendRequest(ctx context.Context, client *http.Client, payload []byte) {
	url := fmt.Sprintf("http://%s/echo", g.target)
	req, err := http.NewRequestWithContext(ctx, g.method, url, bytes.NewReader(payload))
	if err != nil {
		slog.Warn("http request create failed", "flow_id", g.flowID, "error", err)
		metrics.GeneratorErrors.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Inc()
		return
	}
	req.Header.Set("Authorization", g.validator.AuthorizationHeader())
	req.Header.Set("X-Orbit-Flow-ID", g.flowID)
	req.Header.Set("X-Orbit-Checksum", checksum.ComputeHex(payload))

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)

	if err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.Warn("http request failed", "flow_id", g.flowID, "error", err)
		metrics.GeneratorErrors.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Inc()
		return
	}

	bufp := g.bufPool.Get().(*[]byte)
	buf := (*bufp)[:cap(*bufp)]
	n, _ := io.ReadFull(resp.Body, buf)
	buf = buf[:n]
	resp.Body.Close()

	if csHeader := resp.Header.Get("X-Orbit-Checksum"); csHeader != "" {
		expected, err := hex.DecodeString(csHeader)
		if err == nil && !checksum.Verify(buf, expected) {
			metrics.ChecksumErrors.WithLabelValues(g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target).Inc()
		}
	}

	g.recorder.AddBytesSent(int64(len(payload)))
	g.recorder.AddBytesReceived(int64(len(buf)))
	g.recorder.AddConnection()
	g.recorder.RemoveConnection()

	metrics.AppBytesSent.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, g.labels.Direction,
	).Add(float64(len(payload)))
	metrics.AppBytesReceived.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target, g.labels.Direction,
	).Add(float64(len(buf)))
	metrics.AppRequestDuration.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Observe(elapsed.Seconds())
	metrics.AppConnectionsTotal.WithLabelValues(
		g.labels.Scenario, g.labels.RunID, g.labels.FlowType, g.labels.Protocol, g.labels.Source, g.labels.Target,
	).Inc()
	metrics.GeneratorBytes.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Add(float64(len(payload)))
	metrics.GeneratorLatency.WithLabelValues(g.labels.FlowType, g.labels.Source, g.labels.Target).Observe(elapsed.Seconds())

	*bufp = buf[:0]
	g.bufPool.Put(bufp)
}
