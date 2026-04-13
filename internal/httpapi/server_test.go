package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/service"
)

func TestHTTPFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name":        "Todo HTTP Demo",
		"description": "flow test",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 列表的增删改查",
		"content":     "实现 Todo 列表的增删改查，并导出最小交付包。",
		"constraints": []string{"后端使用 Go"},
	})

	planBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	var planResult service.PlanResult
	decodeResponse(t, planBody, &planResult)

	runBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/run", map[string]any{
		"taskId": planResult.Task.ID,
	})
	var runEnvelope service.RunEnvelope
	decodeResponse(t, runBody, &runEnvelope)

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/runs/"+runEnvelope.Run.ID+"/status")
		decodeResponse(t, statusBody, &status)
		if status.Run.Status == domain.RunStatusSucceeded {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if status.Run.Status != domain.RunStatusSucceeded {
		t.Fatalf("expected run to succeed, got %s", status.Run.Status)
	}

	exportBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/delivery/export", map[string]any{
		"runId": runEnvelope.Run.ID,
	})

	var exportResponse struct {
		Artifact     domain.Artifact `json:"artifact"`
		DownloadPath string          `json:"downloadPath"`
	}
	decodeResponse(t, exportBody, &exportResponse)

	resp, err := server.Client().Get(server.URL + exportResponse.DownloadPath)
	if err != nil {
		t.Fatalf("download artifact: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected download status 200, got %d", resp.StatusCode)
	}
}

func TestHTTPContractFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-contracts",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Contracts Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并生成契约。",
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})

	contractBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	var contract domain.Contract
	decodeResponse(t, contractBody, &contract)

	if contract.Version != 1 {
		t.Fatalf("expected contract version 1, got %d", contract.Version)
	}

	listBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/contracts")
	var listResponse struct {
		Items []domain.Contract `json:"items"`
		Count int               `json:"count"`
	}
	decodeResponse(t, listBody, &listResponse)

	if listResponse.Count != 1 || len(listResponse.Items) != 1 {
		t.Fatalf("expected one contract, got count=%d len=%d", listResponse.Count, len(listResponse.Items))
	}

	getBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/contracts/"+contract.ID)
	var stored domain.Contract
	decodeResponse(t, getBody, &stored)

	if len(stored.Endpoints) == 0 {
		t.Fatal("expected stored contract endpoints")
	}
	if stored.Endpoints[0].Method != http.MethodGet {
		t.Fatalf("expected first endpoint method GET, got %s", stored.Endpoints[0].Method)
	}
}

func TestHTTPContractValidationConflict(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-contract-validate",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-test-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Validate Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":   "实现 Todo 列表的增删改查",
		"content": "实现 Todo 列表的增删改查，并生成契约。",
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})

	contractBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	var contract domain.Contract
	decodeResponse(t, contractBody, &contract)

	validateBody := requestJSONExpectStatus(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/validate", map[string]any{
		"contractId": contract.ID,
		"endpoints": []map[string]any{
			{"name": "ListTodo", "method": "GET", "path": "/api/todos"},
		},
		"schemas": []map[string]any{
			{
				"name": "Todo",
				"fields": []map[string]any{
					{"name": "id", "type": "string", "required": true},
					{"name": "title", "type": "number", "required": true},
				},
			},
		},
	}, http.StatusConflict)

	var result service.ContractValidationResult
	decodeResponse(t, validateBody, &result)

	if result.Passed {
		t.Fatal("expected validation result to fail")
	}
	if len(result.Conflicts) == 0 {
		t.Fatal("expected conflicts in validation result")
	}
	if result.RemediationTask == nil {
		t.Fatal("expected remediation task in validation result")
	}
}

func TestHTTPParallelDispatchFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-parallel",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Parallel Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)

	if len(dispatchResult.Tasks) != 3 {
		t.Fatalf("expected 3 dispatched tasks, got %d", len(dispatchResult.Tasks))
	}

	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)

	if len(parallelResult.Started) != 2 {
		t.Fatalf("expected 2 started runs, got %d", len(parallelResult.Started))
	}

	for _, started := range parallelResult.Started {
		waitForHTTPRun(t, server, project.ID, started.Run.ID)
	}

	listBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/tasks")
	var taskList struct {
		Items []domain.Task `json:"items"`
		Count int           `json:"count"`
	}
	decodeResponse(t, listBody, &taskList)

	if taskList.Count < 4 {
		t.Fatalf("expected at least 4 tasks including sprint1 seed and dispatched tasks, got %d", taskList.Count)
	}
}

func TestHTTPTaskContextFlow(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-context",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Context Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})

	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)

	contextBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/"+dispatchResult.Tasks[0].ID+"/context/generate", map[string]any{})
	var generated service.TaskContextEnvelope
	decodeResponse(t, contextBody, &generated)

	if generated.Context.Version != 1 {
		t.Fatalf("expected context version 1, got %d", generated.Context.Version)
	}
	if generated.Context.Role != "backend" {
		t.Fatalf("expected backend role, got %s", generated.Context.Role)
	}

	latestBody := getJSON(t, server.Client(), server.URL+"/projects/"+project.ID+"/tasks/"+dispatchResult.Tasks[0].ID+"/context")
	var latest service.TaskContextEnvelope
	decodeResponse(t, latestBody, &latest)

	if latest.Context.ID != generated.Context.ID {
		t.Fatalf("expected latest context id %s, got %s", generated.Context.ID, latest.Context.ID)
	}
	if len(latest.Context.Sources) != 3 {
		t.Fatalf("expected 3 context sources, got %d", len(latest.Context.Sources))
	}
}

func TestHTTPStatusMatrixAndPanel(t *testing.T) {
	cfg := config.Config{
		Address:      ":0",
		ServiceName:  "test-http-status-panel",
		ArtifactRoot: t.TempDir(),
		DefaultAgent: "http-manager-agent",
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(cfg, logger)
	server := httptest.NewServer(NewServer(cfg, logger, svc))
	defer server.Close()

	projectBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects", map[string]any{
		"name": "Todo Status Demo",
	})

	var project domain.Project
	decodeResponse(t, projectBody, &project)

	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/requirements", map[string]any{
		"title":       "实现 Todo 全栈功能",
		"content":     "实现 Todo 全栈功能，后端提供 API，前端提供页面。",
		"constraints": []string{"后端使用 Go", "前端使用 Vue"},
	})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/plan", map[string]any{})
	requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/contracts/generate", map[string]any{})
	dispatchBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/tasks/dispatch", map[string]any{})
	var dispatchResult service.DispatchTasksResult
	decodeResponse(t, dispatchBody, &dispatchResult)
	parallelBody := requestJSON(t, server.Client(), http.MethodPost, server.URL+"/projects/"+project.ID+"/runs/parallel", map[string]any{
		"taskIds": []string{dispatchResult.Tasks[0].ID, dispatchResult.Tasks[1].ID},
	})
	var parallelResult service.ParallelRunResult
	decodeResponse(t, parallelBody, &parallelResult)
	for _, started := range parallelResult.Started {
		waitForHTTPRun(t, server, project.ID, started.Run.ID)
	}

	matrixBody := getJSON(t, server.Client(), server.URL+"/status/matrix?projectId="+project.ID)
	var matrix service.StatusMatrixView
	decodeResponse(t, matrixBody, &matrix)

	if len(matrix.Matrices) != 1 {
		t.Fatalf("expected 1 matrix, got %d", len(matrix.Matrices))
	}
	if matrix.SelectedProjectID != project.ID {
		t.Fatalf("expected selected project id %s, got %s", project.ID, matrix.SelectedProjectID)
	}

	resp, err := server.Client().Get(server.URL + "/status/panel")
	if err != nil {
		t.Fatalf("get status panel: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read status panel: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status panel 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Status Matrix") {
		t.Fatalf("expected status panel html to contain title, got %s", string(body))
	}
}

func requestJSON(t *testing.T, client *http.Client, method, url string, payload any) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode >= 300 {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(data))
	}

	return data
}

func requestJSONExpectStatus(t *testing.T, client *http.Client, method, url string, payload any, expectedStatus int) []byte {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode != expectedStatus {
		t.Fatalf("expected status %d, got %d: %s", expectedStatus, resp.StatusCode, string(data))
	}

	return data
}

func getJSON(t *testing.T, client *http.Client, url string) []byte {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	if resp.StatusCode >= 300 {
		t.Fatalf("unexpected status %d: %s", resp.StatusCode, string(data))
	}

	return data
}

func decodeResponse(t *testing.T, body []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, string(body))
	}
}

func waitForHTTPRun(t *testing.T, server *httptest.Server, projectID, runID string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	var status service.RunStatusView
	for time.Now().Before(deadline) {
		statusBody := getJSON(t, server.Client(), server.URL+"/projects/"+projectID+"/runs/"+runID+"/status")
		decodeResponse(t, statusBody, &status)
		if status.Run.Status == domain.RunStatusSucceeded {
			return
		}
		if status.Run.Status == domain.RunStatusFailed {
			t.Fatalf("expected successful run, got failure: %s", status.Run.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("run %s did not complete before deadline", runID)
}
