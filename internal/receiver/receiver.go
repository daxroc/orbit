package receiver

import (
	"context"
	"log/slog"
	"net"
	"sync"

	"github.com/daxroc/orbit/internal/auth"
	"github.com/daxroc/orbit/internal/recorder"
)

func stripPort(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

type Receiver interface {
	Start(ctx context.Context) error
	Stop() error
	Type() string
}

type Manager struct {
	mu        sync.RWMutex
	receivers []Receiver
	validator *auth.TokenValidator
	recorder  *recorder.AppRecorder
}

func NewManager(validator *auth.TokenValidator, rec *recorder.AppRecorder) *Manager {
	return &Manager{
		validator: validator,
		recorder:  rec,
	}
}

func (m *Manager) Add(r Receiver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.receivers = append(m.receivers, r)
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, r := range m.receivers {
		slog.Info("starting receiver", "type", r.Type())
		go func(recv Receiver) {
			if err := recv.Start(ctx); err != nil {
				slog.Error("receiver failed", "type", recv.Type(), "error", err)
			}
		}(r)
	}
	return nil
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.receivers {
		slog.Info("stopping receiver", "type", r.Type())
		if err := r.Stop(); err != nil {
			slog.Error("receiver stop failed", "type", r.Type(), "error", err)
		}
	}
	m.receivers = nil
}
