package httpapi

import (
	"strings"
	"testing"
	"time"
)

func TestMetricsRecordHTTPRequest(t *testing.T) {
	m := NewMetricsCollector()

	m.RecordHTTPRequest("GET", "/health", 200, 10*time.Millisecond)
	m.RecordHTTPRequest("GET", "/health", 200, 20*time.Millisecond)
	m.RecordHTTPRequest("POST", "/projects", 201, 50*time.Millisecond)

	output := m.RenderPrometheus()

	if !strings.Contains(output, "http_requests_total{request=\"GET /health 200\"} 2") {
		t.Error("expected 2 GET /health requests")
	}
	if !strings.Contains(output, "http_requests_total{request=\"POST /projects 201\"} 1") {
		t.Error("expected 1 POST /projects request")
	}
	if !strings.Contains(output, "http_request_duration_seconds_bucket") {
		t.Error("expected histogram buckets")
	}
	if !strings.Contains(output, "http_request_duration_seconds_count") {
		t.Error("expected histogram count")
	}
	if !strings.Contains(output, "http_request_duration_seconds_sum") {
		t.Error("expected histogram sum")
	}
}

func TestMetricsHistogramBuckets(t *testing.T) {
	m := NewMetricsCollector()

	// 5ms should fall in 0.005 bucket
	m.RecordHTTPRequest("GET", "/test", 200, 3*time.Millisecond)
	// 150ms should fall in 0.25 bucket
	m.RecordHTTPRequest("GET", "/test", 200, 150*time.Millisecond)

	output := m.RenderPrometheus()

	if !strings.Contains(output, "le=\"0.005\"} 1") {
		t.Error("expected 1 request in 5ms bucket")
	}
	if !strings.Contains(output, "le=\"0.250\"} 2") {
		t.Error("expected 2 requests in 250ms bucket")
	}
}

func TestMetricsBusinessCounters(t *testing.T) {
	m := NewMetricsCollector()

	m.IncProjectsCreated()
	m.IncProjectsCreated()
	m.IncTasksDispatched()
	m.IncRunsStarted()
	m.IncRunsCompleted()
	m.IncRunsFailed()
	m.IncOverridesApplied()
	m.IncConflictsResolved()

	output := m.RenderPrometheus()

	checks := []struct {
		name  string
		count string
	}{
		{"projects_created_total", "2"},
		{"tasks_dispatched_total", "1"},
		{"agent_runs_started_total", "1"},
		{"agent_runs_completed_total", "1"},
		{"agent_runs_failed_total", "1"},
		{"overrides_applied_total", "1"},
		{"conflicts_resolved_total", "1"},
	}
	for _, c := range checks {
		if !strings.Contains(output, c.name+" "+c.count) {
			t.Errorf("expected %s %s", c.name, c.count)
		}
	}
}

func TestMetricsLLMRequests(t *testing.T) {
	m := NewMetricsCollector()

	m.RecordLLMRequest("claude", 100, 500*time.Millisecond, false)
	m.RecordLLMRequest("claude", 200, 1*time.Second, false)
	m.RecordLLMRequest("openai", 50, 200*time.Millisecond, true)

	output := m.RenderPrometheus()

	if !strings.Contains(output, "llm_requests_total{provider=\"claude\"} 2") {
		t.Error("expected 2 claude requests")
	}
	if !strings.Contains(output, "llm_tokens_total{provider=\"claude\"} 300") {
		t.Error("expected 300 claude tokens")
	}
	if !strings.Contains(output, "llm_errors_total{provider=\"openai\"} 1") {
		t.Error("expected 1 openai error")
	}
	if !strings.Contains(output, "llm_duration_seconds_total{provider=\"claude\"}") {
		t.Error("expected claude duration")
	}
}

func TestMetricsConcurrency(t *testing.T) {
	m := NewMetricsCollector()

	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			m.RecordHTTPRequest("GET", "/test", 200, time.Millisecond)
			m.IncProjectsCreated()
			m.RecordLLMRequest("claude", 10, time.Millisecond, false)
			done <- true
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}

	output := m.RenderPrometheus()
	if !strings.Contains(output, "http_requests_total{request=\"GET /test 200\"} 100") {
		t.Error("expected 100 requests after concurrent writes")
	}
}

func BenchmarkMetricsRecordHTTPRequest(b *testing.B) {
	m := NewMetricsCollector()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RecordHTTPRequest("GET", "/health", 200, time.Millisecond)
	}
}

func BenchmarkMetricsRenderPrometheus(b *testing.B) {
	m := NewMetricsCollector()
	// Seed with data
	for i := 0; i < 100; i++ {
		m.RecordHTTPRequest("GET", "/health", 200, time.Millisecond)
		m.RecordHTTPRequest("POST", "/projects", 201, 50*time.Millisecond)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.RenderPrometheus()
	}
}
