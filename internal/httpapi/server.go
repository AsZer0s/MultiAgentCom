package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	mux.HandleFunc("POST /projects/{id}/tasks/{taskId}/sandbox/fail", server.handleInjectSandboxFailure)
	mux.HandleFunc("POST /projects/{id}/tasks/run", server.handleStartRun)
	mux.HandleFunc("POST /projects/{id}/tasks/{taskId}/retry", server.handleRetryTask)
	mux.HandleFunc("POST /projects/{id}/runs/parallel", server.handleStartParallelRun)
	mux.HandleFunc("GET /projects/{id}/runs/{runId}/status", server.handleGetRunStatus)
	mux.HandleFunc("GET /projects/{id}/runs/{runId}/sandbox", server.handleGetRunSandbox)
	mux.HandleFunc("GET /projects/{id}/sandboxes", server.handleListSandboxes)
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
      <button id="refreshBtn" type="button">Refresh Now</button>
      <div id="generatedAt" class="pill">loading...</div>
    </div>
    <div id="app" class="grid"></div>
  </div>
  <script>
    const filter = document.getElementById("projectFilter");
    const refreshBtn = document.getElementById("refreshBtn");
    const app = document.getElementById("app");
    const generatedAt = document.getElementById("generatedAt");

    function statusBadge(status) {
      return '<span class="status ' + status + '"><span class="dot"></span>' + status + '</span>';
    }

    function renderMatrix(view) {
      const selected = view.selectedProjectId || "";
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
          return '<tr>'
            + '<td>' + task.name + '</td>'
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

    async function load() {
      const value = filter.value ? '?projectId=' + encodeURIComponent(filter.value) : '';
      const response = await fetch('/status/matrix' + value, { headers: { 'Accept': 'application/json' } });
      const data = await response.json();
      renderMatrix(data);
    }

    filter.addEventListener('change', load);
    refreshBtn.addEventListener('click', load);
    load();
    setInterval(load, 4000);
  </script>
</body>
</html>`, serviceName)
}
