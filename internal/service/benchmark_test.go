package service

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"multiagentcom/internal/config"
)

func newBenchmarkService(b *testing.B) *Service {
	b.Helper()
	cfg := config.Config{
		ArtifactRoot: b.TempDir(),
		SandboxRoot:  b.TempDir(),
		DefaultAgent: "benchmark-agent",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	return New(cfg, logger)
}

func BenchmarkCreateProject(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.CreateProject(ctx, CreateProjectInput{Name: "benchmark-project"})
	}
}

func BenchmarkAddRequirement(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	proj, _ := svc.CreateProject(ctx, CreateProjectInput{Name: "bench"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.AddRequirement(ctx, proj.ID, AddRequirementInput{
			Title:   "Requirement",
			Content: "Some content for benchmarking",
		})
	}
}

func BenchmarkGeneratePlan(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	proj, _ := svc.CreateProject(ctx, CreateProjectInput{Name: "bench"})
	svc.AddRequirement(ctx, proj.ID, AddRequirementInput{Title: "R1", Content: "C1"})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.GeneratePlan(ctx, proj.ID)
	}
}

func BenchmarkDispatchTasks(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	proj, _ := svc.CreateProject(ctx, CreateProjectInput{Name: "bench"})
	svc.AddRequirement(ctx, proj.ID, AddRequirementInput{Title: "R1", Content: "C1"})
	svc.GeneratePlan(ctx, proj.ID)
	svc.GenerateContract(ctx, proj.ID)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.DispatchTasks(ctx, proj.ID)
	}
}

func BenchmarkListProjects(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		svc.CreateProject(ctx, CreateProjectInput{Name: "project"})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.ListProjects(ctx)
	}
}

func BenchmarkListTasks(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	proj, _ := svc.CreateProject(ctx, CreateProjectInput{Name: "bench"})
	svc.AddRequirement(ctx, proj.ID, AddRequirementInput{Title: "R1", Content: "C1"})
	svc.GeneratePlan(ctx, proj.ID)
	svc.GenerateContract(ctx, proj.ID)
	svc.DispatchTasks(ctx, proj.ID)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.ListTasks(ctx, proj.ID)
	}
}

func BenchmarkGetStatusMatrix(b *testing.B) {
	svc := newBenchmarkService(b)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		proj, _ := svc.CreateProject(ctx, CreateProjectInput{Name: "project"})
		svc.AddRequirement(ctx, proj.ID, AddRequirementInput{Title: "R1", Content: "C1"})
		svc.GeneratePlan(ctx, proj.ID)
		svc.GenerateContract(ctx, proj.ID)
		svc.DispatchTasks(ctx, proj.ID)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.GetStatusMatrix(ctx, "")
	}
}
