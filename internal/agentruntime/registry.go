package agentruntime

import (
	"fmt"
	"strings"
	"sync"
)

type Registry struct {
	mu              sync.RWMutex
	runners         map[string]Runner
	defaultProvider string
}

func NewRegistry() *Registry {
	return &Registry{
		runners: make(map[string]Runner),
	}
}

func (r *Registry) Register(provider string, runner Runner) error {
	normalized := normalizeProvider(provider)
	if normalized == "" {
		return ErrProviderRequired
	}
	if runner == nil {
		return ErrRunnerRequired
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.runners[normalized] = runner

	return nil
}

func (r *Registry) SetDefault(provider string) error {
	normalized := normalizeProvider(provider)
	if normalized == "" {
		return ErrProviderRequired
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.runners[normalized]; !ok {
		return fmt.Errorf("%w: %s", ErrRunnerNotRegistered, normalized)
	}

	r.defaultProvider = normalized
	return nil
}

func (r *Registry) DefaultProvider() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.defaultProvider
}

func (r *Registry) Get(provider string) (Runner, error) {
	normalized := normalizeProvider(provider)

	r.mu.RLock()
	defer r.mu.RUnlock()

	if normalized == "" {
		if r.defaultProvider == "" {
			return nil, ErrDefaultProviderNotSet
		}
		normalized = r.defaultProvider
	}

	runner, ok := r.runners[normalized]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrRunnerNotRegistered, normalized)
	}
	return runner, nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
