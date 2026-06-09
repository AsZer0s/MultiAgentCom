package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"multiagentcom/internal/auth"
	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/service"
	"multiagentcom/internal/store"
)

const maxJSONBodyBytes int64 = 1 << 20

type Server struct {
	cfg     config.Config
	logger  *slog.Logger
	svc     *service.Service
	metrics *MetricsCollector
}

func NewServer(cfg config.Config, logger *slog.Logger, svc *service.Service) http.Handler {
	server := &Server{
		cfg:     cfg,
		logger:  logger,
		svc:     svc,
		metrics: NewMetricsCollector(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.handleHealth)
	mux.HandleFunc("GET /ready", server.handleReady)
	mux.HandleFunc("GET /metrics", server.handleMetrics)
	mux.HandleFunc("GET /status/matrix", server.handleGetStatusMatrix)
	mux.HandleFunc("GET /llm/providers", server.handleListLLMProviders)
	mux.HandleFunc("GET /migrations/status", server.handleMigrationStatus)
	mux.HandleFunc("GET /admin/config", server.handleGetAdminConfig)
	mux.HandleFunc("PUT /admin/config", server.handleUpdateAdminConfig)
	mux.HandleFunc("GET /status/panel", server.handleStatusPanel)
	mux.HandleFunc("GET /status/stream", server.handleStatusStream)
	mux.HandleFunc("GET /auth/oidc/callback", server.handleOIDCCallback)
	mux.HandleFunc("POST /projects", server.handleCreateProject)
	mux.HandleFunc("GET /projects/{id}", server.handleGetProject)
	mux.HandleFunc("POST /projects/{id}/requirements", server.handleAddRequirement)
	mux.HandleFunc("GET /projects/{id}/requirements", server.handleListRequirements)
	mux.HandleFunc("POST /projects/{id}/plan", server.handleGeneratePlan)
	mux.HandleFunc("POST /projects/{id}/contracts/generate", server.handleGenerateContract)
	mux.HandleFunc("POST /projects/{id}/contracts/validate", server.handleValidateContract)
	mux.HandleFunc("GET /projects/{id}/contracts", server.handleListContracts)
	mux.HandleFunc("GET /projects/{id}/contracts/{contractId}", server.handleGetContract)
	mux.HandleFunc("GET /projects/{id}/tasks", server.handleListTasks)
	mux.HandleFunc("POST /projects/{id}/tasks/dispatch", server.handleDispatchTasks)
	mux.HandleFunc("GET /projects/{id}/tasks/{taskId}/context", server.handleGetTaskContext)
	mux.HandleFunc("POST /projects/{id}/tasks/{taskId}/context/generate", server.handleGenerateTaskContext)
	mux.HandleFunc("GET /projects/{id}/communications", server.handleListCommunications)
	mux.HandleFunc("GET /projects/{id}/audit-logs", server.handleListAuditLogs)
	mux.HandleFunc("GET /projects/{id}/alerts", server.handleListAlerts)
	mux.HandleFunc("GET /projects/{id}/token-costs", server.handleGetTokenCosts)
	mux.HandleFunc("POST /projects/{id}/tasks/{taskId}/sandbox/fail", server.handleInjectSandboxFailure)
	mux.HandleFunc("POST /projects/{id}/overrides", server.handleApplyHumanOverride)
	mux.HandleFunc("POST /projects/{id}/locks", server.handleApplyCodeLock)
	mux.HandleFunc("GET /projects/{id}/conflicts", server.handleListConflicts)
	mux.HandleFunc("POST /projects/{id}/conflicts/{conflictId}/resolve", server.handleResolveConflict)
	mux.HandleFunc("POST /projects/{id}/shared-sandbox/merge", server.handleMergeSharedSandbox)
	mux.HandleFunc("POST /projects/{id}/workspaces/cleanup", server.handleCleanupWorkspaces)
	mux.HandleFunc("POST /projects/{id}/workspaces/rebase", server.handleRebaseWorkspaces)
	mux.HandleFunc("GET /projects/{id}/snapshots", server.handleListSnapshots)
	mux.HandleFunc("POST /projects/{id}/snapshots/rollback", server.handleRollbackSnapshot)
	mux.HandleFunc("POST /projects/{id}/preview/start", server.handleStartPreview)
	mux.HandleFunc("GET /projects/{id}/preview/{previewId}", server.handlePreviewPage)
	mux.HandleFunc("GET /projects/{id}/preview/{previewId}/status", server.handleGetPreviewStatus)
	mux.HandleFunc("POST /projects/{id}/tasks/run", server.handleStartRun)
	mux.HandleFunc("POST /projects/{id}/tasks/{taskId}/retry", server.handleRetryTask)
	mux.HandleFunc("POST /projects/{id}/runs/parallel", server.handleStartParallelRun)
	mux.HandleFunc("GET /projects/{id}/runs/{runId}/status", server.handleGetRunStatus)
	mux.HandleFunc("GET /projects/{id}/runs/{runId}/sandbox", server.handleGetRunSandbox)
	mux.HandleFunc("GET /projects/{id}/sandboxes", server.handleListSandboxes)
	mux.HandleFunc("POST /projects/{id}/delivery/export", server.handleExportDelivery)
	mux.HandleFunc("GET /projects/{id}/artifacts/{artifactId}/download", server.handleDownloadArtifact)

	// Serve Vue SPA static files from WebRoot if configured.
	if webRoot := strings.TrimSpace(cfg.WebRoot); webRoot != "" {
		fs := http.FileServer(http.Dir(webRoot))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Try to serve the exact file first.
			path := webRoot + r.URL.Path
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				fs.ServeHTTP(w, r)
				return
			}
			// SPA fallback: serve index.html for any non-file route.
			http.ServeFile(w, r, webRoot+"/index.html")
		})
	}

	return withMiddleware(cfg, logger, server.metrics, mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   s.cfg.ServiceName,
		"timestamp": time.Now().UTC(),
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(s.metrics.RenderPrometheus()))
}

func (s *Server) handleListLLMProviders(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg
	activeProvider := strings.ToLower(strings.TrimSpace(cfg.RuntimeProvider))

	type providerInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Available bool   `json:"available"`
		Active    bool   `json:"active"`
		Model     string `json:"model,omitempty"`
	}

	providers := []providerInfo{
		{
			ID:        "local",
			Name:      "Local (Mock)",
			Available: true,
			Active:    activeProvider == "local",
		},
		{
			ID:        "claude",
			Name:      "Claude (Anthropic)",
			Available: strings.TrimSpace(cfg.RuntimeClaudeAPIKey) != "",
			Active:    activeProvider == "claude",
			Model:     cfg.RuntimeClaudeModel,
		},
		{
			ID:        "openai",
			Name:      "OpenAI Compatible",
			Available: strings.TrimSpace(cfg.RuntimeOpenAIAPIKey) != "",
			Active:    activeProvider == "openai",
			Model:     cfg.RuntimeOpenAIModel,
		},
		{
			ID:        "gemini",
			Name:      "Gemini (Google)",
			Available: strings.TrimSpace(cfg.RuntimeGeminiAPIKey) != "",
			Active:    activeProvider == "gemini",
			Model:     cfg.RuntimeGeminiModel,
		},
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"activeProvider": activeProvider,
		"providers":      providers,
	})
}

func (s *Server) handleMigrationStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg
	if strings.ToLower(strings.TrimSpace(cfg.StoreProvider)) != "postgres" {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "not_applicable",
			"message": "migrations only apply to postgres store",
		})
		return
	}

	migrationsDir := strings.TrimSpace(cfg.MigrationsDir)
	if migrationsDir == "" {
		migrationsDir = "migrations"
	}

	db, err := store.OpenPostgresDB(cfg.PostgresDSN)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "MIGRATION_DB_ERROR",
			"message": err.Error(),
		})
		return
	}
	defer db.Close()

	mgr := store.NewMigrationManager(db, migrationsDir)
	status, err := mgr.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "MIGRATION_STATUS_ERROR",
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"migrations": status,
		"directory":  migrationsDir,
	})
}

func (s *Server) handleGetAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !authorizeAdminOnly(w, r) {
		return
	}
	cfg := s.cfg
	writeJSON(w, http.StatusOK, map[string]any{
		"runtime": map[string]any{
			"provider": cfg.RuntimeProvider,
			"timeout":  cfg.RuntimeTimeout.String(),
			"claude": map[string]any{
				"apiKey":    redactSecret(cfg.RuntimeClaudeAPIKey),
				"model":     cfg.RuntimeClaudeModel,
				"baseURL":   cfg.RuntimeClaudeBaseURL,
				"maxTokens": cfg.RuntimeClaudeMaxTokens,
			},
			"openai": map[string]any{
				"apiKey":    redactSecret(cfg.RuntimeOpenAIAPIKey),
				"model":     cfg.RuntimeOpenAIModel,
				"baseURL":   cfg.RuntimeOpenAIBaseURL,
				"maxTokens": cfg.RuntimeOpenAIMaxTokens,
				"format":    cfg.RuntimeOpenAIFormat,
			},
			"gemini": map[string]any{
				"apiKey":    redactSecret(cfg.RuntimeGeminiAPIKey),
				"model":     cfg.RuntimeGeminiModel,
				"baseURL":   cfg.RuntimeGeminiBaseURL,
				"maxTokens": cfg.RuntimeGeminiMaxTokens,
			},
			"http": map[string]any{
				"endpoint":    cfg.RuntimeEndpoint,
				"bearerToken": redactSecret(cfg.RuntimeHTTPBearerToken),
				"maxAttempts": cfg.RuntimeHTTPMaxAttempts,
				"retryDelay":  cfg.RuntimeHTTPRetryBaseDelay.String(),
			},
		},
		"token": map[string]any{
			"promptPricePerMillion": cfg.TokenPromptPricePerMillion,
			"outputPricePerMillion": cfg.TokenOutputPricePerMillion,
			"budgetWarnUSD":         cfg.TokenBudgetWarnUSD,
			"budgetBlockUSD":        cfg.TokenBudgetBlockUSD,
		},
		"s3": map[string]any{
			"provider": cfg.ArtifactStoreProvider,
			"endpoint": cfg.S3Endpoint,
			"accessKey": redactSecret(cfg.S3AccessKey),
			"secretKey": redactSecret(cfg.S3SecretKey),
			"bucket":   cfg.S3Bucket,
			"region":   cfg.S3Region,
			"useSSL":   cfg.S3UseSSL,
		},
		"alert": map[string]any{
			"webhookURL": redactSecret(cfg.AlertWebhookURL),
		},
		"oidc": map[string]any{
			"issuer":       cfg.OIDCIssuer,
			"clientID":     cfg.OIDCClientID,
			"clientSecret": redactSecret(cfg.OIDCClientSecret),
			"redirectURL":  cfg.OIDCRedirectURL,
		},
	})
}

func (s *Server) handleUpdateAdminConfig(w http.ResponseWriter, r *http.Request) {
	if !authorizeAdminOnly(w, r) {
		return
	}
	var input service.AdminConfigUpdate
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	updated := s.svc.UpdateRuntimeConfig(input)
	writeJSON(w, http.StatusOK, map[string]any{
		"code":    "CONFIG_UPDATED",
		"message": updated,
	})
}

func authorizeAdminOnly(w http.ResponseWriter, r *http.Request) bool {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"code":    "UNAUTHORIZED",
			"message": "authentication required",
		})
		return false
	}
	if !principal.HasAnyRole("admin") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "FORBIDDEN",
			"message": "admin role required",
		})
		return false
	}
	return true
}

func redactSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "****" + value[len(value)-4:]
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	result := s.readiness()
	status := http.StatusOK
	if result.Status != "ready" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, result)
}

func (s *Server) handleGetStatusMatrix(w http.ResponseWriter, r *http.Request) {
	result, err := s.svc.GetStatusMatrix(r.Context(), r.URL.Query().Get("projectId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleStatusPanel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderStatusPanelHTML(s.cfg.ServiceName)))
}

func (s *Server) handleStatusStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"code":    "STREAM_UNSUPPORTED",
			"message": "streaming is not supported",
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	writeStatusEvent := func() bool {
		payload, err := json.Marshal(map[string]any{
			"serverTime": time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: status\ndata: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	if !writeStatusEvent() {
		return
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !writeStatusEvent() {
				return
			}
		}
	}
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.checkIdempotencyOrReject(w, r) {
		return
	}

	var input service.CreateProjectInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	project, err := s.svc.CreateProject(r.Context(), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	project, err := s.svc.GetProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, project)
}

func (s *Server) handleAddRequirement(w http.ResponseWriter, r *http.Request) {
	var input service.AddRequirementInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	requirement, err := s.svc.AddRequirement(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, requirement)
}

func (s *Server) handleListRequirements(w http.ResponseWriter, r *http.Request) {
	requirements, err := s.svc.ListRequirements(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": requirements,
		"count": len(requirements),
	})
}

func (s *Server) handleGeneratePlan(w http.ResponseWriter, r *http.Request) {
	if s.checkIdempotencyOrReject(w, r) {
		return
	}

	result, err := s.svc.GeneratePlan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleGenerateContract(w http.ResponseWriter, r *http.Request) {
	contract, err := s.svc.GenerateContract(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, contract)
}

func (s *Server) handleValidateContract(w http.ResponseWriter, r *http.Request) {
	var input service.ValidateContractInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.ValidateContract(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	status := http.StatusOK
	if !result.Passed {
		status = http.StatusConflict
	}
	writeJSON(w, status, result)
}

func (s *Server) handleListContracts(w http.ResponseWriter, r *http.Request) {
	contracts, err := s.svc.ListContracts(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": contracts,
		"count": len(contracts),
	})
}

func (s *Server) handleGetContract(w http.ResponseWriter, r *http.Request) {
	contract, err := s.svc.GetContract(r.Context(), r.PathValue("id"), r.PathValue("contractId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, contract)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.ListTasks(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": tasks,
		"count": len(tasks),
	})
}

func (s *Server) handleDispatchTasks(w http.ResponseWriter, r *http.Request) {
	if s.checkIdempotencyOrReject(w, r) {
		return
	}

	result, err := s.svc.DispatchTasks(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleGetTaskContext(w http.ResponseWriter, r *http.Request) {
	result, err := s.svc.GetLatestTaskContext(r.Context(), r.PathValue("id"), r.PathValue("taskId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGenerateTaskContext(w http.ResponseWriter, r *http.Request) {
	result, err := s.svc.GenerateTaskContext(r.Context(), r.PathValue("id"), r.PathValue("taskId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListCommunications(w http.ResponseWriter, r *http.Request) {
	page, err := parseStreamPage(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items, err := s.svc.ListCommunicationLogs(r.Context(), r.PathValue("id"), r.URL.Query().Get("taskId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	filtered := make([]domain.CommunicationLog, 0, len(items))
	for _, item := range items {
		if page.includes(item.Timestamp) {
			filtered = append(filtered, item)
		}
	}
	paged := applyPage(filtered, page)
	writeJSON(w, http.StatusOK, streamPageResponse[domain.CommunicationLog]{
		Items:  paged,
		Count:  len(paged),
		Total:  len(filtered),
		Limit:  page.limit,
		Offset: page.offset,
	})
}

func (s *Server) handleGetTokenCosts(w http.ResponseWriter, r *http.Request) {
	trend, err := s.svc.GetTokenCostTrend(r.Context(), r.PathValue("id"), r.URL.Query().Get("taskId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, trend)
}

func (s *Server) handleListAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, err := parseStreamPage(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items, err := s.svc.ListAuditLogs(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	filtered := make([]domain.AuditLog, 0, len(items))
	for _, item := range items {
		if page.includes(item.Timestamp) {
			filtered = append(filtered, item)
		}
	}
	paged := applyPage(filtered, page)
	writeJSON(w, http.StatusOK, streamPageResponse[domain.AuditLog]{
		Items:  paged,
		Count:  len(paged),
		Total:  len(filtered),
		Limit:  page.limit,
		Offset: page.offset,
	})
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	page, err := parseStreamPage(r)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items, err := s.svc.ListAlerts(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	filtered := make([]domain.Alert, 0, len(items))
	for _, item := range items {
		if page.includes(item.Timestamp) {
			filtered = append(filtered, item)
		}
	}
	paged := applyPage(filtered, page)
	writeJSON(w, http.StatusOK, streamPageResponse[domain.Alert]{
		Items:  paged,
		Count:  len(paged),
		Total:  len(filtered),
		Limit:  page.limit,
		Offset: page.offset,
	})
}

func (s *Server) handleInjectSandboxFailure(w http.ResponseWriter, r *http.Request) {
	var input service.InjectSandboxFailureInput
	if err := decodeJSONAllowEmpty(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	if err := s.svc.MarkTaskSandboxFailure(r.Context(), r.PathValue("id"), r.PathValue("taskId"), input.Reason); err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"taskId":  r.PathValue("taskId"),
		"message": "sandbox failure injected",
	})
}

func (s *Server) handleApplyHumanOverride(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.ApplyHumanOverrideInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.ApplyHumanOverride(r.Context(), r.PathValue("id"), input)
	if err != nil {
		if result != nil && result.Conflict != nil {
			writeJSON(w, http.StatusConflict, result)
			return
		}
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleApplyCodeLock(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.ApplyCodeLockInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.ApplyCodeLock(r.Context(), r.PathValue("id"), input)
	if err != nil {
		if result != nil && result.Conflict != nil {
			writeJSON(w, http.StatusConflict, result)
			return
		}
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleListConflicts(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	items, err := s.svc.ListConflictQueue(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
	})
}

func (s *Server) handleResolveConflict(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.ResolveConflictInput
	if err := decodeJSONAllowEmpty(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.ResolveConflictQueueEntry(r.Context(), r.PathValue("id"), r.PathValue("conflictId"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleMergeSharedSandbox(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.MergeSharedSandboxInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.MergeToSharedSandbox(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	status := http.StatusCreated
	if !result.Passed {
		status = http.StatusConflict
	}
	writeJSON(w, status, result)
}

func (s *Server) handleCleanupWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.CleanupWorkspacesInput
	if err := decodeJSONAllowEmpty(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.CleanupWorkspaces(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRebaseWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.RebaseWorkspacesInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.RebaseWorkspaces(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	snapshots, err := s.svc.ListSnapshots(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": snapshots,
		"count": len(snapshots),
	})
}

func (s *Server) handleRollbackSnapshot(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "operator") {
		return
	}
	var input service.RollbackSnapshotInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.RollbackToSnapshot(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleStartPreview(w http.ResponseWriter, r *http.Request) {
	result, err := s.svc.StartPreview(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleGetPreviewStatus(w http.ResponseWriter, r *http.Request) {
	preview, err := s.svc.GetPreview(r.Context(), r.PathValue("id"), r.PathValue("previewId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handlePreviewPage(w http.ResponseWriter, r *http.Request) {
	preview, err := s.svc.GetPreview(r.Context(), r.PathValue("id"), r.PathValue("previewId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderPreviewHTML(*preview)))
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	if s.checkIdempotencyOrReject(w, r) {
		return
	}

	var input service.StartRunInput
	if err := decodeJSONAllowEmpty(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.StartRun(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.svc.RetryTask(r.Context(), r.PathValue("id"), r.PathValue("taskId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleStartParallelRun(w http.ResponseWriter, r *http.Request) {
	if s.checkIdempotencyOrReject(w, r) {
		return
	}

	var input service.ParallelRunInput
	if err := decodeJSONAllowEmpty(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.StartParallelRun(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) handleGetRunSandbox(w http.ResponseWriter, r *http.Request) {
	result, err := s.svc.GetRunSandbox(r.Context(), r.PathValue("id"), r.PathValue("runId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	sandboxes, err := s.svc.ListSandboxes(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": sandboxes,
		"count": len(sandboxes),
	})
}

func (s *Server) handleGetRunStatus(w http.ResponseWriter, r *http.Request) {
	result, err := s.svc.GetRunStatus(r.Context(), r.PathValue("id"), r.PathValue("runId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleExportDelivery(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "delivery") {
		return
	}
	var input service.ExportDeliveryInput
	if err := decodeJSONAllowEmpty(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	artifact, err := s.svc.ExportDelivery(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"artifact":     artifact,
		"downloadPath": "/projects/" + r.PathValue("id") + "/artifacts/" + artifact.ID + "/download",
	})
}

func (s *Server) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	if !authorizeRequest(w, r, r.PathValue("id"), "delivery") {
		return
	}
	artifact, err := s.svc.GetArtifact(r.Context(), r.PathValue("id"), r.PathValue("artifactId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="delivery.zip"`)
	http.ServeFile(w, r, artifact.URI)
}

func decodeJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return &service.AppError{Code: "INVALID_JSON", StatusCode: http.StatusBadRequest, Message: "request body is required"}
	}

	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxJSONBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return &service.AppError{Code: "REQUEST_TOO_LARGE", StatusCode: http.StatusRequestEntityTooLarge, Message: "json body exceeds 1 MiB limit"}
		}
		return &service.AppError{Code: "INVALID_JSON", StatusCode: http.StatusBadRequest, Message: "invalid json body"}
	}
	return nil
}

func decodeJSONAllowEmpty(r *http.Request, target any) error {
	if r.ContentLength == 0 || r.Body == nil {
		return nil
	}
	return decodeJSON(r, target)
}

type streamPage struct {
	limit  int
	offset int
	since  time.Time
	until  time.Time
}

type streamPageResponse[T any] struct {
	Items  []T `json:"items"`
	Count  int `json:"count"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func parseStreamPage(r *http.Request) (streamPage, error) {
	query := r.URL.Query()
	limit, err := parseNonNegativeInt(query.Get("limit"), 100)
	if err != nil || limit <= 0 {
		return streamPage{}, &service.AppError{Code: "INVALID_QUERY", StatusCode: http.StatusBadRequest, Message: "limit must be a positive integer"}
	}
	if limit > 500 {
		limit = 500
	}
	offset, err := parseNonNegativeInt(query.Get("offset"), 0)
	if err != nil {
		return streamPage{}, &service.AppError{Code: "INVALID_QUERY", StatusCode: http.StatusBadRequest, Message: "offset must be a non-negative integer"}
	}

	page := streamPage{limit: limit, offset: offset}
	if since := strings.TrimSpace(query.Get("since")); since != "" {
		parsed, err := time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return streamPage{}, &service.AppError{Code: "INVALID_QUERY", StatusCode: http.StatusBadRequest, Message: "since must be RFC3339"}
		}
		page.since = parsed
	}
	if until := strings.TrimSpace(query.Get("until")); until != "" {
		parsed, err := time.Parse(time.RFC3339Nano, until)
		if err != nil {
			return streamPage{}, &service.AppError{Code: "INVALID_QUERY", StatusCode: http.StatusBadRequest, Message: "until must be RFC3339"}
		}
		page.until = parsed
	}
	return page, nil
}

func parseNonNegativeInt(value string, fallback int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, fmt.Errorf("negative integer")
	}
	return parsed, nil
}

func (p streamPage) includes(timestamp time.Time) bool {
	if !p.since.IsZero() && timestamp.Before(p.since) {
		return false
	}
	if !p.until.IsZero() && timestamp.After(p.until) {
		return false
	}
	return true
}

func applyPage[T any](items []T, page streamPage) []T {
	if page.offset >= len(items) {
		return []T{}
	}
	end := page.offset + page.limit
	if end > len(items) {
		end = len(items)
	}
	return items[page.offset:end]
}

type readinessResult struct {
	Status    string           `json:"status"`
	Service   string           `json:"service"`
	Timestamp time.Time        `json:"timestamp"`
	Checks    []readinessCheck `json:"checks"`
}

type readinessCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func (s *Server) readiness() readinessResult {
	cfg := config.WithDefaults(s.cfg)
	checks := []readinessCheck{}
	addCheck := func(name string, err error) {
		check := readinessCheck{Name: name, Status: "ok"}
		if err != nil {
			check.Status = "failed"
			check.Message = err.Error()
		}
		checks = append(checks, check)
	}

	addCheck("config", config.Validate(cfg))
	_, authErr := auth.New(cfg.APIToken, cfg.AuthTokens, cfg.AuthTokensFile)
	addCheck("auth", authErr)
	switch strings.ToLower(strings.TrimSpace(cfg.StoreProvider)) {
	case "file":
		addCheck("dataRoot", ensureWritableDir(cfg.DataRoot))
		addCheck("fileStoreState", validateFileStoreState(cfg.DataRoot))
	case "postgres":
		addCheck("postgresStore", store.CheckPostgres(context.Background(), cfg.PostgresDSN))
	default:
		addCheck("store", nil)
	}
	addCheck("artifactRoot", ensureWritableDir(cfg.ArtifactRoot))
	addCheck("sandboxRoot", ensureWritableDir(cfg.SandboxRoot))
	if strings.EqualFold(strings.TrimSpace(cfg.WorkspaceProvider), "git") {
		addCheck("gitWorkspace", service.CheckGitWorkspace(context.Background(), cfg))
	} else {
		addCheck("workspace", nil)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.RuntimeProvider), "http") {
		addCheck("runtime", nil)
	} else if strings.EqualFold(strings.TrimSpace(cfg.RuntimeProvider), "local") {
		addCheck("runtime", nil)
	} else if strings.EqualFold(strings.TrimSpace(cfg.RuntimeProvider), "claude") {
		if strings.TrimSpace(cfg.RuntimeClaudeAPIKey) == "" {
			addCheck("runtime", fmt.Errorf("runtime provider claude requires RuntimeClaudeAPIKey"))
		} else {
			addCheck("runtime", nil)
		}
	} else if strings.EqualFold(strings.TrimSpace(cfg.RuntimeProvider), "container") {
		if strings.TrimSpace(cfg.RuntimeContainerImage) == "" {
			addCheck("runtime", fmt.Errorf("runtime provider container requires RuntimeContainerImage"))
		} else if _, err := exec.LookPath(strings.TrimSpace(cfg.RuntimeContainerBinary)); err != nil {
			addCheck("runtime", err)
		} else {
			addCheck("runtime", nil)
		}
	} else if strings.EqualFold(strings.TrimSpace(cfg.RuntimeProvider), "openai") {
		if strings.TrimSpace(cfg.RuntimeOpenAIAPIKey) == "" {
			addCheck("runtime", fmt.Errorf("runtime provider openai requires RuntimeOpenAIAPIKey"))
		} else {
			addCheck("runtime", nil)
		}
	} else if strings.EqualFold(strings.TrimSpace(cfg.RuntimeProvider), "gemini") {
		if strings.TrimSpace(cfg.RuntimeGeminiAPIKey) == "" {
			addCheck("runtime", fmt.Errorf("runtime provider gemini requires RuntimeGeminiAPIKey"))
		} else {
			addCheck("runtime", nil)
		}
	} else {
		addCheck("runtime", fmt.Errorf("runtime provider %q is not registered", cfg.RuntimeProvider))
	}
	if strings.TrimSpace(cfg.AlertWebhookURL) != "" {
		addCheck("alertWebhook", nil)
	}
	if issuer := strings.TrimSpace(cfg.OIDCIssuer); issuer != "" {
		if strings.TrimSpace(cfg.OIDCClientID) == "" {
			addCheck("oidc", fmt.Errorf("OIDC issuer configured but OIDCClientID is missing"))
		} else {
			addCheck("oidc", nil)
		}
	}

	status := "ready"
	for _, check := range checks {
		if check.Status != "ok" {
			status = "not_ready"
			break
		}
	}
	return readinessResult{Status: status, Service: cfg.ServiceName, Timestamp: time.Now().UTC(), Checks: checks}
}

func validateFileStoreState(root string) error {
	path := store.ServiceStatePath(root)
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(payload, &state); err != nil {
		return fmt.Errorf("decode service state: %w", err)
	}
	if state.Version != 1 {
		return fmt.Errorf("unsupported service state version %d", state.Version)
	}
	return nil
}

func ensureWritableDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory")
	}
	file, err := os.CreateTemp(path, ".ready-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := file.Name()
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var appErr *service.AppError
	if errors.As(err, &appErr) {
		writeJSON(w, appErr.StatusCode, map[string]any{
			"code":    appErr.Code,
			"message": appErr.Message,
		})
		return
	}

	writeJSON(w, http.StatusInternalServerError, map[string]any{
		"code":    "INTERNAL_ERROR",
		"message": err.Error(),
	})
}

type contextKey string

const requestIDKey contextKey = "request_id"
const authActorKey contextKey = "auth_actor"

func withMiddleware(cfg config.Config, logger *slog.Logger, metrics *MetricsCollector, next http.Handler) http.Handler {
	return requestIDMiddleware(securityHeadersMiddleware(corsMiddleware(rateLimitMiddleware(authMiddleware(cfg)(recoveryMiddleware(logger)(loggingMiddleware(logger, metrics)(next)))))))
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		next.ServeHTTP(w, r)
	})
}

var corsAllowHeaders = "Authorization, Content-Type, X-API-Key, X-Actor, X-Request-Id, Idempotency-Key"

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // requests per window
	window   time.Duration
}

type visitor struct {
	count    int
	lastSeen time.Time
}

var globalRateLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     100,
	window:   time.Minute,
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	now := time.Now()
	if !exists || now.Sub(v.lastSeen) > rl.window {
		rl.visitors[key] = &visitor{count: 1, lastSeen: now}
		return true
	}
	if v.count >= rl.rate {
		return false
	}
	v.count++
	v.lastSeen = now
	return true
}

func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			key = strings.Split(xff, ",")[0]
		}
		if !globalRateLimiter.allow(strings.TrimSpace(key)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"code":    "RATE_LIMITED",
				"message": "rate limit exceeded, try again later",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = generateRequestID()
		}

		w.Header().Set("X-Request-Id", requestID)
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func loggingMiddleware(logger *slog.Logger, metrics *MetricsCollector) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			writer := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(writer, r)

			duration := time.Since(start)
			logger.Info("http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", writer.status,
				"duration_ms", duration.Milliseconds(),
				"actor", actorFromRequest(r),
				"requestId", requestIDFromContext(r.Context()),
			)
			if metrics != nil {
				metrics.RecordHTTPRequest(r.Method, r.URL.Path, writer.status, duration)
			}
		})
	}
}

func authMiddleware(cfg config.Config) func(http.Handler) http.Handler {
	authenticator, err := auth.New(cfg.APIToken, cfg.AuthTokens, cfg.AuthTokensFile)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" || r.URL.Path == "/ready" {
				next.ServeHTTP(w, r)
				return
			}
			// Allow unauthenticated access to static assets when WebRoot is configured.
			if cfg.WebRoot != "" && isStaticAssetPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{
					"code":    "AUTH_CONFIG_INVALID",
					"message": "auth token configuration is invalid",
				})
				return
			}
			if !authenticator.Required() {
				next.ServeHTTP(w, r)
				return
			}

			token := bearerToken(r.Header.Get("Authorization"))
			if token == "" {
				token = strings.TrimSpace(r.Header.Get("X-API-Key"))
			}
			if token == "" {
				token = strings.TrimSpace(r.URL.Query().Get("token"))
			}
			principal, ok := authenticator.Authenticate(token, time.Now().UTC())
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"code":    "UNAUTHORIZED",
					"message": "missing or invalid api token",
				})
				return
			}
			if headerActor := strings.TrimSpace(r.Header.Get("X-Actor")); headerActor != "" && len(principal.Roles) == 1 && principal.Roles[0] == "admin" && principal.Actor == "api-token" {
				principal.Actor = headerActor
			}

			ctx := context.WithValue(r.Context(), authActorKey, principal.Actor)
			ctx = auth.WithPrincipal(ctx, principal)
			ctx = service.WithActor(ctx, principal.Actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func withRBACPolicy(ctx context.Context, policy *auth.RBACPolicy) context.Context {
	return context.WithValue(ctx, typeRBACPolicyKey{}, policy)
}

type typeRBACPolicyKey struct{}

func rbacPolicyFromContext(ctx context.Context) *auth.RBACPolicy {
	policy, _ := ctx.Value(typeRBACPolicyKey{}).(*auth.RBACPolicy)
	return policy
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": "MISSING_CODE", "message": "authorization code is required"})
		return
	}

	oidcProvider := s.svc.OIDCProvider()
	if oidcProvider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "OIDC_NOT_CONFIGURED", "message": "OIDC provider is not configured"})
		return
	}

	redirectURI := s.cfg.OIDCRedirectURL
	if redirectURI == "" {
		redirectURI = fmt.Sprintf("%s://%s/auth/oidc/callback", r.Header.Get("X-Forwarded-Proto"), r.Host)
	}

	tokenResp, err := oidcProvider.ExchangeCode(r.Context(), code, redirectURI)
	if err != nil {
		s.logger.Warn("oidc code exchange failed", "error", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "OIDC_EXCHANGE_FAILED", "message": "failed to exchange authorization code"})
		return
	}

	if tokenResp.IDToken == "" {
		writeJSON(w, http.StatusBadGateway, map[string]any{"code": "OIDC_NO_ID_TOKEN", "message": "OIDC provider did not return an ID token"})
		return
	}

	claims, err := oidcProvider.VerifyIDToken(r.Context(), tokenResp.IDToken)
	if err != nil {
		s.logger.Warn("oidc id token verification failed", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": "OIDC_INVALID_TOKEN", "message": "ID token verification failed"})
		return
	}

	principal := claims.ToPrincipal()
	writeJSON(w, http.StatusOK, map[string]any{
		"code":         "OIDC_AUTHENTICATED",
		"actor":        principal.Actor,
		"roles":        principal.Roles,
		"projectId":    principal.ProjectID,
		"accessToken":  tokenResp.AccessToken,
		"expiresIn":    tokenResp.ExpiresIn,
	})
}

// isStaticAssetPath returns true for paths that are static frontend assets (JS, CSS, images, favicon)
// or top-level SPA routes that should be served by the frontend router without authentication.
func isStaticAssetPath(path string) bool {
	if path == "/" || path == "/login" || path == "/favicon.svg" {
		return true
	}
	if strings.HasPrefix(path, "/assets/") {
		return true
	}
	// SPA routes: /projects, /projects/:id/board, /projects/:id/hitl, /dashboard, /settings
	if strings.HasPrefix(path, "/projects") || strings.HasPrefix(path, "/dashboard") || path == "/settings" {
		return true
	}
	return false
}

// checkIdempotencyOrReject checks the Idempotency-Key header and returns true if the request
// should be rejected (already applied). Returns false if the request should proceed.
func (s *Server) checkIdempotencyOrReject(w http.ResponseWriter, r *http.Request) bool {
	keyHeader := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if keyHeader == "" {
		return false
	}
	key, err := service.ParseIdempotencyKey(keyHeader)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    "INVALID_IDEMPOTENCY_KEY",
			"message": err.Error(),
		})
		return true
	}
	alreadyApplied, err := s.svc.CheckIdempotency(r.Context(), key)
	if err != nil {
		// Log but don't block on idempotency check failures.
		return false
	}
	if alreadyApplied {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":    "IDEMPOTENCY_CONFLICT",
			"message": "operation with this idempotency key was already applied",
		})
		return true
	}
	return false
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return ""
	}
	return strings.TrimSpace(header[len("Bearer "):])
}

func authorizeRequest(w http.ResponseWriter, r *http.Request, projectID string, roles ...string) bool {
	principal, ok := auth.PrincipalFromContext(r.Context())
	if !ok {
		return true
	}
	// Check RBAC policy if available
	if policy := rbacPolicyFromContext(r.Context()); policy != nil {
		for _, role := range roles {
			if policy.CheckProjectAccess(principal, projectID, role) {
				return true
			}
		}
	}
	if principal.HasAnyRole(roles...) && principal.AllowsProject(projectID) {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"code":    "FORBIDDEN",
		"message": "token is not authorized for this action",
	})
	return false
}

func recoveryMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.Error("panic recovered", "panic", recovered, "requestId", requestIDFromContext(r.Context()))
					writeJSON(w, http.StatusInternalServerError, map[string]any{
						"code":    "INTERNAL_ERROR",
						"message": "internal server error",
					})
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func actorFromRequest(r *http.Request) string {
	actor, _ := r.Context().Value(authActorKey).(string)
	if strings.TrimSpace(actor) == "" {
		return "anonymous"
	}
	return actor
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Flush() {
	flusher, ok := w.ResponseWriter.(http.Flusher)
	if ok {
		flusher.Flush()
	}
}

func requestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func generateRequestID() string {
	return time.Now().UTC().Format("20060102T150405.000000000")
}

func renderPreviewHTML(preview domain.Preview) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Preview %s</title>
  <style>
    :root {
      --bg: #f4f7fb;
      --card: #ffffff;
      --ink: #18212f;
      --muted: #5c6b7d;
      --accent: #0f62fe;
      --accent-soft: rgba(15, 98, 254, 0.12);
      --line: #d7deea;
      --done: #1f9d55;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Avenir Next", "Segoe UI", sans-serif;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, rgba(15, 98, 254, 0.12), transparent 30%%),
        linear-gradient(180deg, #fbfdff 0%%, var(--bg) 100%%);
    }
    .app { max-width: 960px; margin: 0 auto; padding: 28px 18px 48px; }
    .hero { display: grid; gap: 10px; margin-bottom: 22px; }
    .eyebrow { font-size: 12px; letter-spacing: 0.14em; text-transform: uppercase; color: var(--muted); }
    h1 { margin: 0; font-size: clamp(28px, 4vw, 48px); line-height: 0.95; }
    .sub { color: var(--muted); max-width: 680px; line-height: 1.6; }
    .badge-row { display: flex; flex-wrap: wrap; gap: 10px; }
    .badge {
      padding: 8px 12px;
      border-radius: 999px;
      background: var(--card);
      border: 1px solid var(--line);
      font-size: 13px;
      color: var(--muted);
    }
    .layout { display: grid; gap: 18px; }
    .card {
      background: var(--card);
      border: 1px solid var(--line);
      border-radius: 24px;
      box-shadow: 0 18px 48px rgba(24, 33, 47, 0.08);
      padding: 20px;
    }
    .composer { display: grid; gap: 12px; grid-template-columns: 1fr auto; }
    .composer input {
      width: 100%%;
      border: 1px solid var(--line);
      border-radius: 16px;
      padding: 14px 16px;
      font: inherit;
      background: #fff;
    }
    .composer button, .todo-actions button {
      border: 0;
      border-radius: 16px;
      padding: 12px 16px;
      font: inherit;
      cursor: pointer;
    }
    .composer button { background: var(--accent); color: #fff; }
    ul { list-style: none; padding: 0; margin: 0; display: grid; gap: 12px; }
    li {
      border: 1px solid var(--line);
      border-radius: 18px;
      padding: 14px;
      display: grid;
      gap: 12px;
      background: #fff;
    }
    .todo-main { display: flex; gap: 12px; align-items: center; }
    .todo-main input[type="text"] {
      flex: 1;
      border: 0;
      font: inherit;
      color: var(--ink);
      background: transparent;
    }
    .todo-main input[type="text"]:focus { outline: none; }
    .todo-actions { display: flex; gap: 8px; }
    .todo-actions button { background: var(--accent-soft); color: var(--accent); }
    .todo-main.done input[type="text"] { text-decoration: line-through; color: var(--muted); }
    .hot { color: var(--done); font-weight: 600; }
    @media (max-width: 640px) {
      .composer { grid-template-columns: 1fr; }
      .todo-main { align-items: flex-start; }
    }
  </style>
</head>
<body>
  <main class="app">
    <section class="hero">
      <div class="eyebrow">Live Preview</div>
      <h1>Todo Preview Workspace</h1>
      <div class="sub">这是基于共享沙盒发布的最小预览页，支持 Todo 的新增、编辑、完成和删除，并通过 revision 轮询提供 MVP 级热更新。</div>
      <div class="badge-row">
        <div class="badge">Preview ID: %s</div>
        <div class="badge">Sandbox: %s</div>
        <div class="badge">Revision: <span id="revision">%s</span></div>
        <div class="badge hot" id="hot-status">Hot reload watching</div>
      </div>
    </section>

    <section class="layout">
      <div class="card">
        <div class="composer">
          <input id="new-todo" type="text" placeholder="Add a todo and press Create">
          <button id="add-btn" type="button">Create</button>
        </div>
      </div>
      <div class="card">
        <ul id="todo-list"></ul>
      </div>
    </section>
  </main>

  <script>
    const authQuery = window.location.search || "";
    const todos = [
      { id: crypto.randomUUID(), title: "Review shared sandbox merge", done: false },
      { id: crypto.randomUUID(), title: "Verify rollback branch timeline", done: true }
    ];
    const list = document.getElementById("todo-list");
    const input = document.getElementById("new-todo");
    const addBtn = document.getElementById("add-btn");
    const revisionNode = document.getElementById("revision");
    let currentRevision = %q;

    function render() {
      list.innerHTML = "";
      todos.forEach((todo) => {
        const item = document.createElement("li");
        const main = document.createElement("div");
        main.className = "todo-main" + (todo.done ? " done" : "");

        const checkbox = document.createElement("input");
        checkbox.type = "checkbox";
        checkbox.checked = todo.done;
        checkbox.addEventListener("change", () => {
          todo.done = checkbox.checked;
          render();
        });

        const title = document.createElement("input");
        title.type = "text";
        title.value = todo.title;
        title.addEventListener("input", (event) => {
          todo.title = event.target.value;
        });

        const actions = document.createElement("div");
        actions.className = "todo-actions";
        const remove = document.createElement("button");
        remove.type = "button";
        remove.textContent = "Delete";
        remove.addEventListener("click", () => {
          const index = todos.findIndex((entry) => entry.id === todo.id);
          if (index >= 0) {
            todos.splice(index, 1);
            render();
          }
        });
        actions.appendChild(remove);

        main.appendChild(checkbox);
        main.appendChild(title);
        item.appendChild(main);
        item.appendChild(actions);
        list.appendChild(item);
      });
    }

    function createTodo() {
      const title = input.value.trim();
      if (!title) return;
      todos.unshift({ id: crypto.randomUUID(), title, done: false });
      input.value = "";
      render();
    }

    addBtn.addEventListener("click", createTodo);
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") createTodo();
    });

    async function pollRevision() {
      try {
        const resp = await fetch(window.location.pathname + "/status" + authQuery, { headers: { "Accept": "application/json" } });
        if (!resp.ok) return;
        const data = await resp.json();
        revisionNode.textContent = data.revision;
        if (data.revision !== currentRevision) {
          currentRevision = data.revision;
          window.location.reload();
        }
      } catch (_) {
      }
    }

    render();
    window.setInterval(pollRevision, 3000);
  </script>
</body>
</html>`, preview.ID, preview.ID, preview.SandboxID, preview.Revision, preview.Revision)
}

func renderStatusPanelHTML(serviceName string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s Operations Dashboard</title>
  <style>
    :root {
      --bg: #f4efe7;
      --card: rgba(255, 252, 247, 0.94);
      --line: #d6cab8;
      --ink: #1f1b16;
      --muted: #6a5d4d;
      --accent: #165dff;
      --accent-soft: rgba(22, 93, 255, 0.12);
      --ok: #0f8b4c;
      --warn: #b7791f;
      --bad: #b42318;
      --hold: #8a3ffc;
      --idle: #7c8798;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Avenir Next", "Segoe UI", sans-serif;
      color: var(--ink);
      background:
        radial-gradient(circle at top left, rgba(22, 93, 255, 0.12), transparent 28%%),
        radial-gradient(circle at bottom right, rgba(180, 35, 24, 0.08), transparent 24%%),
        linear-gradient(180deg, #fbf7f1 0%%, var(--bg) 100%%);
    }
    .shell { max-width: 1280px; margin: 0 auto; padding: 32px 20px 48px; }
    .hero { display: grid; gap: 12px; margin-bottom: 22px; }
    .eyebrow { font-size: 12px; letter-spacing: 0.18em; text-transform: uppercase; color: var(--muted); }
    h1 { margin: 0; font-size: clamp(32px, 5vw, 56px); line-height: 0.94; }
    h2, h3 { margin: 0; }
    .sub { max-width: 760px; color: var(--muted); font-size: 16px; line-height: 1.5; }
    .toolbar { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; margin: 18px 0; }
    .toolbar select, .toolbar input, .toolbar button {
      border: 1px solid var(--line);
      border-radius: 999px;
      background: var(--card);
      color: var(--ink);
      padding: 10px 16px;
      font: inherit;
    }
    .toolbar button { background: var(--ink); color: #fff; cursor: pointer; }
    .inline-action { border: 1px solid var(--line); border-radius: 999px; background: var(--ink); color: #fff; cursor: pointer; padding: 7px 12px; font: inherit; font-size: 12px; }
    .inline-action:disabled { cursor: wait; opacity: 0.65; }
    .dashboard-grid { display: grid; gap: 18px; }
    .two-col { display: grid; gap: 18px; grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .panel {
      background: var(--card);
      backdrop-filter: blur(14px);
      border: 1px solid rgba(214, 202, 184, 0.8);
      border-radius: 24px;
      box-shadow: 0 18px 60px rgba(31, 27, 22, 0.08);
      padding: 20px;
      min-width: 0;
    }
    .project-head { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 12px; margin-bottom: 14px; }
    .meta, .kpi-grid, .check-grid { display: flex; gap: 8px; flex-wrap: wrap; }
    .kpi-grid { margin: 12px 0 2px; }
    .pill, .kpi, .check {
      border-radius: 999px;
      padding: 6px 10px;
      background: #fff;
      border: 1px solid var(--line);
      font-size: 12px;
      color: var(--muted);
    }
    .kpi { display: grid; gap: 2px; min-width: 118px; border-radius: 18px; padding: 12px 14px; }
    .kpi strong { color: var(--ink); font-size: 22px; }
    .check.ok { border-color: rgba(15, 139, 76, 0.35); color: var(--ok); }
    .check.failed { border-color: rgba(180, 35, 24, 0.35); color: var(--bad); }
    .status { display: inline-flex; align-items: center; gap: 8px; font-weight: 600; }
    .dot { width: 10px; height: 10px; border-radius: 999px; background: var(--idle); }
    .status.RUNNING .dot, .status.ACTIVE .dot, .status.SUCCEEDED .dot { background: var(--accent); }
    .status.READY .dot, .status.PENDING .dot, .status.RELEASED .dot { background: var(--warn); }
    .status.BLOCKED .dot, .status.FAILED .dot, .status.ERROR .dot, .status.CRITICAL .dot, .status.OPEN .dot { background: var(--bad); }
    .status.HUMAN_OVERRIDE .dot { background: var(--hold); }
    .status.COMPLETED .dot, .status.DONE .dot, .status.OK .dot, .status.RESOLVED .dot { background: var(--ok); }
    table { width: 100%%; border-collapse: collapse; margin-top: 12px; }
    th, td { text-align: left; padding: 12px 10px; border-top: 1px solid rgba(214, 202, 184, 0.7); vertical-align: top; font-size: 14px; }
    th { color: var(--muted); font-weight: 600; }
    .task-meta { margin-top: 4px; color: var(--muted); font-size: 12px; word-break: break-all; }
    .task-row.highlight td, .item.highlight { background: var(--accent-soft); }
    .topology-wrap { margin-top: 12px; overflow-x: auto; }
    .topology-svg { min-width: 760px; width: 100%%; border: 1px solid rgba(214, 202, 184, 0.7); border-radius: 20px; background: rgba(255,255,255,0.55); }
    .topology-edge { stroke: rgba(103, 92, 79, 0.45); stroke-width: 2; fill: none; marker-end: url(#arrow); }
    .topology-lane { fill: rgba(255,255,255,0.7); stroke: rgba(214, 202, 184, 0.7); }
    .topology-label { fill: var(--muted); font-size: 12px; }
    .topology-node { cursor: pointer; }
    .topology-card { fill: #fff; stroke: var(--line); stroke-width: 1.5; filter: drop-shadow(0 8px 16px rgba(24, 20, 16, 0.08)); }
    .topology-node.SUCCEEDED .topology-card, .topology-node.DONE .topology-card, .topology-node.COMPLETED .topology-card { stroke: rgba(15, 139, 76, 0.55); }
    .topology-node.RUNNING .topology-card, .topology-node.ACTIVE .topology-card { stroke: rgba(22, 93, 255, 0.6); }
    .topology-node.FAILED .topology-card, .topology-node.ERROR .topology-card, .topology-node.BLOCKED .topology-card { stroke: rgba(180, 35, 24, 0.6); }
    .topology-title { fill: var(--ink); font-size: 13px; font-weight: 700; }
    .topology-meta { fill: var(--muted); font-size: 11px; }
    .topology-badge { fill: var(--accent-soft); stroke: rgba(22, 93, 255, 0.2); }
    .item-list { display: grid; gap: 12px; margin-top: 12px; }
    .item {
      border: 1px solid rgba(214, 202, 184, 0.7);
      border-radius: 18px;
      padding: 12px 14px;
      background: rgba(255,255,255,0.6);
    }
    .item.CRITICAL, .item.FAILED, .item.OPEN { border-color: rgba(180, 35, 24, 0.45); box-shadow: inset 0 0 0 1px rgba(180, 35, 24, 0.12); }
    .item.ERROR, .item.WARN { border-color: rgba(183, 121, 31, 0.45); box-shadow: inset 0 0 0 1px rgba(183, 121, 31, 0.12); }
    .item.RESOLVED { border-color: rgba(15, 139, 76, 0.42); box-shadow: inset 0 0 0 1px rgba(15, 139, 76, 0.10); }
    .trend-head { display: flex; justify-content: space-between; gap: 12px; align-items: baseline; margin-bottom: 8px; }
    .trend-head strong { display: block; }
    .bar-track { width: 100%%; height: 10px; border-radius: 999px; background: rgba(22, 93, 255, 0.12); overflow: hidden; }
    .bar-fill { height: 100%%; border-radius: 999px; background: linear-gradient(90deg, #165dff 0%%, #0f8b4c 100%%); }
    .statline { margin-top: 8px; color: var(--muted); font-size: 12px; }
    .empty { padding: 28px; border: 1px dashed var(--line); border-radius: 20px; color: var(--muted); text-align: center; background: rgba(255,255,255,0.5); }
    .error { color: var(--bad); }
    @media (max-width: 920px) { .two-col { grid-template-columns: 1fr; } }
    @media (max-width: 760px) {
      .panel { padding: 16px; }
      th:nth-child(3), td:nth-child(3), th:nth-child(5), td:nth-child(5) { display: none; }
    }
  </style>
</head>
<body>
  <main class="shell">
    <section class="hero">
      <div class="eyebrow">Operational View</div>
      <h1>Operations Dashboard</h1>
      <div class="sub">集中查看 readiness、Status Matrix、Failure Alerts、HITL Conflicts、Audit Trail、Agent Message Log、Token Cost Trend、Sandboxes 和 Snapshots。页面复用现有 JSON/SSE API，并在 SSE 中断时保留轮询兜底。</div>
    </section>

    <section id="readinessPanel" class="panel"></section>

    <div class="toolbar" aria-label="Project and task controls">
      <select id="projectFilter"></select>
      <input id="taskLogFilter" type="text" placeholder="Filter communications by taskId">
      <select id="logLimit">
        <option value="10">10 logs</option>
        <option value="25" selected>25 logs</option>
        <option value="50">50 logs</option>
        <option value="100">100 logs</option>
      </select>
      <button id="refreshBtn" type="button">Refresh Now</button>
      <div id="generatedAt" class="pill">loading...</div>
      <div id="streamState" class="pill">SSE connecting</div>
    </div>

    <section id="kpiPanel" class="panel"></section>
    <div class="dashboard-grid">
      <section id="topologyPanel" class="panel"></section>
      <section id="matrixPanel" class="panel"></section>
      <div class="two-col">
        <section id="alertPanel" class="panel"></section>
        <section id="conflictPanel" class="panel"></section>
      </div>
      <section id="auditPanel" class="panel"></section>
      <section id="commPanel" class="panel"></section>
      <section id="costPanel" class="panel"></section>
      <div class="two-col">
        <section id="sandboxPanel" class="panel"></section>
        <section id="snapshotPanel" class="panel"></section>
      </div>
    </div>
  </main>
  <script>
    const authQuery = new URLSearchParams(window.location.search).get("token");
    const filter = document.getElementById("projectFilter");
    const taskLogFilter = document.getElementById("taskLogFilter");
    const logLimit = document.getElementById("logLimit");
    const refreshBtn = document.getElementById("refreshBtn");
    const readinessPanel = document.getElementById("readinessPanel");
    const kpiPanel = document.getElementById("kpiPanel");
    const topologyPanel = document.getElementById("topologyPanel");
    const matrixPanel = document.getElementById("matrixPanel");
    const generatedAt = document.getElementById("generatedAt");
    const streamState = document.getElementById("streamState");
    const alertPanel = document.getElementById("alertPanel");
    const conflictPanel = document.getElementById("conflictPanel");
    const auditPanel = document.getElementById("auditPanel");
    const commPanel = document.getElementById("commPanel");
    const costPanel = document.getElementById("costPanel");
    const sandboxPanel = document.getElementById("sandboxPanel");
    const snapshotPanel = document.getElementById("snapshotPanel");

    function el(tag, className, text) {
      const node = document.createElement(tag);
      if (className) node.className = className;
      if (text !== undefined && text !== null) node.textContent = String(text);
      return node;
    }

    function withAuth(path) {
      if (!authQuery) return path;
      return path + (path.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(authQuery);
    }

    function statusClass(value) {
      const allowed = new Set(['RUNNING', 'READY', 'BLOCKED', 'HUMAN_OVERRIDE', 'COMPLETED', 'DONE', 'IDLE', 'CREATED', 'IN_PROGRESS', 'FAILED', 'PENDING', 'SUCCEEDED', 'ACTIVE', 'RELEASED', 'OK', 'ERROR', 'CRITICAL', 'OPEN', 'RESOLVED']);
      const normalized = String(value || 'IDLE').toUpperCase().replace(/[^A-Z_]/g, '_');
      return allowed.has(normalized) ? normalized : 'IDLE';
    }

    function statusBadge(status) {
      const normalized = statusClass(status);
      const badge = el('span', 'status ' + normalized);
      badge.appendChild(el('span', 'dot'));
      badge.appendChild(document.createTextNode(status || 'IDLE'));
      return badge;
    }

    function pill(text) {
      return el('span', 'pill', text);
    }

    function empty(text) {
      return el('div', 'empty', text);
    }

    function panelHead(eyebrow, title, pills) {
      const head = el('div', 'project-head');
      const titleWrap = el('div');
      titleWrap.appendChild(el('div', 'eyebrow', eyebrow));
      titleWrap.appendChild(el('h2', '', title));
      const meta = el('div', 'meta');
      (pills || []).forEach((entry) => meta.appendChild(pill(entry)));
      head.appendChild(titleWrap);
      head.appendChild(meta);
      return head;
    }

    function renderError(panel, title, err) {
      panel.replaceChildren(panelHead('Error', title, []), el('div', 'empty error', err.message || String(err)));
    }

    async function fetchJSON(path) {
      const response = await fetch(withAuth(path), { headers: { 'Accept': 'application/json' } });
      if (!response.ok) throw new Error(path + ' returned ' + response.status);
      return response.json();
    }

    async function postJSON(path, payload) {
      const response = await fetch(withAuth(path), {
        method: 'POST',
        headers: { 'Accept': 'application/json', 'Content-Type': 'application/json' },
        body: JSON.stringify(payload || {})
      });
      if (!response.ok) throw new Error(path + ' returned ' + response.status);
      return response.json();
    }

    function formatTime(value) {
      if (!value) return '-';
      const date = new Date(value);
      if (Number.isNaN(date.getTime())) return String(value);
      return date.toLocaleString();
    }

    function renderReadiness(data) {
      const checks = el('div', 'check-grid');
      (data.checks || []).forEach((check) => {
        const item = el('span', 'check ' + (check.status === 'ok' ? 'ok' : 'failed'), check.name + ': ' + check.status);
        if (check.message) item.title = check.message;
        checks.appendChild(item);
      });
      readinessPanel.replaceChildren(
        panelHead('Readiness', 'Readiness', [data.service || '%s', data.status || 'unknown', 'Last refresh ' + formatTime(data.timestamp)]),
        checks
      );
    }

    function renderProjectOptions(view) {
      const selected = view.selectedProjectId || filter.value || '';
      const options = [el('option', '', 'All Projects')];
      options[0].value = '';
      (view.projects || []).forEach((project) => {
        const option = el('option', '', project.name || project.id);
        option.value = project.id || '';
        option.selected = option.value === selected;
        options.push(option);
      });
      filter.replaceChildren(...options);
      filter.value = selected;
    }

    function renderKPIs(view, costData) {
      const matrices = view.matrices || [];
      const totals = matrices.reduce((acc, matrix) => {
        acc.tasks += matrix.totalTasks || 0;
        acc.ready += matrix.readyTasks || 0;
        acc.running += matrix.runningTasks || 0;
        acc.override += matrix.overrideTasks || 0;
        acc.completed += matrix.completedTasks || 0;
        acc.failed += matrix.failedTasks || 0;
        return acc;
      }, { tasks: 0, ready: 0, running: 0, override: 0, completed: 0, failed: 0 });
      const grid = el('div', 'kpi-grid');
      [
        ['Projects', (view.projects || []).length],
        ['Tasks', totals.tasks],
        ['Ready', totals.ready],
        ['Running', totals.running],
        ['Completed', totals.completed],
        ['Failed', totals.failed],
        ['Human Override', totals.override],
        ['Budget', costData ? (costData.budgetStatus || 'ok') : '-']
      ].forEach(([label, value]) => {
        const card = el('div', 'kpi');
        card.appendChild(el('span', '', label));
        card.appendChild(el('strong', '', value));
        grid.appendChild(card);
      });
      kpiPanel.replaceChildren(panelHead('Summary', 'KPI Cards', []), grid);
    }

    function renderMatrix(view) {
      renderProjectOptions(view);
      generatedAt.textContent = view.generatedAt ? 'Generated at ' + formatTime(view.generatedAt) : 'No data';
      const matrices = view.matrices || [];
      if (!matrices.length) {
        matrixPanel.replaceChildren(panelHead('Status Matrix', 'Status Matrix', []), empty('还没有可展示的项目状态。先创建项目并派发任务，再回来查看。'));
        renderTopology(view, null);
        return;
      }

      const content = el('div', 'dashboard-grid');
      const selectedTaskId = taskLogFilter.value.trim();
      matrices.forEach((matrix) => {
        const project = matrix.project || {};
        const section = el('section', 'item');
        section.appendChild(panelHead('Project', project.name || project.id || '-', [
          'Ready ' + (matrix.readyTasks || 0),
          'Running ' + (matrix.runningTasks || 0),
          'Done ' + (matrix.completedTasks || 0),
          'Failed ' + (matrix.failedTasks || 0)
        ]));

        const agentTable = table(['Agent', 'Status', 'Total', 'Running', 'Done', 'Failed']);
        const agentBody = agentTable.querySelector('tbody');
        (matrix.agentMatrix || []).forEach((agent) => {
          const row = el('tr');
          appendCells(row, [agent.agent || '-', statusBadge(agent.status), agent.totalTasks || 0, agent.runningTasks || 0, agent.doneTasks || 0, agent.failedTasks || 0]);
          agentBody.appendChild(row);
        });
        if (!(matrix.agentMatrix || []).length) appendEmptyRow(agentBody, 6, 'No agents');
        section.appendChild(agentTable);

        const taskTable = table(['Task', 'Agent', 'Type', 'Status', 'Latest Run', 'Depends On']);
        const taskBody = taskTable.querySelector('tbody');
        (matrix.taskMatrix || []).forEach((task) => {
          const row = el('tr', selectedTaskId && task.id === selectedTaskId ? 'task-row highlight' : 'task-row');
          const taskCell = el('td');
          taskCell.appendChild(el('strong', '', task.name || '-'));
          taskCell.appendChild(el('div', 'task-meta', task.id || '-'));
          appendCells(row, [taskCell, task.assigneeAgent || '-', task.type || '-', task.status || '-', task.latestRunStatus || '-', (task.dependsOn || []).join(', ') || '-']);
          taskBody.appendChild(row);
        });
        if (!(matrix.taskMatrix || []).length) appendEmptyRow(taskBody, 6, 'No tasks');
        section.appendChild(taskTable);
        content.appendChild(section);
      });
      matrixPanel.replaceChildren(panelHead('Status Matrix', 'Agent and Task Matrix', []), content);
    }

    function svgEl(tag, attrs, text) {
      const node = document.createElementNS('http://www.w3.org/2000/svg', tag);
      Object.entries(attrs || {}).forEach(([key, value]) => node.setAttribute(key, String(value)));
      if (text !== undefined && text !== null) node.textContent = String(text);
      return node;
    }

    function renderTopology(view, communications) {
      const projectId = filter.value || '';
      if (!projectId) {
        topologyPanel.replaceChildren(panelHead('Topology', 'Task Topology', []), empty('选择一个项目后可查看任务拓扑。'));
        return;
      }
      const matrix = (view.matrices || []).find((item) => (item.project || {}).id === projectId) || (view.matrices || [])[0];
      const tasks = matrix ? (matrix.taskMatrix || []) : [];
      if (!tasks.length) {
        topologyPanel.replaceChildren(panelHead('Topology', 'Task Topology', ['Project ' + projectId]), empty('No tasks to visualize.'));
        return;
      }

      const taskById = new Map(tasks.map((task) => [task.id, task]));
      const depthCache = new Map();
      let hasWarning = false;
      function depthFor(task, visiting) {
        if (!task || !task.id) return 0;
        if (depthCache.has(task.id)) return depthCache.get(task.id);
        if (visiting.has(task.id)) {
          hasWarning = true;
          return 0;
        }
        visiting.add(task.id);
        const depths = (task.dependsOn || []).map((depId) => {
          const dep = taskById.get(depId);
          if (!dep) {
            hasWarning = true;
            return -1;
          }
          return depthFor(dep, visiting);
        });
        visiting.delete(task.id);
        const depth = depths.length ? Math.max(...depths) + 1 : 0;
        depthCache.set(task.id, depth);
        return depth;
      }

      const lanes = Array.from(new Set(tasks.map((task) => task.assigneeAgent || 'unassigned'))).sort();
      const laneIndex = new Map(lanes.map((lane, index) => [lane, index]));
      const placed = tasks.map((task, index) => ({ task, index, depth: depthFor(task, new Set()), lane: laneIndex.get(task.assigneeAgent || 'unassigned') || 0 }));
      const stackCounts = new Map();
      placed.forEach((node) => {
        const key = node.depth + ':' + node.lane;
        const count = stackCounts.get(key) || 0;
        node.stack = count;
        stackCounts.set(key, count + 1);
      });
      const nodeById = new Map(placed.map((node) => [node.task.id, node]));
      const comms = communications || { items: [] };
      const commCount = new Map();
      (comms.items || []).forEach((item) => {
        if (!item.taskId) return;
        commCount.set(item.taskId, (commCount.get(item.taskId) || 0) + 1);
      });

      const colWidth = 220;
      const laneHeight = 132;
      const stackOffset = 34;
      const cardWidth = 172;
      const cardHeight = 86;
      const left = 120;
      const top = 56;
      const maxDepth = Math.max(...placed.map((node) => node.depth), 0);
      const maxStack = Math.max(...placed.map((node) => node.stack), 0);
      const width = Math.max(760, left + (maxDepth + 1) * colWidth + cardWidth + 60);
      const height = Math.max(260, top + lanes.length * laneHeight + maxStack * stackOffset + 40);
      placed.forEach((node) => {
        node.x = left + node.depth * colWidth;
        node.y = top + node.lane * laneHeight + node.stack * stackOffset;
      });

      const svg = svgEl('svg', { class: 'topology-svg', viewBox: '0 0 ' + width + ' ' + height, role: 'img', 'aria-label': 'Task dependency topology' });
      const defs = svgEl('defs');
      const marker = svgEl('marker', { id: 'arrow', viewBox: '0 0 10 10', refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: 'auto-start-reverse' });
      marker.appendChild(svgEl('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: 'rgba(103, 92, 79, 0.55)' }));
      defs.appendChild(marker);
      svg.appendChild(defs);

      lanes.forEach((lane, index) => {
        const y = top - 26 + index * laneHeight;
        svg.appendChild(svgEl('rect', { class: 'topology-lane', x: 20, y, width: width - 40, height: laneHeight - 12, rx: 16 }));
        svg.appendChild(svgEl('text', { class: 'topology-label', x: 34, y: y + 24 }, lane));
      });

      placed.forEach((node) => {
        (node.task.dependsOn || []).forEach((depId) => {
          const dep = nodeById.get(depId);
          if (!dep) return;
          svg.appendChild(svgEl('path', {
            class: 'topology-edge',
            d: 'M ' + (dep.x + cardWidth) + ' ' + (dep.y + cardHeight / 2) + ' C ' + (dep.x + cardWidth + 45) + ' ' + (dep.y + cardHeight / 2) + ', ' + (node.x - 45) + ' ' + (node.y + cardHeight / 2) + ', ' + node.x + ' ' + (node.y + cardHeight / 2)
          }));
        });
      });

      placed.forEach((node) => {
        const task = node.task;
        const group = svgEl('g', { class: 'topology-node ' + statusClass(task.latestRunStatus || task.status), tabindex: 0 });
        group.appendChild(svgEl('title', {}, [task.id || '-', task.type || '-', task.assigneeAgent || '-', 'task=' + (task.status || '-'), 'run=' + (task.latestRunStatus || '-')].join(' · ')));
        group.appendChild(svgEl('rect', { class: 'topology-card', x: node.x, y: node.y, width: cardWidth, height: cardHeight, rx: 14 }));
        group.appendChild(svgEl('text', { class: 'topology-title', x: node.x + 14, y: node.y + 24 }, task.name || task.id || '-'));
        group.appendChild(svgEl('text', { class: 'topology-meta', x: node.x + 14, y: node.y + 44 }, (task.type || '-') + ' · ' + (task.status || '-')));
        group.appendChild(svgEl('text', { class: 'topology-meta', x: node.x + 14, y: node.y + 62 }, 'run ' + (task.latestRunStatus || '-')));
        const count = commCount.get(task.id) || 0;
        if (count > 0) {
          group.appendChild(svgEl('rect', { class: 'topology-badge', x: node.x + cardWidth - 54, y: node.y + 54, width: 40, height: 20, rx: 10 }));
          group.appendChild(svgEl('text', { class: 'topology-meta', x: node.x + cardWidth - 44, y: node.y + 68 }, count + ' msg'));
        }
        group.addEventListener('click', function() {
          taskLogFilter.value = task.id || '';
          loadDashboard();
        });
        group.addEventListener('keydown', function(event) {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            taskLogFilter.value = task.id || '';
            loadDashboard();
          }
        });
        svg.appendChild(group);
      });

      const wrap = el('div', 'topology-wrap');
      wrap.appendChild(svg);
      const legend = el('div', 'meta');
      legend.appendChild(pill('Dependency edge: prerequisite → dependent'));
      legend.appendChild(pill('Badge: communication count'));
      legend.appendChild(pill('Click a task to filter logs'));
      const pills = ['Project ' + projectId, 'Tasks ' + tasks.length, 'Agents ' + lanes.length];
      if (hasWarning) pills.push('Topology warning: missing or cyclic dependency');
      topologyPanel.replaceChildren(panelHead('Topology', 'Task Topology', pills), wrap, legend);
    }

    function table(headers) {
      const node = el('table');
      const thead = el('thead');
      const headRow = el('tr');
      headers.forEach((header) => headRow.appendChild(el('th', '', header)));
      thead.appendChild(headRow);
      node.appendChild(thead);
      node.appendChild(el('tbody'));
      return node;
    }

    function appendCells(row, cells) {
      cells.forEach((value) => {
        if (value instanceof Node) {
          row.appendChild(value.tagName === 'TD' ? value : wrapCell(value));
          return;
        }
        row.appendChild(el('td', '', value));
      });
    }

    function wrapCell(node) {
      const cell = el('td');
      cell.appendChild(node);
      return cell;
    }

    function appendEmptyRow(body, span, text) {
      const row = el('tr');
      const cell = el('td', '', text);
      cell.colSpan = span;
      row.appendChild(cell);
      body.appendChild(row);
    }

    function renderAlerts(projectId, payload) {
      if (!projectId) {
        alertPanel.replaceChildren(panelHead('Alerts', 'Failure Alerts', []), empty('选择一个项目后可查看失败告警。'));
        return;
      }
      const list = el('div', 'item-list');
      (payload.items || []).forEach((item) => {
        const entry = el('div', 'item ' + statusClass(item.severity));
        entry.appendChild(panelHead(item.severity || 'Alert', item.type || '-', [item.resourceId || '-', formatTime(item.timestamp)]));
        entry.appendChild(el('div', 'statline', item.message || '-'));
        list.appendChild(entry);
      });
      alertPanel.replaceChildren(panelHead('Alerts', 'Failure Alerts', ['Project ' + projectId, 'Count ' + (payload.count || 0)]), list.childElementCount ? list : empty('No active alerts'));
    }

    function renderConflictQueue(projectId, payload) {
      if (!projectId) {
        conflictPanel.replaceChildren(panelHead('HITL', 'HITL Conflicts', []), empty('选择一个项目后可查看 HITL 冲突队列。'));
        return;
      }
      const list = el('div', 'item-list');
      let openCount = 0;
      (payload.items || []).forEach((item) => {
        const status = item.status || 'OPEN';
        const isOpen = String(status).toUpperCase() === 'OPEN';
        if (isOpen) openCount += 1;
        const owner = item.requestedOwner || item.currentOwner ? (item.requestedOwner || '-') + ' → ' + (item.currentOwner || '-') : 'owner -';
        const resource = item.taskId || item.resourceId || '-';
        const entry = el('div', 'item ' + statusClass(status));
        const pills = [status, owner, resource, 'Created ' + formatTime(item.createdAt)];
        if (item.resolvedAt) pills.push('Resolved ' + formatTime(item.resolvedAt));
        entry.appendChild(panelHead(item.kind || 'Conflict', item.scope || '-', pills));
        entry.appendChild(el('div', 'statline', item.reason || '-'));
        if (isOpen && item.id) {
          const button = el('button', 'inline-action', 'Resolve conflict');
          button.type = 'button';
          button.addEventListener('click', async function() {
            button.disabled = true;
            button.textContent = 'Resolving...';
            try {
              await postJSON('/projects/' + encodeURIComponent(projectId) + '/conflicts/' + encodeURIComponent(item.id) + '/resolve', { resolutionNote: 'Resolved from status panel' });
              await loadDashboard();
            } catch (err) {
              button.disabled = false;
              button.textContent = 'Resolve conflict';
              entry.appendChild(el('div', 'statline error', err.message || String(err)));
            }
          });
          entry.appendChild(button);
        }
        if (item.resolvedBy || item.resolutionNote) {
          entry.appendChild(el('div', 'statline', 'Resolved by ' + (item.resolvedBy || '-') + ' · ' + (item.resolutionNote || '-')));
        }
        list.appendChild(entry);
      });
      conflictPanel.replaceChildren(panelHead('HITL', 'HITL Conflicts', ['Project ' + projectId, 'Open ' + openCount, 'Count ' + (payload.count || 0)]), list.childElementCount ? list : empty('No HITL conflicts queued'));
    }

    function renderAuditLogs(projectId, payload) {
      if (!projectId) {
        auditPanel.replaceChildren(panelHead('Audit', 'Audit Trail', []), empty('选择一个项目后可查看关键操作审计。'));
        return;
      }
      const list = el('div', 'item-list');
      (payload.items || []).forEach((item) => {
        const entry = el('div', 'item');
        entry.appendChild(panelHead(item.actor || 'actor', item.action || '-', [item.resourceType || '-', item.resourceId || '-', formatTime(item.timestamp)]));
        entry.appendChild(el('div', 'statline', item.summary || '-'));
        list.appendChild(entry);
      });
      auditPanel.replaceChildren(panelHead('Audit', 'Audit Trail', ['Project ' + projectId, 'Count ' + (payload.count || 0)]), list.childElementCount ? list : empty('No audit events recorded'));
    }

    function renderCommunications(projectId, payload) {
      if (!projectId) {
        commPanel.replaceChildren(panelHead('Communications', 'Agent Message Log', []), empty('选择一个项目后可查看内部通信日志。'));
        return;
      }
      const tableNode = table(['Version', 'From', 'To', 'Type', 'Task', 'Checksum', 'Timestamp']);
      const body = tableNode.querySelector('tbody');
      (payload.items || []).forEach((item) => {
        const row = el('tr', taskLogFilter.value.trim() && item.taskId === taskLogFilter.value.trim() ? 'task-row highlight' : 'task-row');
        appendCells(row, [item.version || '-', item.from || '-', item.to || '-', item.type || '-', item.taskId || '-', item.checksum || '-', formatTime(item.timestamp)]);
        body.appendChild(row);
      });
      if (!(payload.items || []).length) appendEmptyRow(body, 7, 'No communication log entries');
      const pills = ['Project ' + projectId, 'Count ' + (payload.count || 0), 'Limit ' + logLimit.value];
      if (taskLogFilter.value.trim()) pills.push('Task ' + taskLogFilter.value.trim());
      commPanel.replaceChildren(panelHead('Communications', 'Agent Message Log', pills), tableNode);
    }

    function renderCosts(projectId, payload) {
      if (!projectId) {
        costPanel.replaceChildren(panelHead('Token Cost', 'Token Cost Trend', []), empty('选择一个项目后可查看 Token 与成本趋势。'));
        return;
      }
      const maxTokens = Math.max(payload.maxTokens || 0, 1);
      const list = el('div', 'item-list');
      (payload.points || []).forEach((item) => {
        const width = Math.max(8, Math.min(100, Math.round(((item.totalTokens || 0) / maxTokens) * 100)));
        const entry = el('div', taskLogFilter.value.trim() && item.taskId === taskLogFilter.value.trim() ? 'item highlight' : 'item');
        entry.appendChild(panelHead(item.status || 'Run', item.taskName || item.taskId || '-', [item.agentType || '-', String(item.totalTokens || 0) + ' tok', '$' + Number(item.estimatedCostUsd || 0).toFixed(4)]));
        const track = el('div', 'bar-track');
        const fill = el('div', 'bar-fill');
        fill.style.width = width + '%%';
        track.appendChild(fill);
        entry.appendChild(track);
        entry.appendChild(el('div', 'statline', 'Prompt ' + (item.promptTokens || 0) + ' · Completion ' + (item.completionTokens || 0) + ' · Run ' + (item.runId || '-') + ' · ' + formatTime(item.timestamp)));
        list.appendChild(entry);
      });
      const pills = ['Project ' + projectId, 'Total ' + (payload.totalTokens || 0) + ' tok', 'Cost $' + Number(payload.estimatedCostUsd || 0).toFixed(4), 'Budget ' + (payload.budgetStatus || 'ok')];
      if (payload.budgetWarnUsd) pills.push('Warn $' + Number(payload.budgetWarnUsd).toFixed(2));
      if (payload.budgetBlockUsd) pills.push('Block $' + Number(payload.budgetBlockUsd).toFixed(2));
      costPanel.replaceChildren(panelHead('Token Cost', 'Token Cost Trend', pills), list.childElementCount ? list : empty('No token usage recorded yet'));
    }

    function renderSandboxes(projectId, payload) {
      if (!projectId) {
        sandboxPanel.replaceChildren(panelHead('Runtime', 'Sandboxes', []), empty('选择一个项目后可查看私有沙盒。'));
        return;
      }
      const list = el('div', 'item-list');
      (payload.items || []).forEach((item) => {
        const sandbox = item.sandbox || {};
        const task = item.task || {};
        const entry = el('div', 'item ' + statusClass(sandbox.status));
        entry.appendChild(panelHead(sandbox.scope || 'Sandbox', sandbox.id || '-', [sandbox.status || '-', sandbox.agentType || '-', task.name || sandbox.taskId || '-']));
        entry.appendChild(el('div', 'statline', 'Run ' + (sandbox.runId || '-') + ' · Task ' + (sandbox.taskId || '-') + ' · Updated ' + formatTime(sandbox.updatedAt)));
        if (sandbox.failureReason) entry.appendChild(el('div', 'statline error', sandbox.failureReason));
        list.appendChild(entry);
      });
      sandboxPanel.replaceChildren(panelHead('Runtime', 'Sandboxes', ['Project ' + projectId, 'Count ' + (payload.count || 0)]), list.childElementCount ? list : empty('No sandboxes recorded'));
    }

    function renderSnapshots(projectId, payload) {
      if (!projectId) {
        snapshotPanel.replaceChildren(panelHead('Timeline', 'Snapshots', []), empty('选择一个项目后可查看时间线快照。'));
        return;
      }
      const list = el('div', 'item-list');
      (payload.items || []).forEach((item) => {
        const entry = el('div', item.stable ? 'item OK' : 'item');
        entry.appendChild(panelHead(item.branch || 'branch', item.id || '-', [item.stable ? 'stable' : 'unstable', item.checksum ? 'checksum present' : 'no checksum', formatTime(item.createdAt)]));
        entry.appendChild(el('div', 'statline', (item.reason || '-') + ' · Source ' + (item.sourceSnapshotId || '-')));
        list.appendChild(entry);
      });
      snapshotPanel.replaceChildren(panelHead('Timeline', 'Snapshots', ['Project ' + projectId, 'Count ' + (payload.count || 0)]), list.childElementCount ? list : empty('No snapshots recorded'));
    }

    async function renderPanelFetch(panel, title, request, render) {
      try {
        const payload = await request();
        render(payload);
        return payload;
      } catch (err) {
        renderError(panel, title, err);
        return null;
      }
    }

    async function loadProjectPanels(projectId) {
      if (!projectId) {
        renderAlerts('', { items: [], count: 0 });
        renderConflictQueue('', { items: [], count: 0 });
        renderAuditLogs('', { items: [], count: 0 });
        renderCommunications('', { items: [], count: 0 });
        renderCosts('', { points: [], totalTokens: 0, estimatedCostUsd: 0, maxTokens: 0 });
        renderSandboxes('', { items: [], count: 0 });
        renderSnapshots('', { items: [], count: 0 });
        return { costs: null, communications: null };
      }
      const taskId = taskLogFilter.value.trim();
      const limit = encodeURIComponent(logLimit.value || '25');
      const scopedTask = taskId ? '&taskId=' + encodeURIComponent(taskId) : '';
      const costPath = '/projects/' + encodeURIComponent(projectId) + '/token-costs' + (taskId ? '?taskId=' + encodeURIComponent(taskId) : '');
      const [, , , communications, costs] = await Promise.all([
        renderPanelFetch(alertPanel, 'Failure Alerts', () => fetchJSON('/projects/' + encodeURIComponent(projectId) + '/alerts?limit=' + limit), (payload) => renderAlerts(projectId, payload)),
        renderPanelFetch(conflictPanel, 'HITL Conflicts', () => fetchJSON('/projects/' + encodeURIComponent(projectId) + '/conflicts'), (payload) => renderConflictQueue(projectId, payload)),
        renderPanelFetch(auditPanel, 'Audit Trail', () => fetchJSON('/projects/' + encodeURIComponent(projectId) + '/audit-logs?limit=' + limit), (payload) => renderAuditLogs(projectId, payload)),
        renderPanelFetch(commPanel, 'Agent Message Log', () => fetchJSON('/projects/' + encodeURIComponent(projectId) + '/communications?limit=' + limit + scopedTask), (payload) => renderCommunications(projectId, payload)),
        renderPanelFetch(costPanel, 'Token Cost Trend', () => fetchJSON(costPath), (payload) => renderCosts(projectId, payload)),
        renderPanelFetch(sandboxPanel, 'Sandboxes', () => fetchJSON('/projects/' + encodeURIComponent(projectId) + '/sandboxes'), (payload) => renderSandboxes(projectId, payload)),
        renderPanelFetch(snapshotPanel, 'Snapshots', () => fetchJSON('/projects/' + encodeURIComponent(projectId) + '/snapshots'), (payload) => renderSnapshots(projectId, payload))
      ]);
      return { costs, communications };
    }

    async function loadDashboard() {
      try {
        const readiness = await fetchJSON('/ready');
        renderReadiness(readiness);
      } catch (err) {
        renderError(readinessPanel, 'Readiness', err);
      }

      try {
        const value = filter.value ? '?projectId=' + encodeURIComponent(filter.value) : '';
        const view = await fetchJSON('/status/matrix' + value);
        renderMatrix(view);
        const projectId = filter.value || '';
        const panels = await loadProjectPanels(projectId);
        renderTopology(view, panels ? panels.communications : null);
        renderKPIs(view, panels ? panels.costs : null);
      } catch (err) {
        renderError(matrixPanel, 'Status Matrix', err);
      }
    }

    filter.addEventListener('change', loadDashboard);
    taskLogFilter.addEventListener('change', loadDashboard);
    logLimit.addEventListener('change', loadDashboard);
    refreshBtn.addEventListener('click', loadDashboard);

    if (typeof window.EventSource === 'function') {
      const stream = new EventSource(withAuth('/status/stream'));
      stream.onopen = function() { streamState.textContent = 'SSE connected'; };
      stream.addEventListener('status', function() {
        loadDashboard();
      });
      stream.onerror = function() {
        streamState.textContent = 'SSE reconnecting; polling fallback active';
      };
    } else {
      streamState.textContent = 'SSE unavailable; polling fallback active';
    }

    loadDashboard();
    setInterval(loadDashboard, 4000);
  </script>
</body>
</html>`, serviceName, serviceName)
}
