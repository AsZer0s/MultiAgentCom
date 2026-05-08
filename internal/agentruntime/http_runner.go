package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxRuntimeResponseBytes = 1 << 20

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
		ProtocolVersion string `json:"protocolVersion"`
		ProjectID       string `json:"projectId"`
		TaskID          string `json:"taskId"`
		RunID           string `json:"runId"`
		AgentType       string `json:"agentType"`
		Prompt          string `json:"prompt"`
		Context         string `json:"context"`
		TimeoutMs       int64  `json:"timeoutMs,omitempty"`
	}{
		ProtocolVersion: ProtocolVersion,
		ProjectID:       req.ProjectID,
		TaskID:          req.TaskID,
		RunID:           req.RunID,
		AgentType:       req.AgentType,
		Prompt:          req.Prompt,
		Context:         req.Context,
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
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-MultiAgentCom-Runtime-Protocol", ProtocolVersion)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	defer resp.Body.Close()

	payloadBytes, err := readRuntimeResponse(resp.Body)
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return Response{}, decodeRuntimeError(resp.StatusCode, payloadBytes)
	}

	decoded, err := decodeRuntimeSuccess(payloadBytes)
	if err != nil {
		return Response{}, err
	}
	return normalizeUsage(decoded), nil
}

func readRuntimeResponse(body io.Reader) ([]byte, error) {
	limited := io.LimitReader(body, maxRuntimeResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, &ProviderError{Code: ProviderErrorMalformedResponse, Message: "read runtime response", Cause: err}
	}
	if len(payload) > maxRuntimeResponseBytes {
		return nil, &ProviderError{Code: ProviderErrorResponseTooLarge, Message: "runtime response exceeded 1048576 bytes"}
	}
	return payload, nil
}

func decodeRuntimeSuccess(payload []byte) (Response, error) {
	var envelope struct {
		ProtocolVersion  string `json:"protocolVersion"`
		Model            string `json:"model"`
		Output           string `json:"output"`
		Usage            Usage  `json:"usage"`
		PromptTokens     int    `json:"promptTokens"`
		CompletionTokens int    `json:"completionTokens"`
		TotalTokens      int    `json:"totalTokens"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return Response{}, &ProviderError{Code: ProviderErrorMalformedResponse, Message: "decode runtime response", Cause: err}
	}
	if envelope.ProtocolVersion != "" && envelope.ProtocolVersion != ProtocolVersion {
		return Response{}, &ProviderError{Code: ProviderErrorProtocolVersionUnsupported, Message: "unsupported runtime protocol version " + envelope.ProtocolVersion}
	}
	response := Response{Model: envelope.Model, Output: envelope.Output}
	if envelope.Usage != (Usage{}) {
		response.PromptTokens = envelope.Usage.PromptTokens
		response.CompletionTokens = envelope.Usage.CompletionTokens
		response.TotalTokens = envelope.Usage.TotalTokens
	} else {
		response.PromptTokens = envelope.PromptTokens
		response.CompletionTokens = envelope.CompletionTokens
		response.TotalTokens = envelope.TotalTokens
	}
	return response, nil
}

func decodeRuntimeError(statusCode int, payload []byte) error {
	var envelope struct {
		ProtocolVersion string              `json:"protocolVersion"`
		Error           ProviderErrorDetail `json:"error"`
		Output          string              `json:"output"`
	}
	if err := json.Unmarshal(payload, &envelope); err == nil {
		if envelope.ProtocolVersion != "" && envelope.ProtocolVersion != ProtocolVersion {
			return &ProviderError{Code: ProviderErrorProtocolVersionUnsupported, Message: "unsupported runtime protocol version " + envelope.ProtocolVersion, StatusCode: statusCode, Retryable: retryableStatus(statusCode)}
		}
		if strings.TrimSpace(envelope.Error.Code) != "" || strings.TrimSpace(envelope.Error.Message) != "" {
			code := strings.TrimSpace(envelope.Error.Code)
			if code == "" {
				code = ProviderErrorHTTPStatus
			}
			message := strings.TrimSpace(envelope.Error.Message)
			if message == "" {
				message = "runtime provider returned non-success status"
			}
			providerStatus := envelope.Error.ProviderStatus
			if providerStatus == 0 {
				providerStatus = statusCode
			}
			return &ProviderError{Code: code, Message: message, Retryable: envelope.Error.Retryable || retryableStatus(statusCode), StatusCode: statusCode, ProviderStatus: providerStatus, RequestID: envelope.Error.RequestID}
		}
		if message := strings.TrimSpace(envelope.Output); message != "" {
			return &ProviderError{Code: ProviderErrorHTTPStatus, Message: message, Retryable: retryableStatus(statusCode), StatusCode: statusCode, ProviderStatus: statusCode}
		}
	}
	message := strings.TrimSpace(string(payload))
	if message == "" {
		message = "runtime provider returned non-success status"
	}
	return &ProviderError{Code: ProviderErrorHTTPStatus, Message: message, Retryable: retryableStatus(statusCode), StatusCode: statusCode, ProviderStatus: statusCode}
}

func transportProviderError(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ProviderError{Code: ProviderErrorTimeout, Message: "runtime provider request timed out", Retryable: true, Cause: err}
	}
	return &ProviderError{Code: ProviderErrorNetwork, Message: "runtime provider request failed", Retryable: true, Cause: err}
}

func retryableStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func normalizeUsage(response Response) Response {
	if response.PromptTokens < 0 {
		response.PromptTokens = 0
	}
	if response.CompletionTokens < 0 {
		response.CompletionTokens = 0
	}
	if response.TotalTokens < 0 {
		response.TotalTokens = 0
	}
	if response.TotalTokens <= 0 {
		response.TotalTokens = response.PromptTokens + response.CompletionTokens
	}
	if response.TotalTokens > 0 && response.PromptTokens == 0 && response.CompletionTokens == 0 {
		response.PromptTokens = response.TotalTokens
	}
	return response
}
