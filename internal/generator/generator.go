package generator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/daxroc/orbit/internal/recorder"
)

type Generator interface {
	Start(ctx context.Context) error
	Stop() error
	Type() string
	FlowID() string
}

type Manager struct {
	mu         sync.RWMutex
	generators map[string]Generator
	recorder   *recorder.AppRecorder
}

func NewManager(rec *recorder.AppRecorder) *Manager {
	return &Manager{
		generators: make(map[string]Generator),
		recorder:   rec,
	}
}

func (m *Manager) Add(g Generator) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generators[g.FlowID()] = g
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, g := range m.generators {
		slog.Info("starting generator", "flow_id", id, "type", g.Type())
		go func(gen Generator) {
			if err := gen.Start(ctx); err != nil {
				slog.Error("generator failed", "flow_id", gen.FlowID(), "type", gen.Type(), "error", err)
			}
		}(g)
	}
	return nil
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, g := range m.generators {
		slog.Info("stopping generator", "flow_id", id, "type", g.Type())
		if err := g.Stop(); err != nil {
			slog.Error("generator stop failed", "flow_id", id, "error", err)
		}
	}
	m.generators = make(map[string]Generator)
}

func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.generators)
}

type Labels struct {
	Scenario string
	RunID    string
	FlowType string
	Protocol string
	Source   string
	Target   string
}

func (l Labels) String() string {
	return fmt.Sprintf("%s/%s/%s->%s", l.Scenario, l.FlowType, l.Source, l.Target)
}
