package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"multiagentcom/internal/config"
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
	mux.HandleFunc("POST /projects/{id}/tasks/run", server.handleStartRun)
	mux.HandleFunc("POST /projects/{id}/tasks/{taskId}/retry", server.handleRetryTask)
	mux.HandleFunc("POST /projects/{id}/runs/parallel", server.handleStartParallelRun)
	mux.HandleFunc("GET /projects/{id}/runs/{runId}/status", server.handleGetRunStatus)
	mux.HandleFunc("POST /projects/{id}/delivery/export", server.handleExportDelivery)
	mux.HandleFunc("GET /projects/{id}/artifacts/{artifactId}/download", server.handleDownloadArtifact)

	return withMiddleware(logger, mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"service":   s.cfg.ServiceName,
		"timestamp": time.Now().UTC(),
	})
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

func withMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return requestIDMiddleware(recoveryMiddleware(logger)(loggingMiddleware(logger)(next)))
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
				"requestId", requestIDFromContext(r.Context()),
			)
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
