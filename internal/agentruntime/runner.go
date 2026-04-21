package agentruntime

import (
	"context"
	"errors"
	"time"
)

var (
	ErrProviderRequired      = errors.New("agentruntime: provider is required")
	ErrRunnerRequired        = errors.New("agentruntime: runner is required")
	ErrRunnerNotRegistered   = errors.New("agentruntime: runner not registered")
	ErrDefaultProviderNotSet = errors.New("agentruntime: default provider not set")
)

type Runner interface {
	Run(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	ProjectID string
	TaskID    string
	RunID     string
	AgentType string
	Prompt    string
	Context   string
	Timeout   time.Duration
}

type Response struct {
	Model            string
	Output           string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
