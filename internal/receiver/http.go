package receiver

import (
	"context"
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
)

type HTTPReceiver struct {
	port      int
	validator *auth.TokenValidator
	recorder  *recorder.AppRecorder
	srv       *http.Server

	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewHTTPReceiver(port int, validator *auth.TokenValidator, rec *recorder.AppRecorder) *HTTPReceiver {
	r := &HTTPReceiver{
		port:      port,
		validator: validator,
		recorder:  rec,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", r.handleEcho)

	r.srv = &http.Server{
		Addr:           fmt.Sprintf(":%d", port),
		Handler:        validator.HTTPMiddleware(false, mux),
		ReadTimeout:    30 * time.Second,
		WriteTimeout:   30 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 16,
	}

	return r
}

func (r *HTTPReceiver) Type() string { return "http-echo" }

func (r *HTTPReceiver) Start(ctx context.Context) error {
	_, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.cancel = cancel
	r.mu.Unlock()

	slog.Info("HTTP echo receiver listening", "port", r.port)
	if err := r.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		cancel()
		return err
	}
	return nil
}

func (r *HTTPReceiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return r.srv.Shutdown(ctx)
}

const maxEchoBodyBytes = 1 << 20

func (r *HTTPReceiver) handleEcho(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	req.Body = http.MaxBytesReader(w, req.Body, maxEchoBodyBytes)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	r.recorder.AddBytesReceived(int64(len(body)))
	metrics.ReceiverBytes.WithLabelValues("http").Add(float64(len(body)))
	metrics.ReceiverConnections.WithLabelValues("http").Inc()
	source := stripPort(req.RemoteAddr)
	target := stripPort(req.Host)

	metrics.AppBytesReceived.WithLabelValues(
		"", "", "http", "http", source, target, "east-west",
	).Add(float64(len(body)))

	if csHeader := req.Header.Get("X-Orbit-Checksum"); csHeader != "" {
		expected, err := hex.DecodeString(csHeader)
		if err == nil && !checksum.Verify(body, expected) {
			flowID := req.Header.Get("X-Orbit-Flow-ID")
			metrics.ChecksumErrors.WithLabelValues("http", "http", source, target).Inc()
			slog.Warn("checksum mismatch", "flow_id", flowID, "source", source)
		}
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Orbit-Flow-ID", req.Header.Get("X-Orbit-Flow-ID"))
	w.Header().Set("X-Orbit-Checksum", checksum.ComputeHex(body))
	n, _ := w.Write(body)

	r.recorder.AddBytesSent(int64(n))
	metrics.AppBytesSent.WithLabelValues(
		"", "", "http", "http", target, source, "east-west",
	).Add(float64(n))
}
