package scenario

import (
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/daxroc/orbit/internal/config"
	"gopkg.in/yaml.v3"
)

type Engine struct {
	mu        sync.RWMutex
	scenarios map[string]config.Scenario
	active    string
	runID     string
}

func NewEngine() *Engine {
	return &Engine{
		scenarios: make(map[string]config.Scenario),
	}
}

func (e *Engine) LoadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read scenarios file: %w", err)
	}

	var sc config.ScenariosConfig
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("parse scenarios: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.scenarios = sc.Scenarios

	slog.Info("loaded scenarios", "count", len(e.scenarios))
	for name, s := range e.scenarios {
		slog.Info("scenario available",
			"name", name,
			"description", s.Description,
			"east_west_flows", len(s.EastWest),
			"north_south_flows", len(s.NorthSouth),
		)
	}
	return nil
}

func (e *Engine) Get(name string) (config.Scenario, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.scenarios[name]
	return s, ok
}

func (e *Engine) List() map[string]config.Scenario {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make(map[string]config.Scenario, len(e.scenarios))
	for k, v := range e.scenarios {
		result[k] = v
	}
	return result
}

func (e *Engine) SetActive(name, runID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active = name
	e.runID = runID
}

func (e *Engine) Active() (string, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.active, e.runID
}

func (e *Engine) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active = ""
	e.runID = ""
}
