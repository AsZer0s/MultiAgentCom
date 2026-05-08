package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPRunnerRunProtocolSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("X-MultiAgentCom-Runtime-Protocol"); got != ProtocolVersion {
			t.Fatalf("unexpected protocol header: %s", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("unexpected accept header: %s", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["protocolVersion"] != ProtocolVersion {
			t.Fatalf("unexpected protocolVersion: %v", payload["protocolVersion"])
		}
		if payload["projectId"] != "proj_1" {
			t.Fatalf("unexpected projectId: %v", payload["projectId"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"model":           "runtime-http-v1",
			"output":          "http runtime result",
			"usage": map[string]any{
				"promptTokens":     9,
				"completionTokens": 7,
				"totalTokens":      16,
			},
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{
		ProjectID: "proj_1",
		TaskID:    "task_1",
		RunID:     "run_1",
		AgentType: "go-backend-agent",
		Prompt:    "build api",
		Context:   "contract=v1",
		Timeout:   500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run http runner: %v", err)
	}
	if resp.Model != "runtime-http-v1" {
		t.Fatalf("unexpected model: %s", resp.Model)
	}
	if resp.PromptTokens != 9 || resp.CompletionTokens != 7 || resp.TotalTokens != 16 {
		t.Fatalf("unexpected usage: %+v", resp)
	}
}

func TestHTTPRunnerRunSendsBearerToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer runtime-secret" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"model":           "runtime-http-v1",
			"output":          "authorized",
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunnerWithOptions(server.URL, server.Client(), HTTPRunnerOptions{BearerToken: " runtime-secret "})
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	if _, err := runner.Run(context.Background(), Request{}); err != nil {
		t.Fatalf("run http runner: %v", err)
	}
}

func TestHTTPRunnerRunRetriesRetryableProviderError(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := atomic.AddInt32(&attempts, 1)
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"protocolVersion": ProtocolVersion,
				"error": map[string]any{
					"code":      "UPSTREAM_UNAVAILABLE",
					"message":   "temporary outage",
					"retryable": true,
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"model":           "runtime-http-v1",
			"output":          "retried",
			"usage": map[string]any{
				"promptTokens":     3,
				"completionTokens": 4,
				"totalTokens":      7,
			},
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunnerWithOptions(server.URL, server.Client(), HTTPRunnerOptions{MaxAttempts: 2, RetryBaseDelay: time.Millisecond})
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{Timeout: time.Second})
	if err != nil {
		t.Fatalf("run http runner: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if resp.Output != "retried" || resp.TotalTokens != 7 {
		t.Fatalf("unexpected retried response: %+v", resp)
	}
}

func TestHTTPRunnerRunDoesNotRetryNonRetryableProviderError(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"error": map[string]any{
				"code":    "BAD_REQUEST",
				"message": "bad request",
			},
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunnerWithOptions(server.URL, server.Client(), HTTPRunnerOptions{MaxAttempts: 3, RetryBaseDelay: time.Millisecond})
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestHTTPRunnerRunStopsRetryWhenContextExpires(t *testing.T) {
	t.Parallel()

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"error": map[string]any{
				"code":      "UPSTREAM_UNAVAILABLE",
				"message":   "temporary outage",
				"retryable": true,
			},
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunnerWithOptions(server.URL, server.Client(), HTTPRunnerOptions{MaxAttempts: 3, RetryBaseDelay: 500 * time.Millisecond})
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{Timeout: 100 * time.Millisecond})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ProviderErrorTimeout {
		t.Fatalf("expected timeout provider error, got %+v", providerErr)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestNewHTTPRunnerWithOptionsRejectsInvalidRetryOptions(t *testing.T) {
	t.Parallel()

	if _, err := NewHTTPRunnerWithOptions("http://runtime.example", nil, HTTPRunnerOptions{MaxAttempts: -1}); err == nil {
		t.Fatal("expected max attempts error")
	}
	if _, err := NewHTTPRunnerWithOptions("http://runtime.example", nil, HTTPRunnerOptions{RetryBaseDelay: -time.Millisecond}); err == nil {
		t.Fatal("expected retry base delay error")
	}
}

func TestHTTPRunnerRunLegacySuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Response{
			Model:            "legacy-runtime",
			Output:           "legacy result",
			PromptTokens:     11,
			CompletionTokens: 13,
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{})
	if err != nil {
		t.Fatalf("run http runner: %v", err)
	}
	if resp.Model != "legacy-runtime" || resp.Output != "legacy result" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.TotalTokens != 24 {
		t.Fatalf("expected total tokens to be derived from legacy usage, got %d", resp.TotalTokens)
	}
}

func TestHTTPRunnerRunTotalOnlyUsage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"model":           "runtime-http-v1",
			"output":          "total only",
			"usage": map[string]any{
				"totalTokens": 42,
			},
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{})
	if err != nil {
		t.Fatalf("run http runner: %v", err)
	}
	if resp.PromptTokens != 42 || resp.CompletionTokens != 0 || resp.TotalTokens != 42 {
		t.Fatalf("unexpected normalized usage: %+v", resp)
	}
}

func TestHTTPRunnerRunStructuredProviderError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": ProtocolVersion,
			"error": map[string]any{
				"code":           "UPSTREAM_UNAVAILABLE",
				"message":        "runtime provider dependency unavailable",
				"retryable":      true,
				"providerStatus": http.StatusServiceUnavailable,
				"requestId":      "req_123",
			},
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != "UPSTREAM_UNAVAILABLE" || providerErr.Message != "runtime provider dependency unavailable" {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
	if !providerErr.Retryable || providerErr.StatusCode != http.StatusBadGateway || providerErr.ProviderStatus != http.StatusServiceUnavailable || providerErr.RequestID != "req_123" {
		t.Fatalf("unexpected provider error metadata: %+v", providerErr)
	}
}

func TestHTTPRunnerRunMalformedErrorBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ProviderErrorHTTPStatus || providerErr.Message != "upstream exploded" || providerErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}

func TestHTTPRunnerRunMalformedSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
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
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}

func TestHTTPRunnerRunTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
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

func TestHTTPRunnerRunOversizedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxRuntimeResponseBytes+1)))
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
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
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}

func TestHTTPRunnerRunUnsupportedProtocolVersion(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"protocolVersion": "runtime.http.v999",
			"model":           "future-runtime",
			"output":          "future response",
		})
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %T: %v", err, err)
	}
	if providerErr.Code != ProviderErrorProtocolVersionUnsupported {
		t.Fatalf("unexpected provider error: %+v", providerErr)
	}
}
