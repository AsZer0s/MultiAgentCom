package agentruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRunnerRunSuccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["projectId"] != "proj_1" {
			t.Fatalf("unexpected projectId: %v", payload["projectId"])
		}
		_ = json.NewEncoder(w).Encode(Response{
			Model:            "runtime-http-v1",
			Output:           "http runtime result",
			PromptTokens:     9,
			CompletionTokens: 7,
			TotalTokens:      16,
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
	if resp.TotalTokens != 16 {
		t.Fatalf("unexpected total tokens: %d", resp.TotalTokens)
	}
}

func TestHTTPRunnerRunFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(Response{Output: "upstream unavailable"})
	}))
	defer server.Close()

	runner, err := NewHTTPRunner(server.URL, server.Client())
	if err != nil {
		t.Fatalf("new http runner: %v", err)
	}

	if _, err := runner.Run(context.Background(), Request{}); err == nil {
		t.Fatal("expected error when runtime returns non-success status")
	}
}
