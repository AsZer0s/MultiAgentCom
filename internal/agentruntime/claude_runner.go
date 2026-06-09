package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClaudeRunner implements the Runner interface by calling the Anthropic Messages API directly.
// It conforms to the same runtime.http.v1 protocol envelope so the service layer can use
// it as a drop-in replacement alongside HTTPRunner and ContainerRunner.
type ClaudeRunner struct {
	apiKey     string
	model      string
	baseURL    string
	client     *http.Client
	maxTokens  int
}

// ClaudeRunnerOptions configures the Claude API runner.
type ClaudeRunnerOptions struct {
	APIKey    string
	Model     string
	BaseURL   string
	Timeout   time.Duration
	MaxTokens int
}

// NewClaudeRunner creates a Runner that calls the Anthropic Messages API.
func NewClaudeRunner(options ClaudeRunnerOptions) (*ClaudeRunner, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: claude api key", ErrProviderRequired)
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return &ClaudeRunner{
		apiKey:    apiKey,
		model:     model,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: timeout},
		maxTokens: maxTokens,
	}, nil
}

// anthropicMessagesRequest is the request body for the Anthropic Messages API.
type anthropicMessagesRequest struct {
	Model     string                   `json:"model"`
	MaxTokens int                      `json:"max_tokens"`
	Stream    bool                     `json:"stream"`
	System    string                   `json:"system,omitempty"`
	Messages  []anthropicMessageEntry  `json:"messages"`
}

type anthropicMessageEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicMessagesResponse is the response body from the Anthropic Messages API.
type anthropicMessagesResponse struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []anthropicContentBlock `json:"content"`
	Model   string                 `json:"model"`
	Usage   anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// anthropicErrorResponse is the error response shape from the Anthropic API.
type anthropicErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *ClaudeRunner) Run(ctx context.Context, req Request) (Response, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	systemPrompt := buildClaudeSystemPrompt(req)
	userPrompt := buildClaudeUserPrompt(req)

	body, err := json.Marshal(anthropicMessagesRequest{
		Model:     r.model,
		MaxTokens: r.maxTokens,
		Stream:    false,
		System:    systemPrompt,
		Messages: []anthropicMessageEntry{
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "marshal claude request",
			Cause:   err,
		}
	}

	httpReq, err := http.NewRequestWithContext(execCtx, http.MethodPost, r.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-MultiAgentCom-Runtime-Protocol", ProtocolVersion)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("x-api-key", r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "read claude response",
			Cause:   err,
		}
	}
	if len(respBody) > maxRuntimeResponseBytes {
		return Response{}, &ProviderError{
			Code:    ProviderErrorResponseTooLarge,
			Message: "claude response exceeded 1048576 bytes",
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Response{}, decodeClaudeError(resp.StatusCode, respBody)
	}

	return decodeClaudeSuccess(respBody)
}

func decodeClaudeSuccess(payload []byte) (Response, error) {
	var result anthropicMessagesResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "decode claude response",
			Cause:   err,
		}
	}
	output := extractClaudeOutput(result.Content)
	return normalizeUsage(Response{
		Model:            result.Model,
		Output:           output,
		PromptTokens:     result.Usage.InputTokens,
		CompletionTokens: result.Usage.OutputTokens,
		TotalTokens:      result.Usage.InputTokens + result.Usage.OutputTokens,
	}), nil
}

func decodeClaudeError(statusCode int, payload []byte) error {
	var errResp anthropicErrorResponse
	if json.Unmarshal(payload, &errResp) == nil && errResp.Error.Message != "" {
		code := mapAnthropicErrorType(errResp.Error.Type)
		retryable := retryableStatus(statusCode) || statusCode == http.StatusTooManyRequests
		return &ProviderError{
			Code:           code,
			Message:        errResp.Error.Message,
			Retryable:      retryable,
			StatusCode:     statusCode,
			ProviderStatus: statusCode,
		}
	}
	message := strings.TrimSpace(string(payload))
	if message == "" {
		message = "claude api returned non-success status"
	}
	return &ProviderError{
		Code:           ProviderErrorHTTPStatus,
		Message:        message,
		Retryable:      retryableStatus(statusCode),
		StatusCode:     statusCode,
		ProviderStatus: statusCode,
	}
}

func extractClaudeOutput(blocks []anthropicContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func mapAnthropicErrorType(errorType string) string {
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "authentication_error":
		return "CLAUDE_AUTH_ERROR"
	case "permission_error":
		return "CLAUDE_PERMISSION_ERROR"
	case "not_found_error":
		return "CLAUDE_NOT_FOUND"
	case "rate_limit_error":
		return "CLAUDE_RATE_LIMITED"
	case "invalid_request_error":
		return "CLAUDE_INVALID_REQUEST"
	case "overloaded_error":
		return "CLAUDE_OVERLOADED"
	case "api_error":
		return "CLAUDE_API_ERROR"
	default:
		return ProviderErrorHTTPStatus
	}
}

func buildClaudeSystemPrompt(req Request) string {
	parts := []string{
		"You are an AI software development agent in a multi-agent development platform.",
		fmt.Sprintf("Agent type: %s", req.AgentType),
		fmt.Sprintf("Project: %s", req.ProjectID),
	}
	if req.SandboxID != "" {
		parts = append(parts, fmt.Sprintf("Sandbox: %s", req.SandboxID))
	}
	return strings.Join(parts, ". ") + "."
}

func buildClaudeUserPrompt(req Request) string {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = fmt.Sprintf("Execute task %s for project %s", req.TaskID, req.ProjectID)
	}
	if contextText := strings.TrimSpace(req.Context); contextText != "" {
		prompt = prompt + "\n\nContext:\n" + contextText
	}
	return prompt
}
