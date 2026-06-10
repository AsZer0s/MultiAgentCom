package httpapi

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Histogram buckets in seconds.
var defaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// MetricsCollector collects and exposes Prometheus-compatible metrics.
type MetricsCollector struct {
	mu sync.RWMutex

	// HTTP metrics (bounded memory)
	httpRequestsTotal    map[string]int64              // method+path+status -> count
	httpRequestSum       map[string]float64            // method+path -> total seconds
	httpRequestCount     map[string]int64              // method+path -> count (for avg)
	httpRequestHistogram map[string]map[float64]int64  // method+path -> bucket -> count

	// Business metrics
	projectsCreated   int64
	tasksDispatched   int64
	runsStarted       int64
	runsCompleted     int64
	runsFailed        int64
	overridesApplied  int64
	conflictsResolved int64

	// LLM metrics
	llmRequestsTotal   map[string]int64
	llmTokensTotal     map[string]int64
	llmErrorsTotal     map[string]int64
	llmDurationTotal   map[string]time.Duration
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		httpRequestsTotal:    make(map[string]int64),
		httpRequestSum:       make(map[string]float64),
		httpRequestCount:     make(map[string]int64),
		httpRequestHistogram: make(map[string]map[float64]int64),
		llmRequestsTotal:     make(map[string]int64),
		llmTokensTotal:       make(map[string]int64),
		llmErrorsTotal:       make(map[string]int64),
		llmDurationTotal:     make(map[string]time.Duration),
	}
}

// RecordHTTPRequest records an HTTP request metric with histogram bucket.
func (m *MetricsCollector) RecordHTTPRequest(method, path string, status int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s %s %d", method, path, status)
	m.httpRequestsTotal[key]++

	// Use running sum instead of storing every duration.
	histKey := fmt.Sprintf("%s %s", method, path)
	m.httpRequestSum[histKey] += duration.Seconds()
	m.httpRequestCount[histKey]++

	// Record histogram bucket.
	if m.httpRequestHistogram[histKey] == nil {
		m.httpRequestHistogram[histKey] = make(map[float64]int64)
	}
	secs := duration.Seconds()
	for _, bucket := range defaultBuckets {
		if secs <= bucket {
			m.httpRequestHistogram[histKey][bucket]++
		}
	}
	m.httpRequestHistogram[histKey][0]++ // +Inf bucket (total)
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
func (m *MetricsCollector) RecordLLMRequest(provider string, tokens int, duration time.Duration, err bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.llmRequestsTotal[provider]++
	m.llmTokensTotal[provider] += int64(tokens)
	m.llmDurationTotal[provider] += duration
	if err {
		m.llmErrorsTotal[provider]++
	}
}

// RenderPrometheus renders all metrics in Prometheus text format.
func (m *MetricsCollector) RenderPrometheus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out strings.Builder

	// HTTP request total counter.
	out.WriteString("# HELP http_requests_total Total HTTP requests\n")
	out.WriteString("# TYPE http_requests_total counter\n")
	for key, count := range m.httpRequestsTotal {
		fmt.Fprintf(&out, "http_requests_total{request=\"%s\"} %d\n", key, count)
	}

	// HTTP request duration histogram.
	out.WriteString("# HELP http_request_duration_seconds HTTP request duration histogram\n")
	out.WriteString("# TYPE http_request_duration_seconds histogram\n")
	histKeys := make([]string, 0, len(m.httpRequestHistogram))
	for k := range m.httpRequestHistogram {
		histKeys = append(histKeys, k)
	}
	sort.Strings(histKeys)
	for _, key := range histKeys {
		buckets := m.httpRequestHistogram[key]
		var cumulative int64
		bucketNums := make([]float64, 0, len(buckets))
		for b := range buckets {
			bucketNums = append(bucketNums, b)
		}
		sort.Float64s(bucketNums)
		for _, bucket := range bucketNums {
			if bucket == 0 {
				continue
			}
			cumulative += buckets[bucket]
			fmt.Fprintf(&out, "http_request_duration_seconds_bucket{request=\"%s\",le=\"%.3f\"} %d\n", key, bucket, cumulative)
		}
		total := buckets[0]
		if total == 0 {
			total = cumulative
		}
		fmt.Fprintf(&out, "http_request_duration_seconds_bucket{request=\"%s\",le=\"+Inf\"} %d\n", key, total)
		fmt.Fprintf(&out, "http_request_duration_seconds_count{request=\"%s\"} %d\n", key, total)
		fmt.Fprintf(&out, "http_request_duration_seconds_sum{request=\"%s\"} %.6f\n", key, m.httpRequestSum[key])
	}

	// Business counters.
	out.WriteString("# HELP projects_created_total Total projects created\n")
	out.WriteString("# TYPE projects_created_total counter\n")
	fmt.Fprintf(&out, "projects_created_total %d\n", m.projectsCreated)

	out.WriteString("# HELP tasks_dispatched_total Total tasks dispatched\n")
	out.WriteString("# TYPE tasks_dispatched_total counter\n")
	fmt.Fprintf(&out, "tasks_dispatched_total %d\n", m.tasksDispatched)

	out.WriteString("# HELP agent_runs_started_total Total agent runs started\n")
	out.WriteString("# TYPE agent_runs_started_total counter\n")
	fmt.Fprintf(&out, "agent_runs_started_total %d\n", m.runsStarted)

	out.WriteString("# HELP agent_runs_completed_total Total agent runs completed\n")
	out.WriteString("# TYPE agent_runs_completed_total counter\n")
	fmt.Fprintf(&out, "agent_runs_completed_total %d\n", m.runsCompleted)

	out.WriteString("# HELP agent_runs_failed_total Total agent runs failed\n")
	out.WriteString("# TYPE agent_runs_failed_total counter\n")
	fmt.Fprintf(&out, "agent_runs_failed_total %d\n", m.runsFailed)

	out.WriteString("# HELP overrides_applied_total Total human overrides applied\n")
	out.WriteString("# TYPE overrides_applied_total counter\n")
	fmt.Fprintf(&out, "overrides_applied_total %d\n", m.overridesApplied)

	out.WriteString("# HELP conflicts_resolved_total Total conflicts resolved\n")
	out.WriteString("# TYPE conflicts_resolved_total counter\n")
	fmt.Fprintf(&out, "conflicts_resolved_total %d\n", m.conflictsResolved)

	// LLM metrics.
	out.WriteString("# HELP llm_requests_total Total LLM requests by provider\n")
	out.WriteString("# TYPE llm_requests_total counter\n")
	for provider, count := range m.llmRequestsTotal {
		fmt.Fprintf(&out, "llm_requests_total{provider=\"%s\"} %d\n", provider, count)
	}

	out.WriteString("# HELP llm_tokens_total Total LLM tokens by provider\n")
	out.WriteString("# TYPE llm_tokens_total counter\n")
	for provider, tokens := range m.llmTokensTotal {
		fmt.Fprintf(&out, "llm_tokens_total{provider=\"%s\"} %d\n", provider, tokens)
	}

	out.WriteString("# HELP llm_errors_total Total LLM errors by provider\n")
	out.WriteString("# TYPE llm_errors_total counter\n")
	for provider, count := range m.llmErrorsTotal {
		fmt.Fprintf(&out, "llm_errors_total{provider=\"%s\"} %d\n", provider, count)
	}

	out.WriteString("# HELP llm_duration_seconds_total Total LLM request duration by provider\n")
	out.WriteString("# TYPE llm_duration_seconds_total counter\n")
	for provider, duration := range m.llmDurationTotal {
		fmt.Fprintf(&out, "llm_duration_seconds_total{provider=\"%s\"} %.6f\n", provider, duration.Seconds())
	}

	return out.String()
}
