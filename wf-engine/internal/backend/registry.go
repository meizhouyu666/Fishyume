package backend

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry owns the Agent Backends available to one Engine process.
// Registration is normally completed during composition, while reads may be
// performed concurrently by run controllers and RPC handlers.
type Registry struct {
	mu       sync.RWMutex
	backends map[string]AgentBackend
}

func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]AgentBackend)}
}

func (r *Registry) Register(candidate AgentBackend) error {
	if candidate == nil {
		return fmt.Errorf("Backend is required")
	}
	name := strings.TrimSpace(candidate.Name())
	if name == "" {
		return fmt.Errorf("Backend name is required")
	}
	if name != candidate.Name() {
		return fmt.Errorf("Backend name %q must not contain surrounding whitespace", candidate.Name())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.backends[name]; exists {
		return fmt.Errorf("Backend %q is already registered", name)
	}
	r.backends[name] = candidate
	return nil
}

func (r *Registry) Get(name string) (AgentBackend, error) {
	name = strings.TrimSpace(name)
	r.mu.RLock()
	candidate, ok := r.backends[name]
	r.mu.RUnlock()
	if ok {
		return candidate, nil
	}
	names := r.Names()
	if len(names) == 0 {
		return nil, fmt.Errorf("unknown Backend %q; no Backends are registered", name)
	}
	return nil, fmt.Errorf("unknown Backend %q; available Backends: %s", name, strings.Join(names, ", "))
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.backends))
	for name := range r.backends {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}
