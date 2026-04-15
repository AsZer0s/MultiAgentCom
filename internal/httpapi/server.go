package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/service"
)

type Server struct {
	cfg    config.Config
	logger *slog.Logger
	svc    *service.Service
}

func NewServer(cfg config.Config, logger *slog.Logger, svc *service.Service) http.Handler {
	server := &Server{
		cfg:    cfg,
		logger: logger,
		svc:    svc,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", server.handleHealth)
	mux.HandleFunc("GET /status/matrix", server.handleGetStatusMatrix)
	mux.HandleFunc("GET /status/panel", server.handleStatusPanel)
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
	mux.HandleFunc("POST /projects/{id}/shared-sandbox/merge", server.handleMergeSharedSandbox)
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

	return withMiddleware(cfg, logger, mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   s.cfg.ServiceName,
		"timestamp": time.Now().UTC(),
	})
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

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
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
	items, err := s.svc.ListCommunicationLogs(r.Context(), r.PathValue("id"), r.URL.Query().Get("taskId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
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
	items, err := s.svc.ListAuditLogs(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
	})
}

func (s *Server) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ListAlerts(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"count": len(items),
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
	var input service.ApplyHumanOverrideInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.ApplyHumanOverride(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleApplyCodeLock(w http.ResponseWriter, r *http.Request) {
	var input service.ApplyCodeLockInput
	if err := decodeJSON(r, &input); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.svc.ApplyCodeLock(r.Context(), r.PathValue("id"), input)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) handleMergeSharedSandbox(w http.ResponseWriter, r *http.Request) {
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
	artifact, err := s.svc.GetArtifact(r.Context(), r.PathValue("id"), r.PathValue("artifactId"))
	if err != nil {
		writeServiceError(w, err)
		return
	}

	if _, err := os.Stat(artifact.URI); err != nil {
		writeServiceError(w, &service.AppError{Code: "ARTIFACT_MISSING", StatusCode: http.StatusInternalServerError, Message: "artifact file is missing"})
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

	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
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

func withMiddleware(cfg config.Config, logger *slog.Logger, next http.Handler) http.Handler {
	return requestIDMiddleware(authMiddleware(cfg)(recoveryMiddleware(logger)(loggingMiddleware(logger)(next))))
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

func loggingMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			writer := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(writer, r)

			logger.Info("http_request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", writer.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"actor", actorFromRequest(r),
				"requestId", requestIDFromContext(r.Context()),
			)
		})
	}
}

func authMiddleware(cfg config.Config) func(http.Handler) http.Handler {
	requiredToken := strings.TrimSpace(cfg.APIToken)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requiredToken == "" || r.URL.Path == "/health" {
				next.ServeHTTP(w, r)
				return
			}

			token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if token == "" {
				token = strings.TrimSpace(r.Header.Get("X-API-Key"))
			}
			if token == "" {
				token = strings.TrimSpace(r.URL.Query().Get("token"))
			}
			if token != requiredToken {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"code":    "UNAUTHORIZED",
					"message": "missing or invalid api token",
				})
				return
			}

			actor := strings.TrimSpace(r.Header.Get("X-Actor"))
			if actor == "" {
				actor = "api-token"
			}
			ctx := context.WithValue(r.Context(), authActorKey, actor)
			ctx = service.WithActor(ctx, actor)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
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
  <title>%s Status Matrix</title>
  <style>
    :root {
      --bg: #f4efe7;
      --card: rgba(255, 252, 247, 0.92);
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
    .shell {
      max-width: 1200px;
      margin: 0 auto;
      padding: 32px 20px 48px;
    }
    .hero {
      display: grid;
      gap: 12px;
      margin-bottom: 24px;
    }
    .eyebrow {
      font-size: 12px;
      letter-spacing: 0.18em;
      text-transform: uppercase;
      color: var(--muted);
    }
    h1 {
      margin: 0;
      font-size: clamp(32px, 5vw, 52px);
      line-height: 0.94;
    }
    .sub {
      max-width: 680px;
      color: var(--muted);
      font-size: 16px;
      line-height: 1.5;
    }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
      align-items: center;
      margin: 20px 0 28px;
    }
    .toolbar select,
    .toolbar input,
    .toolbar button {
      border: 1px solid var(--line);
      border-radius: 999px;
      background: var(--card);
      color: var(--ink);
      padding: 10px 16px;
      font: inherit;
    }
    .toolbar button {
      background: var(--ink);
      color: #fff;
      cursor: pointer;
    }
    .grid {
      display: grid;
      gap: 18px;
    }
    .panel {
      background: var(--card);
      backdrop-filter: blur(14px);
      border: 1px solid rgba(214, 202, 184, 0.8);
      border-radius: 24px;
      box-shadow: 0 18px 60px rgba(31, 27, 22, 0.08);
      padding: 20px;
    }
    .project-head {
      display: flex;
      flex-wrap: wrap;
      justify-content: space-between;
      gap: 12px;
      margin-bottom: 14px;
    }
    .meta {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .pill {
      border-radius: 999px;
      padding: 6px 10px;
      background: #fff;
      border: 1px solid var(--line);
      font-size: 12px;
      color: var(--muted);
    }
    .status {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      font-weight: 600;
    }
    .dot {
      width: 10px;
      height: 10px;
      border-radius: 999px;
    }
    .RUNNING .dot { background: var(--accent); }
    .READY .dot { background: var(--warn); }
    .BLOCKED .dot { background: var(--bad); }
    .HUMAN_OVERRIDE .dot { background: var(--hold); }
    .COMPLETED .dot { background: var(--ok); }
    .IDLE .dot { background: var(--idle); }
    table {
      width: 100%%;
      border-collapse: collapse;
      margin-top: 12px;
    }
    th, td {
      text-align: left;
      padding: 12px 10px;
      border-top: 1px solid rgba(214, 202, 184, 0.7);
      vertical-align: top;
      font-size: 14px;
    }
    th {
      color: var(--muted);
      font-weight: 600;
    }
    .task-meta {
      margin-top: 4px;
      color: var(--muted);
      font-size: 12px;
      word-break: break-all;
    }
    .task-row.highlight td {
      background: var(--accent-soft);
    }
    .trend-list {
      display: grid;
      gap: 12px;
      margin-top: 12px;
    }
    .alert-list {
      display: grid;
      gap: 12px;
      margin-top: 12px;
    }
    .alert-item {
      border: 1px solid rgba(214, 202, 184, 0.7);
      border-radius: 18px;
      padding: 12px 14px;
      background: rgba(255,255,255,0.6);
    }
    .alert-item.CRITICAL {
      border-color: rgba(180, 35, 24, 0.45);
      box-shadow: inset 0 0 0 1px rgba(180, 35, 24, 0.12);
    }
    .alert-item.ERROR {
      border-color: rgba(183, 121, 31, 0.45);
      box-shadow: inset 0 0 0 1px rgba(183, 121, 31, 0.12);
    }
    .trend-row {
      border: 1px solid rgba(214, 202, 184, 0.7);
      border-radius: 18px;
      padding: 12px 14px;
      background: rgba(255,255,255,0.6);
    }
    .trend-row.highlight {
      border-color: rgba(22, 93, 255, 0.4);
      box-shadow: inset 0 0 0 1px rgba(22, 93, 255, 0.16);
    }
    .trend-head {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      align-items: baseline;
      margin-bottom: 8px;
    }
    .trend-head strong {
      display: block;
    }
    .bar-track {
      width: 100%%;
      height: 10px;
      border-radius: 999px;
      background: rgba(22, 93, 255, 0.12);
      overflow: hidden;
    }
    .bar-fill {
      height: 100%%;
      border-radius: 999px;
      background: linear-gradient(90deg, #165dff 0%%, #0f8b4c 100%%);
    }
    .statline {
      margin-top: 8px;
      color: var(--muted);
      font-size: 12px;
    }
    .empty {
      padding: 28px;
      border: 1px dashed var(--line);
      border-radius: 20px;
      color: var(--muted);
      text-align: center;
      background: rgba(255,255,255,0.5);
    }
    .foot {
      margin-top: 18px;
      color: var(--muted);
      font-size: 12px;
    }
    @media (max-width: 760px) {
      .panel { padding: 16px; }
      th:nth-child(3), td:nth-child(3),
      th:nth-child(5), td:nth-child(5) { display: none; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <div class="hero">
      <div class="eyebrow">Operational View</div>
      <h1>Status Matrix</h1>
      <div class="sub">查看每个项目下任务和 Agent 的当前状态。这个最小版面板会自动刷新，适合演示 Sprint 2 的协同编排进度。</div>
    </div>
    <div class="toolbar">
      <select id="projectFilter"></select>
      <input id="taskLogFilter" type="text" placeholder="Filter communications by taskId">
      <button id="refreshBtn" type="button">Refresh Now</button>
      <div id="generatedAt" class="pill">loading...</div>
    </div>
    <div id="app" class="grid"></div>
    <div id="alertPanel" class="panel" style="margin-top:18px;"></div>
    <div id="commPanel" class="panel" style="margin-top:18px;"></div>
    <div id="costPanel" class="panel" style="margin-top:18px;"></div>
  </div>
  <script>
    const authQuery = new URLSearchParams(window.location.search).get("token");
    const filter = document.getElementById("projectFilter");
    const taskLogFilter = document.getElementById("taskLogFilter");
    const refreshBtn = document.getElementById("refreshBtn");
    const app = document.getElementById("app");
    const generatedAt = document.getElementById("generatedAt");
    const alertPanel = document.getElementById("alertPanel");
    const commPanel = document.getElementById("commPanel");
    const costPanel = document.getElementById("costPanel");

    function statusBadge(status) {
      return '<span class="status ' + status + '"><span class="dot"></span>' + status + '</span>';
    }

    function withAuth(path) {
      if (!authQuery) return path;
      return path + (path.indexOf('?') >= 0 ? '&' : '?') + 'token=' + encodeURIComponent(authQuery);
    }

    function renderMatrix(view) {
      const selected = view.selectedProjectId || "";
      const selectedTaskId = taskLogFilter.value.trim();
      const options = ['<option value="">All Projects</option>']
        .concat((view.projects || []).map(project =>
          '<option value="' + project.id + '"' + (project.id === selected ? ' selected' : '') + '>' + project.name + '</option>'
        ));
      filter.innerHTML = options.join("");
      generatedAt.textContent = view.generatedAt ? 'Generated at ' + new Date(view.generatedAt).toLocaleString() : 'No data';

      if (!view.matrices || view.matrices.length === 0) {
        app.innerHTML = '<div class="empty">还没有可展示的项目状态。先创建项目并派发任务，再回来查看。</div>';
        return;
      }

      app.innerHTML = view.matrices.map(function(matrix) {
        const project = matrix.project;
        const agentRows = (matrix.agentMatrix || []).map(function(agent) {
          return '<tr>'
            + '<td>' + agent.agent + '</td>'
            + '<td>' + statusBadge(agent.status) + '</td>'
            + '<td>' + agent.totalTasks + '</td>'
            + '<td>' + agent.runningTasks + '</td>'
            + '<td>' + agent.doneTasks + '</td>'
            + '<td>' + agent.failedTasks + '</td>'
            + '</tr>';
        }).join("");

        const taskRows = (matrix.taskMatrix || []).map(function(task) {
          const rowClass = selectedTaskId && task.id === selectedTaskId ? ' class="task-row highlight"' : ' class="task-row"';
          return '<tr' + rowClass + '>'
            + '<td>' + task.name + '<div class="task-meta">' + task.id + '</div></td>'
            + '<td>' + (task.assigneeAgent || '-') + '</td>'
            + '<td>' + task.type + '</td>'
            + '<td>' + task.status + '</td>'
            + '<td>' + (task.latestRunStatus || '-') + '</td>'
            + '<td>' + ((task.dependsOn || []).join(', ') || '-') + '</td>'
            + '</tr>';
        }).join("");

        return '<section class="panel">'
          + '<div class="project-head">'
          +   '<div>'
          +     '<div class="eyebrow">Project</div>'
          +     '<h2 style="margin:6px 0 0;">' + project.name + '</h2>'
          +   '</div>'
          +   '<div class="meta">'
          +     '<span class="pill">Ready ' + matrix.readyTasks + '</span>'
          +     '<span class="pill">Running ' + matrix.runningTasks + '</span>'
          +     '<span class="pill">Done ' + matrix.completedTasks + '</span>'
          +     '<span class="pill">Failed ' + matrix.failedTasks + '</span>'
          +   '</div>'
          + '</div>'
          + '<table>'
          +   '<thead><tr><th>Agent</th><th>Status</th><th>Total</th><th>Running</th><th>Done</th><th>Failed</th></tr></thead>'
          +   '<tbody>' + (agentRows || '<tr><td colspan="6">No agents</td></tr>') + '</tbody>'
          + '</table>'
          + '<table>'
          +   '<thead><tr><th>Task</th><th>Agent</th><th>Type</th><th>Status</th><th>Latest Run</th><th>Depends On</th></tr></thead>'
          +   '<tbody>' + (taskRows || '<tr><td colspan="6">No tasks</td></tr>') + '</tbody>'
          + '</table>'
          + '</section>';
      }).join("");
    }

    function renderCommunications(projectId, payload) {
      if (!projectId) {
        commPanel.innerHTML = '<div class="empty">选择一个项目后可查看内部通信日志。</div>';
        return;
      }

      const rows = (payload.items || []).map(function(item) {
        return '<tr>'
          + '<td>' + item.version + '</td>'
          + '<td>' + item.from + '</td>'
          + '<td>' + item.to + '</td>'
          + '<td>' + item.type + '</td>'
          + '<td>' + item.taskId + '</td>'
          + '<td>' + item.checksum + '</td>'
          + '</tr>';
      }).join("");

      commPanel.innerHTML = '<div class="project-head">'
        + '<div><div class="eyebrow">Communications</div><h2 style="margin:6px 0 0;">Agent Message Log</h2></div>'
        + '<div class="meta"><span class="pill">Project ' + projectId + '</span><span class="pill">Count ' + (payload.count || 0) + '</span>' + (taskLogFilter.value.trim() ? ('<span class="pill">Task ' + taskLogFilter.value.trim() + '</span>') : '') + '</div>'
        + '</div>'
        + '<table>'
        + '<thead><tr><th>Version</th><th>From</th><th>To</th><th>Type</th><th>Task</th><th>Checksum</th></tr></thead>'
        + '<tbody>' + (rows || '<tr><td colspan="6">No communication log entries</td></tr>') + '</tbody>'
        + '</table>';
    }

    function renderAlerts(projectId, payload) {
      if (!projectId) {
        alertPanel.innerHTML = '<div class="empty">选择一个项目后可查看失败告警。</div>';
        return;
      }

      const rows = (payload.items || []).map(function(item) {
        return '<div class="alert-item ' + item.severity + '">'
          + '<div class="trend-head"><div><strong>' + item.type + '</strong><div class="task-meta">' + item.resourceId + '</div></div><strong>' + item.severity + '</strong></div>'
          + '<div class="statline">' + item.message + '</div>'
          + '</div>';
      }).join("");

      alertPanel.innerHTML = '<div class="project-head">'
        + '<div><div class="eyebrow">Alerts</div><h2 style="margin:6px 0 0;">Failure Alerts</h2></div>'
        + '<div class="meta"><span class="pill">Project ' + projectId + '</span><span class="pill">Count ' + (payload.count || 0) + '</span></div>'
        + '</div>'
        + ((payload.items || []).length ? ('<div class="alert-list">' + rows + '</div>') : '<div class="empty">No active alerts</div>');
    }

    function renderCosts(projectId, payload) {
      if (!projectId) {
        costPanel.innerHTML = '<div class="empty">选择一个项目后可查看 Token 与成本趋势。</div>';
        return;
      }

      const maxTokens = Math.max(payload.maxTokens || 0, 1);
      const rows = (payload.points || []).map(function(item) {
        const width = Math.max(8, Math.round((item.totalTokens / maxTokens) * 100));
        const highlight = taskLogFilter.value.trim() && item.taskId === taskLogFilter.value.trim() ? ' highlight' : '';
        return '<div class="trend-row' + highlight + '">'
          + '<div class="trend-head"><div><strong>' + item.taskName + '</strong><div class="task-meta">' + item.taskId + ' · ' + item.agentType + '</div></div><strong>' + item.totalTokens + ' tok</strong></div>'
          + '<div class="bar-track"><div class="bar-fill" style="width:' + width + '%%;"></div></div>'
          + '<div class="statline">Prompt ' + item.promptTokens + ' · Completion ' + item.completionTokens + ' · Cost $' + Number(item.estimatedCostUsd || 0).toFixed(4) + '</div>'
          + '</div>';
      }).join("");

      costPanel.innerHTML = '<div class="project-head">'
        + '<div><div class="eyebrow">Token Cost</div><h2 style="margin:6px 0 0;">Token Cost Trend</h2></div>'
        + '<div class="meta"><span class="pill">Project ' + projectId + '</span><span class="pill">Total ' + (payload.totalTokens || 0) + ' tok</span><span class="pill">Cost $' + Number(payload.estimatedCostUsd || 0).toFixed(4) + '</span></div>'
        + '</div>'
        + ((payload.points || []).length ? ('<div class="trend-list">' + rows + '</div>') : '<div class="empty">No token usage recorded yet</div>');
    }

    async function load() {
      const value = filter.value ? '?projectId=' + encodeURIComponent(filter.value) : '';
      const response = await fetch(withAuth('/status/matrix' + value), { headers: { 'Accept': 'application/json' } });
      const data = await response.json();
      renderMatrix(data);

      const projectId = filter.value || '';
      if (!projectId) {
        renderAlerts('', { items: [], count: 0 });
        renderCommunications('', { items: [], count: 0 });
        renderCosts('', { points: [], totalTokens: 0, estimatedCostUsd: 0, maxTokens: 0 });
        return;
      }
      const taskId = taskLogFilter.value.trim();
      const alertURL = '/projects/' + encodeURIComponent(projectId) + '/alerts';
      const alertResponse = await fetch(withAuth(alertURL), { headers: { 'Accept': 'application/json' } });
      const alertData = await alertResponse.json();
      renderAlerts(projectId, alertData);

      const commURL = '/projects/' + encodeURIComponent(projectId) + '/communications' + (taskId ? ('?taskId=' + encodeURIComponent(taskId)) : '');
      const commResponse = await fetch(withAuth(commURL), { headers: { 'Accept': 'application/json' } });
      const commData = await commResponse.json();
      renderCommunications(projectId, commData);

      const costURL = '/projects/' + encodeURIComponent(projectId) + '/token-costs' + (taskId ? ('?taskId=' + encodeURIComponent(taskId)) : '');
      const costResponse = await fetch(withAuth(costURL), { headers: { 'Accept': 'application/json' } });
      const costData = await costResponse.json();
      renderCosts(projectId, costData);
    }

    filter.addEventListener('change', load);
    taskLogFilter.addEventListener('change', load);
    refreshBtn.addEventListener('click', load);
    load();
    setInterval(load, 4000);
  </script>
</body>
</html>`, serviceName)
}
