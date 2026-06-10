package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAIRunnerSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(openAIChatResponse{
			ID:    "chatcmpl-123",
			Model: "gpt-4o",
			Choices: []openAIChoice{
				{Message: openAIMessageEntry{Role: "assistant", Content: "Hello world"}},
			},
			Usage: openAIUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		})
	}))
	defer server.Close()

	runner, err := NewOpenAIRunner(OpenAIRunnerOptions{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "gpt-4o",
		Format:  "chat",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := runner.Run(context.Background(), Request{
		ProjectID: "proj-1",
		TaskID:    "task-1",
		Prompt:    "Say hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Output != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", resp.Output)
	}
	if resp.PromptTokens != 10 {
		t.Errorf("expected 10 prompt tokens, got %d", resp.PromptTokens)
	}
	if resp.TotalTokens != 15 {
		t.Errorf("expected 15 total tokens, got %d", resp.TotalTokens)
	}
}

func TestOpenAIRunnerCompletionsFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var req openAICompletionsRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Prompt == "" {
			t.Error("expected non-empty prompt")
		}
		json.NewEncoder(w).Encode(openAICompletionsResponse{
			ID:    "cmpl-123",
			Model: "code-davinci-002",
			Choices: []openAICompletionsChoice{
				{Text: "func main() {}", FinishReason: "stop"},
			},
			Usage: openAIUsage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
		})
	}))
	defer server.Close()

	runner, err := NewOpenAIRunner(OpenAIRunnerOptions{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "code-davinci-002",
		Format:  "completions",
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := runner.Run(context.Background(), Request{
		ProjectID: "proj-1",
		TaskID:    "task-1",
		Prompt:    "Write a Go main function",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Output != "func main() {}" {
		t.Errorf("unexpected output: %q", resp.Output)
	}
}

func TestOpenAIRunnerRequiresAPIKey(t *testing.T) {
	_, err := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: ""})
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestOpenAIRunnerDefaultsModel(t *testing.T) {
	runner, err := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.model != "gpt-4o" {
		t.Errorf("expected default model 'gpt-4o', got %q", runner.model)
	}
}

func TestOpenAIRunnerInvalidFormat(t *testing.T) {
	_, err := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: "key", Format: "invalid"})
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestOpenAIRunnerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(openAIErrorResponse{
			Error: struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			}{Message: "rate limit exceeded", Type: "rate_limit_error"},
		})
	}))
	defer server.Close()

	runner, _ := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: "key", BaseURL: server.URL})
	_, err := runner.Run(context.Background(), Request{ProjectID: "p", TaskID: "t", Prompt: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("expected ProviderError, got %T", err)
	}
	if !pe.Retryable {
		t.Error("expected retryable error")
	}
}

func TestOpenAIRunnerTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	runner, _ := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: "key", BaseURL: server.URL})
	_, err := runner.Run(context.Background(), Request{
		ProjectID: "p", TaskID: "t", Prompt: "test", Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestOpenAIRunnerMultiContentBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIChatResponse{
			Choices: []openAIChoice{
				{Message: openAIMessageEntry{Content: "Part 1"}},
				{Message: openAIMessageEntry{Content: "Part 2"}},
			},
			Usage: openAIUsage{TotalTokens: 10},
		})
	}))
	defer server.Close()

	runner, _ := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: "key", BaseURL: server.URL})
	resp, err := runner.Run(context.Background(), Request{ProjectID: "p", TaskID: "t", Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Output != "Part 1\nPart 2" {
		t.Errorf("unexpected output: %q", resp.Output)
	}
}

func BenchmarkOpenAIRunner(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIChatResponse{
			Choices: []openAIChoice{{Message: openAIMessageEntry{Content: "ok"}}},
			Usage:   openAIUsage{TotalTokens: 5},
		})
	}))
	defer server.Close()

	runner, _ := NewOpenAIRunner(OpenAIRunnerOptions{APIKey: "key", BaseURL: server.URL})
	ctx := context.Background()
	req := Request{ProjectID: "p", TaskID: "t", Prompt: "benchmark"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner.Run(ctx, req)
	}
}
