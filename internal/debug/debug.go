package debug

import (
	"strings"
	"sync/atomic"
)

const (
	ComponentTCPGenerator = "tcp-generator"
	ComponentTCPReceiver  = "tcp-receiver"
	ComponentWire         = "wire"
	ComponentChurn        = "churn"
	ComponentCoordinator  = "coordinator"
	ComponentDiscovery    = "discovery"
)

var global atomic.Pointer[map[string]struct{}]

// IsEnabled reports whether verbose debug logging is enabled for the named component.
// Returns true if "all" is set or the specific component is listed.
// Hot path: ~10ns no-op when disabled.
func IsEnabled(component string) bool {
	p := global.Load()
	if p == nil {
		return false
	}
	m := *p
	if _, ok := m["all"]; ok {
		return true
	}
	_, ok := m[component]
	return ok
}

// Set replaces the set of enabled components atomically.
// An empty slice disables all per-component debug logging.
func Set(components []string) {
	if len(components) == 0 {
		global.Store(nil)
		return
	}
	m := make(map[string]struct{}, len(components))
	for _, c := range components {
		m[strings.TrimSpace(c)] = struct{}{}
	}
	global.Store(&m)
}
