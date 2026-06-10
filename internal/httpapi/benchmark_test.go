package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"multiagentcom/internal/config"
	"multiagentcom/internal/service"
)

func newBenchmarkServer(b *testing.B) *httptest.Server {
	b.Helper()
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "bench",
		ArtifactRoot: b.TempDir(),
		SandboxRoot:  b.TempDir(),
		DefaultAgent: "bench-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	return httptest.NewServer(NewServer(cfg, logger, svc))
}

func BenchmarkHealthEndpoint(b *testing.B) {
	server := newBenchmarkServer(b)
	defer server.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(server.URL + "/health")
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkCreateProject(b *testing.B) {
	server := newBenchmarkServer(b)
	defer server.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body := `{"name":"bench-project"}`
		resp, err := http.Post(server.URL+"/projects", "application/json", strings.NewReader(body))
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkListProjects(b *testing.B) {
	server := newBenchmarkServer(b)
	defer server.Close()
	// Seed data.
	for i := 0; i < 10; i++ {
		body := `{"name":"project"}`
		resp, _ := http.Post(server.URL+"/projects", "application/json", strings.NewReader(body))
		resp.Body.Close()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(server.URL + "/projects")
		if err != nil {
			b.Fatal(err)
		}
		json.NewDecoder(resp.Body).Decode(&[]any{})
		resp.Body.Close()
	}
}

func BenchmarkStatusMatrix(b *testing.B) {
	server := newBenchmarkServer(b)
	defer server.Close()
	// Seed data.
	for i := 0; i < 5; i++ {
		body := `{"name":"project"}`
		resp, _ := http.Post(server.URL+"/projects", "application/json", strings.NewReader(body))
		resp.Body.Close()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(server.URL + "/status/matrix")
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkMetricsEndpoint(b *testing.B) {
	server := newBenchmarkServer(b)
	defer server.Close()
	// Seed some traffic.
	for i := 0; i < 100; i++ {
		resp, _ := http.Get(server.URL + "/health")
		resp.Body.Close()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := http.Get(server.URL + "/metrics")
		if err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkConcurrentHealthChecks(b *testing.B) {
	server := newBenchmarkServer(b)
	defer server.Close()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			resp, err := http.Get(server.URL + "/health")
			if err != nil {
				b.Fatal(err)
			}
			resp.Body.Close()
		}
	})
}
