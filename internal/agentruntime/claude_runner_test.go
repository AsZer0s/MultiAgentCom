package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClaudeRunnerSuccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Fatalf("unexpected anthropic-version header: %s", got)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-test-key" {
			t.Fatalf("unexpected x-api-key header: %s", got)
		}
		if got := r.Header.Get("X-MultiAgentCom-Runtime-Protocol"); got != ProtocolVersion {
			t.Fatalf("unexpected protocol header: %s", got)
		}
		var payload anthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload.Model != "claude-sonnet-4-20250514" {
			t.Fatalf("unexpected model: %s", payload.Model)
		}
		if payload.Stream {
			t.Fatal("streaming should be disabled")
		}
		_ = json.NewEncoder(w).Encode(anthropicMessagesResponse{
			ID:   "msg_test",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "package main\n\nfunc main() {}"},
			},
			Model: "claude-sonnet-4-20250514",
			Usage: anthropicUsage{InputTokens: 25, OutputTokens: 10},
		})
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test-key",
		Model:   "claude-sonnet-4-20250514",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{
		ProjectID: "proj_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		AgentType: "go-backend-agent",
		Prompt:    "Build a Go API endpoint",
		Context:   "contract=v1",
	})
	if err != nil {
		t.Fatalf("run claude runner: %v", err)
	}
	if resp.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("unexpected model: %s", resp.Model)
	}
	if resp.Output == "" {
		t.Fatal("expected non-empty output")
	}
	if resp.PromptTokens != 25 || resp.CompletionTokens != 10 || resp.TotalTokens != 35 {
		t.Fatalf("unexpected usage: prompt=%d completion=%d total=%d", resp.PromptTokens, resp.CompletionTokens, resp.TotalTokens)
	}
}

func TestClaudeRunnerRequiresAPIKey(t *testing.T) {
	t.Parallel()
	_, err := NewClaudeRunner(ClaudeRunnerOptions{APIKey: ""})
	if err == nil {
		t.Fatal("expected error for empty api key")
	}
	if !errors.Is(err, ErrProviderRequired) {
		t.Fatalf("expected ErrProviderRequired, got: %v", err)
	}
}

func TestClaudeRunnerDefaultsModel(t *testing.T) {
	t.Parallel()
	runner, err := NewClaudeRunner(ClaudeRunnerOptions{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}
	if runner.model != "claude-sonnet-4-20250514" {
		t.Fatalf("expected default model, got: %s", runner.model)
	}
}

func TestClaudeRunnerError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(anthropicErrorResponse{
			Type: "error",
			Error: struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}{Type: "rate_limit_error", Message: "rate limit exceeded"},
		})
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{ProjectID: "proj_1"})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != "CLAUDE_RATE_LIMITED" {
		t.Fatalf("unexpected code: %s", providerErr.Code)
	}
	if !providerErr.Retryable {
		t.Fatal("rate limit error should be retryable")
	}
}

func TestClaudeRunnerAuthError(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(anthropicErrorResponse{
			Type: "error",
			Error: struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}{Type: "authentication_error", Message: "invalid api key"},
		})
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-bad-key",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != "CLAUDE_AUTH_ERROR" {
		t.Fatalf("unexpected code: %s", providerErr.Code)
	}
}

func TestClaudeRunnerMalformedResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ProviderErrorMalformedResponse {
		t.Fatalf("unexpected code: %s", providerErr.Code)
	}
}

func TestClaudeRunnerTimeout(t *testing.T) {
	t.Parallel()
	unblock := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	defer server.Close()
	defer close(unblock)

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{Timeout: time.Millisecond})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ProviderErrorTimeout || !providerErr.Retryable {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}

func TestClaudeRunnerOversizedResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxRuntimeResponseBytes+1)))
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ProviderErrorResponseTooLarge {
		t.Fatalf("unexpected code: %s", providerErr.Code)
	}
}

func TestClaudeRunnerPromptEngineering(t *testing.T) {
	t.Parallel()
	var capturedPayload anthropicMessagesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedPayload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		_ = json.NewEncoder(w).Encode(anthropicMessagesResponse{
			ID:   "msg_prompt",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "PRD generated"},
			},
			Model: "claude-sonnet-4-20250514",
			Usage: anthropicUsage{InputTokens: 100, OutputTokens: 50},
		})
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{
		ProjectID: "proj_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		AgentType: "manager-agent",
		Prompt:    "Generate a PRD for a todo app",
		Context:   "requirement=todo mvp",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(capturedPayload.System, "manager-agent") {
		t.Fatalf("system prompt should contain agent type, got: %s", capturedPayload.System)
	}
	if !strings.Contains(capturedPayload.System, "proj_1") {
		t.Fatalf("system prompt should contain project id, got: %s", capturedPayload.System)
	}
	if len(capturedPayload.Messages) != 1 || capturedPayload.Messages[0].Role != "user" {
		t.Fatalf("expected one user message, got: %+v", capturedPayload.Messages)
	}
	if !strings.Contains(capturedPayload.Messages[0].Content, "Generate a PRD") {
		t.Fatalf("user message should contain prompt, got: %s", capturedPayload.Messages[0].Content)
	}
	if !strings.Contains(capturedPayload.Messages[0].Content, "Context:") {
		t.Fatalf("user message should contain context section, got: %s", capturedPayload.Messages[0].Content)
	}
}

func TestClaudeRunnerEmptyPromptDefault(t *testing.T) {
	t.Parallel()
	var capturedPayload anthropicMessagesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedPayload)
		_ = json.NewEncoder(w).Encode(anthropicMessagesResponse{
			ID:   "msg_default",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "done"},
			},
			Model: "claude-sonnet-4-20250514",
			Usage: anthropicUsage{InputTokens: 10, OutputTokens: 5},
		})
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{
		ProjectID: "proj_2",
		TaskID:    "task_2",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(capturedPayload.Messages[0].Content, "Execute task task_2") {
		t.Fatalf("expected default prompt, got: %s", capturedPayload.Messages[0].Content)
	}
}

func TestClaudeRunnerMultiContentBlock(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(anthropicMessagesResponse{
			ID:   "msg_multi",
			Type: "message",
			Role: "assistant",
			Content: []anthropicContentBlock{
				{Type: "text", Text: "Part 1. "},
				{Type: "text", Text: "Part 2."},
			},
			Model: "claude-sonnet-4-20250514",
			Usage: anthropicUsage{InputTokens: 20, OutputTokens: 15},
		})
	}))
	defer server.Close()

	runner, err := NewClaudeRunner(ClaudeRunnerOptions{
		APIKey:  "sk-test",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("new claude runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{ProjectID: "proj_1"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !strings.Contains(resp.Output, "Part 1") || !strings.Contains(resp.Output, "Part 2") {
		t.Fatalf("expected combined output, got: %s", resp.Output)
	}
}
