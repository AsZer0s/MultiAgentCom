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

// GeminiRunner implements the Runner interface by calling the Google Gemini API.
// It supports the Gemini API (generativelanguage.googleapis.com) and compatible endpoints.
type GeminiRunner struct {
	apiKey    string
	model     string
	baseURL   string
	client    *http.Client
	maxTokens int
}

// GeminiRunnerOptions configures the Gemini API runner.
type GeminiRunnerOptions struct {
	APIKey    string
	Model     string
	BaseURL   string
	Timeout   time.Duration
	MaxTokens int
}

// NewGeminiRunner creates a Runner that calls the Google Gemini API.
func NewGeminiRunner(options GeminiRunnerOptions) (*GeminiRunner, error) {
	apiKey := strings.TrimSpace(options.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: gemini api key", ErrProviderRequired)
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "gemini-2.0-flash"
	}
	baseURL := strings.TrimSpace(options.BaseURL)
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com/v1beta"
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
	return &GeminiRunner{
		apiKey:    apiKey,
		model:     model,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: timeout},
		maxTokens: maxTokens,
	}, nil
}

// geminiGenerateRequest is the request body for the Gemini generateContent API.
type geminiGenerateRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig,omitempty"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

// geminiGenerateResponse is the response body from the Gemini API.
type geminiGenerateResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

// geminiErrorResponse is the error response shape from the Gemini API.
type geminiErrorResponse struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (r *GeminiRunner) Run(ctx context.Context, req Request) (Response, error) {
	execCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	systemPrompt := buildGeminiSystemPrompt(req)
	userPrompt := buildGeminiUserPrompt(req)

	geminiReq := geminiGenerateRequest{
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: userPrompt}}},
		},
		GenerationConfig: geminiGenerationConfig{
			MaxOutputTokens: r.maxTokens,
		},
	}
	if systemPrompt != "" {
		geminiReq.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		}
	}

	body, err := json.Marshal(geminiReq)
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "marshal gemini request",
			Cause:   err,
		}
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", r.baseURL, r.model, r.apiKey)
	httpReq, err := http.NewRequestWithContext(execCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-MultiAgentCom-Runtime-Protocol", ProtocolVersion)

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return Response{}, transportProviderError(execCtx, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "read gemini response",
			Cause:   err,
		}
	}
	if len(respBody) > maxRuntimeResponseBytes {
		return Response{}, &ProviderError{
			Code:    ProviderErrorResponseTooLarge,
			Message: "gemini response exceeded 1048576 bytes",
		}
	}

	if resp.StatusCode != http.StatusOK {
		return Response{}, decodeGeminiError(resp.StatusCode, respBody)
	}

	return decodeGeminiSuccess(respBody)
}

func decodeGeminiSuccess(payload []byte) (Response, error) {
	var result geminiGenerateResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		return Response{}, &ProviderError{
			Code:    ProviderErrorMalformedResponse,
			Message: "decode gemini response",
			Cause:   err,
		}
	}
	output := extractGeminiOutput(result.Candidates)
	return normalizeUsage(Response{
		Model:            "",
		Output:           output,
		PromptTokens:     result.UsageMetadata.PromptTokenCount,
		CompletionTokens: result.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      result.UsageMetadata.TotalTokenCount,
	}), nil
}

func decodeGeminiError(statusCode int, payload []byte) error {
	var errResp geminiErrorResponse
	if json.Unmarshal(payload, &errResp) == nil && errResp.Error.Message != "" {
		code := mapGeminiErrorType(errResp.Error.Status, statusCode)
		retryable := retryableStatus(statusCode) || statusCode == http.StatusTooManyRequests
		return &ProviderError{
			Code:           code,
			Message:        errResp.Error.Message,
			Retryable:      retryable,
			StatusCode:     statusCode,
			ProviderStatus: errResp.Error.Code,
		}
	}
	message := strings.TrimSpace(string(payload))
	if message == "" {
		message = "gemini api returned non-success status"
	}
	return &ProviderError{
		Code:           ProviderErrorHTTPStatus,
		Message:        message,
		Retryable:      retryableStatus(statusCode),
		StatusCode:     statusCode,
		ProviderStatus: statusCode,
	}
}

func extractGeminiOutput(candidates []geminiCandidate) string {
	var parts []string
	for _, candidate := range candidates {
		for _, part := range candidate.Content.Parts {
			if strings.TrimSpace(part.Text) != "" {
				parts = append(parts, part.Text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func mapGeminiErrorType(status string, statusCode int) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "UNAUTHENTICATED":
		return "GEMINI_AUTH_ERROR"
	case "PERMISSION_DENIED":
		return "GEMINI_PERMISSION_ERROR"
	case "NOT_FOUND":
		return "GEMINI_NOT_FOUND"
	case "RESOURCE_EXHAUSTED":
		return "GEMINI_RATE_LIMITED"
	case "INVALID_ARGUMENT":
		return "GEMINI_INVALID_REQUEST"
	case "INTERNAL":
		return "GEMINI_SERVER_ERROR"
	case "UNAVAILABLE":
		return "GEMINI_UNAVAILABLE"
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return "GEMINI_AUTH_ERROR"
	}
	if statusCode == http.StatusTooManyRequests {
		return "GEMINI_RATE_LIMITED"
	}
	return ProviderErrorHTTPStatus
}

func buildGeminiSystemPrompt(req Request) string {
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

func buildGeminiUserPrompt(req Request) string {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = fmt.Sprintf("Execute task %s for project %s", req.TaskID, req.ProjectID)
	}
	if contextText := strings.TrimSpace(req.Context); contextText != "" {
		prompt = prompt + "\n\nContext:\n" + contextText
	}
	return prompt
}
