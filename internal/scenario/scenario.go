package scenario

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/daxroc/orbit/internal/config"
	"gopkg.in/yaml.v3"
)

type ActiveChangeFunc func(scenarioName string)

const defaultStabilizationPeriod = 10 * time.Second

type Engine struct {
	mu                  sync.RWMutex
	scenarios           map[string]config.Scenario
	satellites          []config.SatelliteTarget
	active              string
	runID               string
	configActive        string
	stabilizationPeriod time.Duration
	onActiveChange      ActiveChangeFunc
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

	stabPeriod := defaultStabilizationPeriod
	if sc.StabilizationPeriod != "" {
		if d, err := time.ParseDuration(sc.StabilizationPeriod); err == nil {
			stabPeriod = d
		} else {
			slog.Warn("invalid stabilizationPeriod, using default", "value", sc.StabilizationPeriod, "default", defaultStabilizationPeriod)
		}
	}

	e.mu.Lock()
	e.scenarios = sc.Scenarios
	e.satellites = sc.Satellites
	e.stabilizationPeriod = stabPeriod
	prevActive := e.active
	configActive := sc.ActiveScenario
	e.active = configActive
	e.configActive = configActive
	var cb ActiveChangeFunc
	if configActive != prevActive {
		cb = e.onActiveChange
	}
	e.mu.Unlock()

	slog.Info("loaded scenarios", "count", len(e.scenarios), "satellites", len(sc.Satellites), "activeScenario", configActive)
	for name, s := range sc.Scenarios {
		slog.Info("scenario available",
			"name", name,
			"description", s.Description,
			"east_west_flows", len(s.EastWest),
			"north_south_flows", len(s.NorthSouth),
		)
	}
	for i, sat := range sc.Satellites {
		if sat.Host == "" {
			slog.Warn("satellite missing host, will be skipped at activation", "index", i, "name", sat.Name)
		}
		if sat.Name == "" {
			slog.Warn("satellite missing name", "index", i, "host", sat.Host)
		}
		slog.Info("satellite registered", "name", sat.Name, "host", sat.Host, "flows", len(sat.Flows))
	}

	if cb != nil {
		slog.Info("active scenario changed via config", "from", prevActive, "to", configActive)
		cb(configActive)
	}
	return nil
}

func (e *Engine) SetOnActiveChange(fn ActiveChangeFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onActiveChange = fn
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

func (e *Engine) ConfigActive() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.configActive
}

func (e *Engine) StabilizationPeriod() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.stabilizationPeriod <= 0 {
		return defaultStabilizationPeriod
	}
	return e.stabilizationPeriod
}

func (e *Engine) Satellites() []config.SatelliteTarget {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]config.SatelliteTarget, len(e.satellites))
	for i, sat := range e.satellites {
		out[i] = sat
		out[i].Flows = make([]config.FlowDef, len(sat.Flows))
		copy(out[i].Flows, sat.Flows)
	}
	return out
}

func (e *Engine) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.active = ""
	e.runID = ""
}
