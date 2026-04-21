package agentruntime

import (
	"context"
	"fmt"
	"strings"
)

type MockRunner struct {
	responses map[string]string
}

func NewMockRunner() *MockRunner {
	return &MockRunner{
		responses: map[string]string{
			"manager-agent":      "mock manager summary",
			"go-backend-agent":   "mock backend summary",
			"vue-frontend-agent": "mock frontend summary",
			"integration-agent":  "mock integration summary",
		},
	}
}

func (m *MockRunner) Run(ctx context.Context, req Request) (Response, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	select {
	case <-execCtx.Done():
		return Response{}, execCtx.Err()
	default:
	}

	agentType := strings.TrimSpace(req.AgentType)
	if agentType == "" {
		agentType = "generic-agent"
	}

	output := fmt.Sprintf(
		"%s | project=%s task=%s run=%s",
		m.summaryForAgent(agentType),
		req.ProjectID,
		req.TaskID,
		req.RunID,
	)
	if prompt := strings.TrimSpace(req.Prompt); prompt != "" {
		output += " | prompt=" + prompt
	}
	if contextText := strings.TrimSpace(req.Context); contextText != "" {
		output += " | context=" + contextText
	}

	promptTokens := estimateTokens(req.Prompt) + estimateTokens(req.Context)
	completionTokens := estimateTokens(output)

	return Response{
		Model:            "mock-" + agentType,
		Output:           output,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}, nil
}

func (m *MockRunner) summaryForAgent(agentType string) string {
	if summary, ok := m.responses[agentType]; ok {
		return summary
	}
	return "mock generic summary"
}

func estimateTokens(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	return len(strings.Fields(trimmed))
}
