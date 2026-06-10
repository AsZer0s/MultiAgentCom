package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeminiRunnerSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("expected x-goog-api-key header, got %s", r.Header.Get("x-goog-api-key"))
		}
		json.NewEncoder(w).Encode(geminiGenerateResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: "Hello from Gemini"}}}},
			},
			UsageMetadata: geminiUsageMetadata{
				PromptTokenCount:     8,
				CandidatesTokenCount: 4,
				TotalTokenCount:      12,
			},
		})
	}))
	defer server.Close()

	runner, err := NewGeminiRunner(GeminiRunnerOptions{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Model:   "gemini-2.0-flash",
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
	if resp.Output != "Hello from Gemini" {
		t.Errorf("expected 'Hello from Gemini', got %q", resp.Output)
	}
	if resp.PromptTokens != 8 {
		t.Errorf("expected 8 prompt tokens, got %d", resp.PromptTokens)
	}
}

func TestGeminiRunnerRequiresAPIKey(t *testing.T) {
	_, err := NewGeminiRunner(GeminiRunnerOptions{APIKey: ""})
	if err == nil {
		t.Error("expected error for empty API key")
	}
}

func TestGeminiRunnerDefaultsModel(t *testing.T) {
	runner, err := NewGeminiRunner(GeminiRunnerOptions{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if runner.model != "gemini-2.0-flash" {
		t.Errorf("expected default model 'gemini-2.0-flash', got %q", runner.model)
	}
}

func TestGeminiRunnerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(geminiErrorResponse{
			Error: struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			}{Code: 429, Message: "quota exceeded", Status: "RESOURCE_EXHAUSTED"},
		})
	}))
	defer server.Close()

	runner, _ := NewGeminiRunner(GeminiRunnerOptions{APIKey: "key", BaseURL: server.URL})
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

func TestGeminiRunnerAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(geminiErrorResponse{
			Error: struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			}{Code: 401, Message: "invalid key", Status: "UNAUTHENTICATED"},
		})
	}))
	defer server.Close()

	runner, _ := NewGeminiRunner(GeminiRunnerOptions{APIKey: "key", BaseURL: server.URL})
	_, err := runner.Run(context.Background(), Request{ProjectID: "p", TaskID: "t", Prompt: "test"})
	if err == nil {
		t.Fatal("expected error")
	}
	pe := err.(*ProviderError)
	if pe.Code != "GEMINI_AUTH_ERROR" {
		t.Errorf("expected GEMINI_AUTH_ERROR, got %s", pe.Code)
	}
}

func TestGeminiRunnerTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer server.Close()

	runner, _ := NewGeminiRunner(GeminiRunnerOptions{APIKey: "key", BaseURL: server.URL})
	_, err := runner.Run(context.Background(), Request{
		ProjectID: "p", TaskID: "t", Prompt: "test", Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestGeminiRunnerMultiCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(geminiGenerateResponse{
			Candidates: []geminiCandidate{
				{Content: geminiContent{Parts: []geminiPart{{Text: "Part A"}, {Text: "Part B"}}}},
			},
			UsageMetadata: geminiUsageMetadata{TotalTokenCount: 10},
		})
	}))
	defer server.Close()

	runner, _ := NewGeminiRunner(GeminiRunnerOptions{APIKey: "key", BaseURL: server.URL})
	resp, err := runner.Run(context.Background(), Request{ProjectID: "p", TaskID: "t", Prompt: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Output != "Part A\nPart B" {
		t.Errorf("unexpected output: %q", resp.Output)
	}
}

func BenchmarkGeminiRunner(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(geminiGenerateResponse{
			Candidates:   []geminiCandidate{{Content: geminiContent{Parts: []geminiPart{{Text: "ok"}}}}},
			UsageMetadata: geminiUsageMetadata{TotalTokenCount: 5},
		})
	}))
	defer server.Close()

	runner, _ := NewGeminiRunner(GeminiRunnerOptions{APIKey: "key", BaseURL: server.URL})
	ctx := context.Background()
	req := Request{ProjectID: "p", TaskID: "t", Prompt: "benchmark"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runner.Run(ctx, req)
	}
}
