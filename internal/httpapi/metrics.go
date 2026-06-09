package httpapi

import (
	"fmt"
	"sync"
	"time"
)

// MetricsCollector collects and exposes Prometheus-compatible metrics.
type MetricsCollector struct {
	mu sync.RWMutex

	// HTTP metrics
	httpRequestsTotal   map[string]int64 // method+path -> count
	httpRequestDuration map[string][]time.Duration

	// Business metrics
	projectsCreated   int64
	tasksDispatched   int64
	runsStarted       int64
	runsCompleted     int64
	runsFailed        int64
	overridesApplied  int64
	conflictsResolved int64

	// LLM metrics
	llmRequestsTotal   map[string]int64 // provider -> count
	llmTokensTotal     map[string]int64 // provider -> tokens
	llmErrorsTotal     map[string]int64 // provider -> count
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		httpRequestsTotal:   make(map[string]int64),
		httpRequestDuration: make(map[string][]time.Duration),
		llmRequestsTotal:    make(map[string]int64),
		llmTokensTotal:      make(map[string]int64),
		llmErrorsTotal:      make(map[string]int64),
	}
}

// RecordHTTPRequest records an HTTP request metric.
func (m *MetricsCollector) RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s %s %d", method, path, status)
	m.httpRequestsTotal[key]++
	m.httpRequestDuration[key] = append(m.httpRequestDuration[key], duration)
}

// IncProjectsCreated increments the projects created counter.
func (m *MetricsCollector) IncProjectsCreated() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projectsCreated++
}

// IncTasksDispatched increments the tasks dispatched counter.
func (m *MetricsCollector) IncTasksDispatched() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasksDispatched++
}

// IncRunsStarted increments the runs started counter.
func (m *MetricsCollector) IncRunsStarted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runsStarted++
}

// IncRunsCompleted increments the runs completed counter.
func (m *MetricsCollector) IncRunsCompleted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runsCompleted++
}

// IncRunsFailed increments the runs failed counter.
func (m *MetricsCollector) IncRunsFailed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runsFailed++
}

// IncOverridesApplied increments the overrides applied counter.
func (m *MetricsCollector) IncOverridesApplied() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.overridesApplied++
}

// IncConflictsResolved increments the conflicts resolved counter.
func (m *MetricsCollector) IncConflictsResolved() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conflictsResolved++
}

// RecordLLMRequest records an LLM request metric.
func (m *MetricsCollector) RecordLLMRequest(provider string, tokens int, err bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.llmRequestsTotal[provider]++
	m.llmTokensTotal[provider] += int64(tokens)
	if err {
		m.llmErrorsTotal[provider]++
	}
}

// RenderPrometheus renders all metrics in Prometheus text format.
func (m *MetricsCollector) RenderPrometheus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out string

	// HTTP metrics
	out += "# HELP http_requests_total Total HTTP requests\n"
	out += "# TYPE http_requests_total counter\n"
	for key, count := range m.httpRequestsTotal {
		out += fmt.Sprintf("http_requests_total{request=\"%s\"} %d\n", key, count)
	}

	out += "# HELP http_request_duration_seconds HTTP request duration\n"
	out += "# TYPE http_request_duration_seconds summary\n"
	for key, durations := range m.httpRequestDuration {
		if len(durations) == 0 {
			continue
		}
		var total time.Duration
		for _, d := range durations {
			total += d
		}
		avg := total.Seconds() / float64(len(durations))
		out += fmt.Sprintf("http_request_duration_seconds{request=\"%s\"} %.6f\n", key, avg)
	}

	// Business metrics
	out += "# HELP projects_created_total Total projects created\n"
	out += "# TYPE projects_created_total counter\n"
	out += fmt.Sprintf("projects_created_total %d\n", m.projectsCreated)

	out += "# HELP tasks_dispatched_total Total tasks dispatched\n"
	out += "# TYPE tasks_dispatched_total counter\n"
	out += fmt.Sprintf("tasks_dispatched_total %d\n", m.tasksDispatched)

	out += "# HELP agent_runs_started_total Total agent runs started\n"
	out += "# TYPE agent_runs_started_total counter\n"
	out += fmt.Sprintf("agent_runs_started_total %d\n", m.runsStarted)

	out += "# HELP agent_runs_completed_total Total agent runs completed\n"
	out += "# TYPE agent_runs_completed_total counter\n"
	out += fmt.Sprintf("agent_runs_completed_total %d\n", m.runsCompleted)

	out += "# HELP agent_runs_failed_total Total agent runs failed\n"
	out += "# TYPE agent_runs_failed_total counter\n"
	out += fmt.Sprintf("agent_runs_failed_total %d\n", m.runsFailed)

	out += "# HELP overrides_applied_total Total human overrides applied\n"
	out += "# TYPE overrides_applied_total counter\n"
	out += fmt.Sprintf("overrides_applied_total %d\n", m.overridesApplied)

	out += "# HELP conflicts_resolved_total Total conflicts resolved\n"
	out += "# TYPE conflicts_resolved_total counter\n"
	out += fmt.Sprintf("conflicts_resolved_total %d\n", m.conflictsResolved)

	// LLM metrics
	out += "# HELP llm_requests_total Total LLM requests by provider\n"
	out += "# TYPE llm_requests_total counter\n"
	for provider, count := range m.llmRequestsTotal {
		out += fmt.Sprintf("llm_requests_total{provider=\"%s\"} %d\n", provider, count)
	}

	out += "# HELP llm_tokens_total Total LLM tokens by provider\n"
	out += "# TYPE llm_tokens_total counter\n"
	for provider, tokens := range m.llmTokensTotal {
		out += fmt.Sprintf("llm_tokens_total{provider=\"%s\"} %d\n", provider, tokens)
	}

	out += "# HELP llm_errors_total Total LLM errors by provider\n"
	out += "# TYPE llm_errors_total counter\n"
	for provider, count := range m.llmErrorsTotal {
		out += fmt.Sprintf("llm_errors_total{provider=\"%s\"} %d\n", provider, count)
	}

	return out
}
