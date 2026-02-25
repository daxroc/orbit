package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type HTTPServer struct {
	srv       *http.Server
	validator *auth.TokenValidator
	port      int
	statusFn  func() StatusResponse
	mu        sync.RWMutex
}

type StatusResponse struct {
	PodName        string `json:"podName"`
	Mode           string `json:"mode"`
	Leader         bool   `json:"leader"`
	LeaderID       string `json:"leaderId"`
	PeerCount      int    `json:"peerCount"`
	ActiveScenario string `json:"activeScenario"`
	RunID          string `json:"runId"`
	ActiveFlows    int    `json:"activeFlows"`
	Uptime         string `json:"uptime"`
}

func NewHTTPServer(port int, validator *auth.TokenValidator, protectMetrics bool) *HTTPServer {
	s := &HTTPServer{
		validator: validator,
		port:      port,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/status", s.handleStatus)

	handler := validator.HTTPMiddleware(protectMetrics, mux)

	s.srv = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

func (s *HTTPServer) SetStatusFunc(fn func() StatusResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusFn = fn
}

func (s *HTTPServer) Start() error {
	slog.Info("starting HTTP server", "port", s.port)
	if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

func (s *HTTPServer) Stop(ctx context.Context) error {
	slog.Info("stopping HTTP server")
	return s.srv.Shutdown(ctx)
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *HTTPServer) handleReady(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *HTTPServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	fn := s.statusFn
	s.mu.RUnlock()

	if fn == nil {
		http.Error(w, "status not available", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fn())
}
