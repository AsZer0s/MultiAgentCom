package agentruntime

import (
	"context"
	"errors"
	"strconv"
	"time"
)

var (
	ErrProviderRequired      = errors.New("agentruntime: provider is required")
	ErrRunnerRequired        = errors.New("agentruntime: runner is required")
	ErrRunnerNotRegistered   = errors.New("agentruntime: runner not registered")
	ErrDefaultProviderNotSet = errors.New("agentruntime: default provider not set")
)

type Runner interface {
	Run(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	ProjectID         string
	TaskID            string
	RunID             string
	AgentType         string
	Prompt            string
	Context           string
	Timeout           time.Duration
	SandboxID         string
	SandboxRootPath   string
	WorkspacePath     string
	WorkspaceProvider string
	WorkspaceBranch   string
	WorkspaceBaseRef  string
	WorkspaceHeadRef  string
}

const ProtocolVersion = "runtime.http.v1"

const (
	ProviderErrorHTTPStatus                 = "PROVIDER_HTTP_STATUS"
	ProviderErrorTimeout                    = "PROVIDER_TIMEOUT"
	ProviderErrorNetwork                    = "PROVIDER_NETWORK_ERROR"
	ProviderErrorMalformedResponse          = "PROVIDER_MALFORMED_RESPONSE"
	ProviderErrorResponseTooLarge           = "PROVIDER_RESPONSE_TOO_LARGE"
	ProviderErrorProtocolVersionUnsupported = "PROVIDER_PROTOCOL_VERSION_UNSUPPORTED"
	ProviderErrorContainerFailed            = "PROVIDER_CONTAINER_FAILED"
)

type Response struct {
	Model            string
	Output           string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type Usage struct {
	PromptTokens     int `json:"promptTokens"`
	CompletionTokens int `json:"completionTokens"`
	TotalTokens      int `json:"totalTokens"`
}

type ProviderErrorDetail struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Retryable      bool   `json:"retryable"`
	ProviderStatus int    `json:"providerStatus,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
}

type ProviderError struct {
	Provider       string
	Code           string
	Message        string
	Retryable      bool
	StatusCode     int
	ProviderStatus int
	RequestID      string
	Cause          error
}

func (e *ProviderError) Error() string {
	message := e.Message
	if message == "" {
		message = "runtime provider failed"
	}
	result := "runtime provider failed: code=" + e.Code
	if e.StatusCode != 0 {
		result += " status=" + strconv.Itoa(e.StatusCode)
	}
	if e.ProviderStatus != 0 && e.ProviderStatus != e.StatusCode {
		result += " providerStatus=" + strconv.Itoa(e.ProviderStatus)
	}
	if e.Retryable {
		result += " retryable=true"
	}
	if e.RequestID != "" {
		result += " requestId=" + e.RequestID
	}
	return result + " message=" + message
}

func (e *ProviderError) Unwrap() error {
	return e.Cause
}

func IsRetryableProviderError(err error) bool {
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		return providerErr.Retryable
	}
	return false
}
