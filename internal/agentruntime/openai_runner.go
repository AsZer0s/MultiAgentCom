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

// OpenAIRunner implements the Runner interface by calling OpenAI-compatible APIs.
// Supports two API formats:
//   - "chat" (default): /v1/chat/completions — OpenAI GPT-4o, Claude via proxy, Ollama, vLLM, etc.
//   - "completions": /v1/completions — legacy Codex, code-davinci, text-davinci, local completions APIs.
type OpenAIRunner struct {
	apiKey    string
	model     string
	baseURL   string
	client    *http.Client
	maxTokens int
	format    string // "chat" or "completions"
}

// OpenAIRunnerOptions configures the OpenAI-compatible API runner.
type OpenAIRunnerOptions struct {
	APIKey    string
	Model     string
	BaseURL   string
	Timeout   time.Duration
	MaxTokens int
	Format    string // "chat" (default) or "completions" for legacy Codex-style API
}

// NewOpenAIRunner creates a Runner that calls OpenAI-compatible endpoints.
func NewOpenAIRunner(options OpenAIRunnerOptions) (*OpenAIRunner, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: openai api key", ErrProviderRequired)
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "gpt-4o"
	}
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimSuffix(baseURL, "/")
	timeout := options.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	maxTokens := options.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format == "" {
		format = "chat"
	}
	if format != "chat" && format != "completions" {
		return nil, fmt.Errorf("openai format must be 'chat' or 'completions', got %q", format)
	}
	return &OpenAIRunner{
		apiKey:    apiKey,
		model:     model,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: timeout},
		maxTokens: maxTokens,
		format:    format,
	}, nil
}

// ── Chat Completions format (/v1/chat/completions) ──────────────────────

type openAIChatRequest struct {
	Model     string               `json:"model"`
	MaxTokens int                  `json:"max_tokens,omitempty"`
	Messages  []openAIMessageEntry `json:"messages"`
}

type openAIMessageEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

type openAIChoice struct {
	Index        int                `json:"index"`
	Message      openAIMessageEntry `json:"message"`
	FinishReason string             `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ── Legacy Completions format (/v1/completions) ─────────────────────────

type openAICompletionsRequest struct {
	Model     string `json:"model"`
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type openAICompletionsResponse struct {
	ID      string                  `json:"id"`
	Model   string                  `json:"model"`
	Choices []openAICompletionsChoice `json:"choices"`
	Usage   openAIUsage             `json:"usage"`
}

type openAICompletionsChoice struct {
	Index        int    `json:"index"`
	Text         string `json:"text"`
	FinishReason string `json:"finish_reason"`
}

// ── Shared error format ─────────────────────────────────────────────────

type openAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (r *OpenAIRunner) Run(ctx context.Context, req Request) (Response, error) {
	if r.format == "completions" {
		return r.runCompletions(ctx, req)
	}
	return r.runChat(ctx, req)
}

// runChat calls the /v1/chat/completions endpoint (GPT-4o, GPT-4, GPT-3.5, Ollama chat, etc.)
func (r *OpenAIRunner) runChat(ctx context.Context, req Request) (Response, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	systemPrompt := buildOpenAISystemPrompt(req)
	userPrompt := buildOpenAIUserPrompt(req)

	body, err := json.Marshal(openAIChatRequest{
		Model:     r.model,
		MaxTokens: r.maxTokens,
		Messages: []openAIMessageEntry{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "marshal openai chat request",
			Cause:   err,
		}
	}

	httpReq, err := http.NewRequestWithContext(execCtx, http.MethodPost, r.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-MultiAgentCom-Runtime-Protocol", ProtocolVersion)
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "read openai chat response",
			Cause:   err,
		}
	}
	if len(respBody) > maxRuntimeResponseBytes {
		return Response{}, &ProviderError{
			Code:    ProviderErrorResponseTooLarge,
			Message: "openai response exceeded 1048576 bytes",
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Response{}, decodeOpenAIError(resp.StatusCode, respBody)
	}

	return decodeOpenAIChatSuccess(respBody)
}

// runCompletions calls the /v1/completions endpoint (Codex, code-davinci, text-davinci, local models)
func (r *OpenAIRunner) runCompletions(ctx context.Context, req Request) (Response, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	prompt := buildOpenAICompletionsPrompt(req)

	body, err := json.Marshal(openAICompletionsRequest{
		Model:     r.model,
		Prompt:    prompt,
		MaxTokens: r.maxTokens,
	})
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "marshal openai completions request",
			Cause:   err,
		}
	}

	httpReq, err := http.NewRequestWithContext(execCtx, http.MethodPost, r.baseURL+"/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-MultiAgentCom-Runtime-Protocol", ProtocolVersion)
	httpReq.Header.Set("Authorization", "Bearer "+r.apiKey)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "read openai completions response",
			Cause:   err,
		}
	}
	if len(respBody) > maxRuntimeResponseBytes {
		return Response{}, &ProviderError{
			Code:    ProviderErrorResponseTooLarge,
			Message: "openai response exceeded 1048576 bytes",
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Response{}, decodeOpenAIError(resp.StatusCode, respBody)
	}

	return decodeOpenAICompletionsSuccess(respBody)
}

func decodeOpenAIChatSuccess(payload []byte) (Response, error) {
	var result openAIChatResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "decode openai chat response",
			Cause:   err,
		}
	}
	output := extractOpenAIChatOutput(result.Choices)
	return normalizeUsage(Response{
		Model:            result.Model,
		Output:           output,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}), nil
}

func decodeOpenAICompletionsSuccess(payload []byte) (Response, error) {
	var result openAICompletionsResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "decode openai completions response",
			Cause:   err,
		}
	}
	output := extractOpenAICompletionsOutput(result.Choices)
	return normalizeUsage(Response{
		Model:            result.Model,
		Output:           output,
		PromptTokens:     result.Usage.PromptTokens,
		CompletionTokens: result.Usage.CompletionTokens,
		TotalTokens:      result.Usage.TotalTokens,
	}), nil
}

func decodeOpenAIError(statusCode int, payload []byte) error {
	var errResp openAIErrorResponse
	if json.Unmarshal(payload, &errResp) == nil && errResp.Error.Message != "" {
		code := mapOpenAIErrorType(errResp.Error.Type, errResp.Error.Code)
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
		message = "openai api returned non-success status"
	}
	return &ProviderError{
		Code:           ProviderErrorHTTPStatus,
		Message:        message,
		Retryable:      retryableStatus(statusCode),
		StatusCode:     statusCode,
		ProviderStatus: statusCode,
	}
}

func extractOpenAIChatOutput(choices []openAIChoice) string {
	var parts []string
	for _, choice := range choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			parts = append(parts, choice.Message.Content)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractOpenAICompletionsOutput(choices []openAICompletionsChoice) string {
	var parts []string
	for _, choice := range choices {
		if strings.TrimSpace(choice.Text) != "" {
			parts = append(parts, choice.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func mapOpenAIErrorType(errorType, code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "invalid_api_key", "invalid_api_key_error":
		return "OPENAI_AUTH_ERROR"
	case "rate_limit_exceeded":
		return "OPENAI_RATE_LIMITED"
	case "model_not_found":
		return "OPENAI_MODEL_NOT_FOUND"
	case "invalid_request_error":
		return "OPENAI_INVALID_REQUEST"
	case "server_error", "internal_error":
		return "OPENAI_SERVER_ERROR"
	}
	switch strings.ToLower(strings.TrimSpace(errorType)) {
	case "authentication_error":
		return "OPENAI_AUTH_ERROR"
	case "permission_error":
		return "OPENAI_PERMISSION_ERROR"
	case "rate_limit_error":
		return "OPENAI_RATE_LIMITED"
	case "invalid_request_error":
		return "OPENAI_INVALID_REQUEST"
	case "server_error":
		return "OPENAI_SERVER_ERROR"
	}
	return ProviderErrorHTTPStatus
}

func buildOpenAISystemPrompt(req Request) string {
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

func buildOpenAIUserPrompt(req Request) string {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = fmt.Sprintf("Execute task %s for project %s", req.TaskID, req.ProjectID)
	}
	if contextText := strings.TrimSpace(req.Context); contextText != "" {
		prompt = prompt + "\n\nContext:\n" + contextText
	}
	return prompt
}

// buildOpenAICompletionsPrompt builds a single prompt string for the legacy /v1/completions endpoint.
// It combines system and user prompts into one block since the completions API has no message roles.
func buildOpenAICompletionsPrompt(req Request) string {
	system := buildOpenAISystemPrompt(req)
	user := buildOpenAIUserPrompt(req)
	return system + "\n\n" + user
}
