package agentruntime

import (
	"context"
	"testing"
)

func TestMockRunnerRunReturnsPredictableOutputAndTokens(t *testing.T) {
	runner := NewMockRunner()

	resp, err := runner.Run(context.Background(), Request{
		ProjectID: "proj_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		AgentType: "go-backend-agent",
		Prompt:    "implement CRUD todo API",
		Context:   "contract=v1",
	})
	if err != nil {
		t.Fatalf("run mock runner: %v", err)
	}

	expectedOutput := "mock backend summary | project=proj_1 task=task_1 run=run_1 | prompt=implement CRUD todo API | context=contract=v1"
	if resp.Output != expectedOutput {
		t.Fatalf("expected output %q, got %q", expectedOutput, resp.Output)
	}
	if resp.Model != "mock-go-backend-agent" {
		t.Fatalf("expected model mock-go-backend-agent, got %s", resp.Model)
	}
	if resp.PromptTokens != 5 {
		t.Fatalf("expected prompt tokens 5, got %d", resp.PromptTokens)
	}
	if resp.CompletionTokens != 14 {
		t.Fatalf("expected completion tokens 14, got %d", resp.CompletionTokens)
	}
	if resp.TotalTokens != 19 {
		t.Fatalf("expected total tokens 19, got %d", resp.TotalTokens)
	}
}

func TestMockRunnerRunUsesGenericSummaryForUnknownAgent(t *testing.T) {
	runner := NewMockRunner()

	resp, err := runner.Run(context.Background(), Request{
		ProjectID: "proj_2",
		TaskID:    "task_2",
		RunID:     "run_2",
		AgentType: "unknown-agent",
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("run mock runner: %v", err)
	}

	expectedOutput := "mock generic summary | project=proj_2 task=task_2 run=run_2 | prompt=hello"
	if resp.Output != expectedOutput {
		t.Fatalf("expected output %q, got %q", expectedOutput, resp.Output)
	}
	if resp.Model != "mock-unknown-agent" {
		t.Fatalf("expected model mock-unknown-agent, got %s", resp.Model)
	}
	if resp.TotalTokens != resp.PromptTokens+resp.CompletionTokens {
		t.Fatalf("expected total tokens to equal prompt+completion, got %d", resp.TotalTokens)
	}
}
