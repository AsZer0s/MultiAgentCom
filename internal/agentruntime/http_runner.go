package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type HTTPRunner struct {
	endpoint string
	client   *http.Client
}

func NewHTTPRunner(endpoint string, client *http.Client) (*HTTPRunner, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("%w: endpoint", ErrProviderRequired)
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	return &HTTPRunner{
		endpoint: endpoint,
		client:   client,
	}, nil
}

func (r *HTTPRunner) Run(ctx context.Context, req Request) (Response, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	payload := struct {
		ProjectID string `json:"projectId"`
		TaskID    string `json:"taskId"`
		RunID     string `json:"runId"`
		AgentType string `json:"agentType"`
		Prompt    string `json:"prompt"`
		Context   string `json:"context"`
		TimeoutMs int64  `json:"timeoutMs,omitempty"`
	}{
		ProjectID: req.ProjectID,
		TaskID:    req.TaskID,
		RunID:     req.RunID,
		AgentType: req.AgentType,
		Prompt:    req.Prompt,
		Context:   req.Context,
	}
	if req.Timeout > 0 {
		payload.TimeoutMs = req.Timeout.Milliseconds()
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("marshal runtime request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(execCtx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("create runtime request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, fmt.Errorf("runtime request failed: %w", err)
	}
	defer resp.Body.Close()

	var decoded Response
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return Response{}, fmt.Errorf("decode runtime response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(decoded.Output)
		if message == "" {
			message = "runtime request returned non-success status"
		}
		return Response{}, fmt.Errorf("runtime request failed: status=%d message=%s", resp.StatusCode, message)
	}

	if decoded.TotalTokens <= 0 {
		decoded.TotalTokens = decoded.PromptTokens + decoded.CompletionTokens
	}

	return decoded, nil
}
