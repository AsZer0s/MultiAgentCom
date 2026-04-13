package service

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
)

type AppError struct {
	Code       string
	StatusCode int
	Message    string
}

func (e *AppError) Error() string {
	return e.Message
}

func newValidationError(message string) *AppError {
	return &AppError{Code: "VALIDATION_ERROR", StatusCode: 400, Message: message}
}

func newNotFoundError(message string) *AppError {
	return &AppError{Code: "NOT_FOUND", StatusCode: 404, Message: message}
}

func newConflictError(message string) *AppError {
	return &AppError{Code: "CONFLICT", StatusCode: 409, Message: message}
}

type Service struct {
	cfg    config.Config
	logger *slog.Logger

	mu            sync.RWMutex
	projects      map[string]*domain.Project
	requirements  map[string][]*domain.Requirement
	planIndex     map[string]*domain.Plan
	plans         map[string][]*domain.Plan
	contractIndex map[string]*domain.Contract
	contracts     map[string][]*domain.Contract
	contextIndex  map[string]*domain.ContextInjection
	contexts      map[string][]*domain.ContextInjection
	sandboxIndex  map[string]*domain.Sandbox
	sandboxes     map[string][]*domain.Sandbox
	sandboxFaults map[string]string
	tasks         map[string]*domain.Task
	taskOrder     map[string][]string
	runs          map[string]*domain.AgentRun
	runOrder      map[string][]string
	artifacts     map[string]*domain.Artifact
	artifactOrder map[string][]string
}

type CreateProjectInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AddRequirementInput struct {
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	Constraints     []string `json:"constraints"`
	AcceptanceHints []string `json:"acceptanceHints"`
}

type PlanResult struct {
	Plan domain.Plan `json:"plan"`
	Task domain.Task `json:"task"`
}

type StartRunInput struct {
	TaskID string `json:"taskId"`
}

type RunEnvelope struct {
	Task domain.Task     `json:"task"`
	Run  domain.AgentRun `json:"run"`
}

type RunStatusView struct {
	Run       domain.AgentRun   `json:"run"`
	Task      domain.Task       `json:"task"`
	Artifacts []domain.Artifact `json:"artifacts"`
}

type ExportDeliveryInput struct {
	RunID string `json:"runId"`
}

type ValidateContractInput struct {
	ContractID string                    `json:"contractId"`
	Endpoints  []domain.ContractEndpoint `json:"endpoints"`
	Schemas    []domain.ContractSchema   `json:"schemas"`
}

type ContractValidationConflict struct {
	Type     string `json:"type"`
	Location string `json:"location"`
	Message  string `json:"message"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

type ContractValidationResult struct {
	Contract        domain.Contract              `json:"contract"`
	Passed          bool                         `json:"passed"`
	Conflicts       []ContractValidationConflict `json:"conflicts"`
	RemediationTask *domain.Task                 `json:"remediationTask,omitempty"`
}

type DispatchTasksResult struct {
	Contract domain.Contract `json:"contract"`
	Tasks    []domain.Task   `json:"tasks"`
}

type ParallelRunInput struct {
	TaskIDs []string `json:"taskIds"`
}

type ParallelRunResult struct {
	BatchID      string        `json:"batchId"`
	Started      []RunEnvelope `json:"started"`
	BlockedTasks []domain.Task `json:"blockedTasks,omitempty"`
}

type TaskContextEnvelope struct {
	Task    domain.Task             `json:"task"`
	Context domain.ContextInjection `json:"context"`
}

type StatusMatrixAgent struct {
	Agent        string `json:"agent"`
	Status       string `json:"status"`
	CreatedTasks int    `json:"createdTasks"`
	RunningTasks int    `json:"runningTasks"`
	DoneTasks    int    `json:"doneTasks"`
	FailedTasks  int    `json:"failedTasks"`
	TotalTasks   int    `json:"totalTasks"`
}

type StatusMatrixTask struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Type            string            `json:"type"`
	AssigneeAgent   string            `json:"assigneeAgent"`
	Status          domain.TaskStatus `json:"status"`
	DependsOn       []string          `json:"dependsOn,omitempty"`
	LatestRunStatus domain.RunStatus  `json:"latestRunStatus,omitempty"`
}

type StatusMatrixProject struct {
	Project        domain.Project      `json:"project"`
	AgentMatrix    []StatusMatrixAgent `json:"agentMatrix"`
	TaskMatrix     []StatusMatrixTask  `json:"taskMatrix"`
	TotalTasks     int                 `json:"totalTasks"`
	ReadyTasks     int                 `json:"readyTasks"`
	RunningTasks   int                 `json:"runningTasks"`
	CompletedTasks int                 `json:"completedTasks"`
	FailedTasks    int                 `json:"failedTasks"`
}

type StatusMatrixView struct {
	Projects          []domain.Project      `json:"projects"`
	SelectedProjectID string                `json:"selectedProjectId,omitempty"`
	Matrices          []StatusMatrixProject `json:"matrices"`
	GeneratedAt       time.Time             `json:"generatedAt"`
}

type SandboxView struct {
	Sandbox domain.Sandbox  `json:"sandbox"`
	Run     domain.AgentRun `json:"run"`
	Task    domain.Task     `json:"task"`
}

type InjectSandboxFailureInput struct {
	Reason string `json:"reason"`
}

func New(cfg config.Config, logger *slog.Logger) *Service {
	if strings.TrimSpace(cfg.SandboxRoot) == "" {
		cfg.SandboxRoot = filepath.Join(os.TempDir(), "multiagentcom", "sandboxes")
	}

	return &Service{
		cfg:           cfg,
		logger:        logger,
		projects:      make(map[string]*domain.Project),
		requirements:  make(map[string][]*domain.Requirement),
		planIndex:     make(map[string]*domain.Plan),
		plans:         make(map[string][]*domain.Plan),
		contractIndex: make(map[string]*domain.Contract),
		contracts:     make(map[string][]*domain.Contract),
		contextIndex:  make(map[string]*domain.ContextInjection),
		contexts:      make(map[string][]*domain.ContextInjection),
		sandboxIndex:  make(map[string]*domain.Sandbox),
		sandboxes:     make(map[string][]*domain.Sandbox),
		sandboxFaults: make(map[string]string),
		tasks:         make(map[string]*domain.Task),
		taskOrder:     make(map[string][]string),
		runs:          make(map[string]*domain.AgentRun),
		runOrder:      make(map[string][]string),
		artifacts:     make(map[string]*domain.Artifact),
		artifactOrder: make(map[string][]string),
	}
}

func (s *Service) CreateProject(_ context.Context, input CreateProjectInput) (*domain.Project, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, newValidationError("project name is required")
	}

	now := time.Now().UTC()
	project := &domain.Project{
		ID:          nextID("proj"),
		Name:        name,
		Description: strings.TrimSpace(input.Description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.mu.Lock()
	s.projects[project.ID] = project
	s.mu.Unlock()

	return cloneProject(project), nil
}

func (s *Service) ListSandboxes(_ context.Context, projectID string) ([]SandboxView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.sandboxes[projectID]
	result := make([]SandboxView, 0, len(items))
	for _, sandbox := range items {
		run, ok := s.runs[sandbox.RunID]
		if !ok {
			continue
		}
		task, ok := s.tasks[sandbox.TaskID]
		if !ok {
			continue
		}
		result = append(result, SandboxView{
			Sandbox: *cloneSandbox(sandbox),
			Run:     *cloneRun(run),
			Task:    *cloneTask(task),
		})
	}

	return result, nil
}

func (s *Service) ListProjects(_ context.Context) ([]domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	projects := make([]domain.Project, 0, len(s.projects))
	for _, project := range s.projects {
		projects = append(projects, *cloneProject(project))
	}
	slices.SortFunc(projects, func(a, b domain.Project) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		default:
			return strings.Compare(a.ID, b.ID)
		}
	})

	return projects, nil
}

func (s *Service) GetProject(_ context.Context, projectID string) (*domain.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	return cloneProject(project), nil
}

func (s *Service) AddRequirement(_ context.Context, projectID string, input AddRequirementInput) (*domain.Requirement, error) {
	title := strings.TrimSpace(input.Title)
	content := strings.TrimSpace(input.Content)
	if title == "" {
		return nil, newValidationError("requirement title is required")
	}
	if content == "" {
		return nil, newValidationError("requirement content is required")
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	req := &domain.Requirement{
		ID:              nextID("req"),
		ProjectID:       projectID,
		Title:           title,
		Content:         content,
		Constraints:     compactStrings(input.Constraints),
		AcceptanceHints: compactStrings(input.AcceptanceHints),
		CreatedAt:       now,
	}

	s.requirements[projectID] = append(s.requirements[projectID], req)
	project.UpdatedAt = now

	return cloneRequirement(req), nil
}

func (s *Service) ListRequirements(_ context.Context, projectID string) ([]domain.Requirement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.requirements[projectID]
	result := make([]domain.Requirement, 0, len(items))
	for _, item := range items {
		result = append(result, *cloneRequirement(item))
	}

	return result, nil
}

func (s *Service) GeneratePlan(_ context.Context, projectID string) (*PlanResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	requirements := s.requirements[projectID]
	if len(requirements) == 0 {
		return nil, newValidationError("no requirement available for plan generation")
	}

	latestRequirement := requirements[len(requirements)-1]
	version := len(s.plans[projectID]) + 1
	plan := buildPlan(latestRequirement, version, now)
	task := domain.NewTask(
		nextID("task"),
		projectID,
		plan.ID,
		fmt.Sprintf("Implement %s", latestRequirement.Title),
		"SPRINT1_EXECUTE",
		s.cfg.DefaultAgent,
		nil,
		fmt.Sprintf("plan://%s", plan.ID),
		now,
	)

	s.planIndex[plan.ID] = plan
	s.plans[projectID] = append(s.plans[projectID], plan)
	s.tasks[task.ID] = task
	s.taskOrder[projectID] = append(s.taskOrder[projectID], task.ID)
	project.UpdatedAt = now

	return &PlanResult{
		Plan: *clonePlan(plan),
		Task: *cloneTask(task),
	}, nil
}

func (s *Service) GenerateContract(_ context.Context, projectID string) (*domain.Contract, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	plans := s.plans[projectID]
	if len(plans) == 0 {
		return nil, newValidationError("no plan available for contract generation")
	}

	plan := plans[len(plans)-1]
	requirement, err := resolveRequirementByID(s.requirements[projectID], plan.RequirementID)
	if err != nil {
		return nil, err
	}

	version := len(s.contracts[projectID]) + 1
	contract := buildContract(requirement, plan, version, now)
	s.contractIndex[contract.ID] = contract
	s.contracts[projectID] = append(s.contracts[projectID], contract)
	project.UpdatedAt = now

	return cloneContract(contract), nil
}

func (s *Service) ListContracts(_ context.Context, projectID string) ([]domain.Contract, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.contracts[projectID]
	result := make([]domain.Contract, 0, len(items))
	for _, item := range items {
		result = append(result, *cloneContract(item))
	}

	return result, nil
}

func (s *Service) GetContract(_ context.Context, projectID, contractID string) (*domain.Contract, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	contract, ok := s.contractIndex[contractID]
	if !ok || contract.ProjectID != projectID {
		return nil, newNotFoundError("contract not found")
	}

	return cloneContract(contract), nil
}

func (s *Service) GenerateTaskContext(_ context.Context, projectID, taskID string) (*TaskContextEnvelope, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	task, err := s.resolveTaskLocked(projectID, taskID)
	if err != nil {
		return nil, err
	}

	plan, ok := s.planIndex[task.PlanID]
	if !ok {
		return nil, newNotFoundError("plan not found for task")
	}

	contract, err := s.resolveTaskContractLocked(projectID, task)
	if err != nil {
		return nil, err
	}

	requirement, err := resolveRequirementByID(s.requirements[projectID], plan.RequirementID)
	if err != nil {
		return nil, err
	}

	version := len(s.contexts[task.ID]) + 1
	injection := buildTaskContextInjection(task, plan, contract, requirement, version, now)
	s.contextIndex[injection.ID] = injection
	s.contexts[task.ID] = append(s.contexts[task.ID], injection)

	return &TaskContextEnvelope{
		Task:    *cloneTask(task),
		Context: *cloneContextInjection(injection),
	}, nil
}

func (s *Service) GetLatestTaskContext(_ context.Context, projectID, taskID string) (*TaskContextEnvelope, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	task, ok := s.tasks[taskID]
	if !ok || task.ProjectID != projectID {
		return nil, newNotFoundError("task not found")
	}

	history := s.contexts[taskID]
	if len(history) == 0 {
		return nil, newNotFoundError("context not found for task")
	}

	latest := history[len(history)-1]
	return &TaskContextEnvelope{
		Task:    *cloneTask(task),
		Context: *cloneContextInjection(latest),
	}, nil
}

func (s *Service) GetRunSandbox(_ context.Context, projectID, runID string) (*SandboxView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[runID]
	if !ok || run.ProjectID != projectID {
		return nil, newNotFoundError("run not found")
	}
	if run.SandboxID == "" {
		return nil, newNotFoundError("sandbox not found for run")
	}
	sandbox, ok := s.sandboxIndex[run.SandboxID]
	if !ok || sandbox.ProjectID != projectID {
		return nil, newNotFoundError("sandbox not found")
	}
	task, ok := s.tasks[run.TaskID]
	if !ok {
		return nil, newNotFoundError("task not found for sandbox")
	}

	return &SandboxView{
		Sandbox: *cloneSandbox(sandbox),
		Run:     *cloneRun(run),
		Task:    *cloneTask(task),
	}, nil
}

func (s *Service) GetStatusMatrix(_ context.Context, selectedProjectID string) (*StatusMatrixView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	projects := make([]domain.Project, 0, len(s.projects))
	for _, project := range s.projects {
		projects = append(projects, *cloneProject(project))
	}
	slices.SortFunc(projects, func(a, b domain.Project) int {
		switch {
		case a.CreatedAt.Before(b.CreatedAt):
			return -1
		case a.CreatedAt.After(b.CreatedAt):
			return 1
		default:
			return strings.Compare(a.ID, b.ID)
		}
	})

	if selectedProjectID != "" {
		if _, ok := s.projects[selectedProjectID]; !ok {
			return nil, newNotFoundError("project not found")
		}
	}

	matrices := make([]StatusMatrixProject, 0, len(projects))
	for _, project := range projects {
		if selectedProjectID != "" && project.ID != selectedProjectID {
			continue
		}
		matrices = append(matrices, s.buildProjectStatusMatrixLocked(project))
	}

	return &StatusMatrixView{
		Projects:          projects,
		SelectedProjectID: selectedProjectID,
		Matrices:          matrices,
		GeneratedAt:       time.Now().UTC(),
	}, nil
}

func (s *Service) ListTasks(_ context.Context, projectID string) ([]domain.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	order := s.taskOrder[projectID]
	result := make([]domain.Task, 0, len(order))
	for _, taskID := range order {
		task, ok := s.tasks[taskID]
		if !ok {
			continue
		}
		result = append(result, *cloneTask(task))
	}

	return result, nil
}

func (s *Service) MarkTaskSandboxFailure(_ context.Context, projectID, taskID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return newNotFoundError("project not found")
	}
	task, err := s.resolveTaskLocked(projectID, taskID)
	if err != nil {
		return err
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "simulated sandbox failure"
	}
	s.sandboxFaults[task.ID] = reason
	return nil
}

func (s *Service) DispatchTasks(_ context.Context, projectID string) (*DispatchTasksResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	plan, contract, err := s.resolveLatestPlanAndContractLocked(projectID)
	if err != nil {
		return nil, err
	}

	existing := s.findDispatchedTasksLocked(projectID, plan.ID, contract.ID)
	if len(existing) > 0 {
		return &DispatchTasksResult{
			Contract: *cloneContract(contract),
			Tasks:    cloneTasks(existing),
		}, nil
	}

	inputRef := fmt.Sprintf("contract://%s", contract.ID)
	backendTask := domain.NewTask(
		nextID("task"),
		projectID,
		plan.ID,
		fmt.Sprintf("Build backend implementation for %s", plan.Title),
		"BACKEND_IMPLEMENTATION",
		"go-backend-agent",
		nil,
		inputRef,
		now,
	)
	frontendTask := domain.NewTask(
		nextID("task"),
		projectID,
		plan.ID,
		fmt.Sprintf("Build frontend implementation for %s", plan.Title),
		"FRONTEND_IMPLEMENTATION",
		"vue-frontend-agent",
		nil,
		inputRef,
		now,
	)
	integrationTask := domain.NewTask(
		nextID("task"),
		projectID,
		plan.ID,
		fmt.Sprintf("Merge and verify %s", plan.Title),
		"INTEGRATION_REVIEW",
		"integration-agent",
		[]string{backendTask.ID, frontendTask.ID},
		inputRef,
		now,
	)

	dispatched := []*domain.Task{backendTask, frontendTask, integrationTask}
	for _, task := range dispatched {
		s.tasks[task.ID] = task
		s.taskOrder[projectID] = append(s.taskOrder[projectID], task.ID)
	}
	project.UpdatedAt = now

	return &DispatchTasksResult{
		Contract: *cloneContract(contract),
		Tasks:    cloneTasks(dispatched),
	}, nil
}

func (s *Service) ValidateContract(_ context.Context, projectID string, input ValidateContractInput) (*ContractValidationResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	contract, err := s.resolveContractLocked(projectID, input.ContractID)
	if err != nil {
		return nil, err
	}

	if len(input.Endpoints) == 0 && len(input.Schemas) == 0 {
		return nil, newValidationError("validation payload must include endpoints or schemas")
	}

	conflicts := validateContractDefinition(contract, input.Endpoints, input.Schemas)
	result := &ContractValidationResult{
		Contract:  *cloneContract(contract),
		Passed:    len(conflicts) == 0,
		Conflicts: conflicts,
	}

	if len(conflicts) == 0 {
		return result, nil
	}

	now := time.Now().UTC()
	task := domain.NewTask(
		nextID("task"),
		projectID,
		contract.PlanID,
		fmt.Sprintf("Resolve contract conflicts for v%d", contract.Version),
		"CONTRACT_REWORK",
		s.cfg.DefaultAgent,
		nil,
		fmt.Sprintf("contract://%s", contract.ID),
		now,
	)

	s.tasks[task.ID] = task
	s.taskOrder[projectID] = append(s.taskOrder[projectID], task.ID)
	project.UpdatedAt = now
	result.RemediationTask = cloneTask(task)

	return result, nil
}

func (s *Service) RetryTask(_ context.Context, projectID, taskID string) (*domain.Task, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	task, err := s.resolveTaskLocked(projectID, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status != domain.TaskStatusFailed {
		return nil, newConflictError("only failed tasks can be retried")
	}

	retryTask := domain.NewTask(
		nextID("task"),
		projectID,
		task.PlanID,
		task.Name+" (retry)",
		task.Type,
		task.AssigneeAgent,
		task.DependsOn,
		task.InputRef,
		now,
	)

	s.tasks[retryTask.ID] = retryTask
	s.taskOrder[projectID] = append(s.taskOrder[projectID], retryTask.ID)
	project.UpdatedAt = now

	return cloneTask(retryTask), nil
}

func (s *Service) StartRun(_ context.Context, projectID string, input StartRunInput) (*RunEnvelope, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	task, err := s.resolveTaskLocked(projectID, input.TaskID)
	if err != nil {
		return nil, err
	}

	envelope, err := s.startTaskRunLocked(projectID, task, now, "single agent execution started")
	if err != nil {
		return nil, err
	}

	go s.executeRun(envelope.Run.ID)

	return envelope, nil
}

func (s *Service) StartParallelRun(_ context.Context, projectID string, input ParallelRunInput) (*ParallelRunResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	selected, blocked, err := s.resolveParallelTasksLocked(projectID, input.TaskIDs)
	if err != nil {
		return nil, err
	}

	started := make([]RunEnvelope, 0, len(selected))
	for _, task := range selected {
		envelope, startErr := s.startTaskRunLocked(projectID, task, now, "parallel execution started")
		if startErr != nil {
			return nil, startErr
		}
		started = append(started, *envelope)
	}

	for _, envelope := range started {
		go s.executeRun(envelope.Run.ID)
	}

	return &ParallelRunResult{
		BatchID:      nextID("batch"),
		Started:      started,
		BlockedTasks: cloneTasks(blocked),
	}, nil
}

func (s *Service) GetRunStatus(_ context.Context, projectID, runID string) (*RunStatusView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[runID]
	if !ok || run.ProjectID != projectID {
		return nil, newNotFoundError("run not found")
	}

	task, ok := s.tasks[run.TaskID]
	if !ok {
		return nil, newNotFoundError("task not found for run")
	}

	artifacts := make([]domain.Artifact, 0, len(run.ArtifactIDs))
	for _, artifactID := range run.ArtifactIDs {
		if artifact, exists := s.artifacts[artifactID]; exists {
			artifacts = append(artifacts, *cloneArtifact(artifact))
		}
	}

	return &RunStatusView{
		Run:       *cloneRun(run),
		Task:      *cloneTask(task),
		Artifacts: artifacts,
	}, nil
}

func (s *Service) ExportDelivery(_ context.Context, projectID string, input ExportDeliveryInput) (*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	if input.RunID != "" {
		run, ok := s.runs[input.RunID]
		if !ok || run.ProjectID != projectID {
			return nil, newNotFoundError("run not found")
		}
		artifact, err := s.resolveArtifactFromRunLocked(run)
		if err != nil {
			return nil, err
		}
		return cloneArtifact(artifact), nil
	}

	runIDs := s.runOrder[projectID]
	for idx := len(runIDs) - 1; idx >= 0; idx-- {
		run := s.runs[runIDs[idx]]
		if run.Status != domain.RunStatusSucceeded {
			continue
		}
		artifact, err := s.resolveArtifactFromRunLocked(run)
		if err == nil {
			return cloneArtifact(artifact), nil
		}
	}

	return nil, newConflictError("no exportable artifact found")
}

func (s *Service) GetArtifact(_ context.Context, projectID, artifactID string) (*domain.Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	artifact, ok := s.artifacts[artifactID]
	if !ok || artifact.ProjectID != projectID {
		return nil, newNotFoundError("artifact not found")
	}

	return cloneArtifact(artifact), nil
}

func (s *Service) executeRun(runID string) {
	run, task, plan, project, sandbox, err := s.snapshotForExecution(runID)
	if err != nil {
		s.logger.Error("failed to prepare run snapshot", "runId", runID, "error", err)
		s.failRun(runID, err)
		return
	}

	if failure := s.sandboxFailureForTask(task.ID); failure != "" {
		if sandbox != nil {
			_ = writeFile(filepath.Join(sandbox.RootPath, "sandbox-error.log"), []byte(failure+"\n"))
		}
		s.logger.Error("sandbox execution failed", "runId", run.ID, "taskId", task.ID, "sandboxId", run.SandboxID, "error", failure)
		s.failRun(runID, errors.New(failure))
		return
	}

	artifact, summary, err := s.generateDeliveryBundle(project, task, plan, run, sandbox)
	if err != nil {
		s.logger.Error("run execution failed", "runId", run.ID, "taskId", task.ID, "error", err)
		s.failRun(runID, err)
		return
	}

	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	storedRun, ok := s.runs[runID]
	if !ok {
		return
	}
	storedTask, ok := s.tasks[task.ID]
	if !ok {
		return
	}

	s.artifacts[artifact.ID] = artifact
	s.artifactOrder[artifact.ProjectID] = append(s.artifactOrder[artifact.ProjectID], artifact.ID)

	storedRun.Status = domain.RunStatusSucceeded
	storedRun.ResultSummary = summary
	storedRun.ArtifactIDs = append(storedRun.ArtifactIDs, artifact.ID)
	storedRun.EndedAt = now

	storedTask.OutputRef = artifact.URI
	if err := storedTask.TransitionTo(domain.TaskStatusDone, "single agent execution completed", now); err != nil {
		s.logger.Error("failed to transition task to done", "taskId", storedTask.ID, "error", err)
	}
	if storedRun.SandboxID != "" {
		if sandbox, exists := s.sandboxIndex[storedRun.SandboxID]; exists {
			sandbox.Status = domain.SandboxStatusReleased
			sandbox.UpdatedAt = now
		}
	}

	s.logger.Info("run execution completed", "runId", storedRun.ID, "taskId", storedTask.ID, "artifactId", artifact.ID)
}

func (s *Service) snapshotForExecution(runID string) (*domain.AgentRun, *domain.Task, *domain.Plan, *domain.Project, *domain.Sandbox, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	run, ok := s.runs[runID]
	if !ok {
		return nil, nil, nil, nil, nil, errors.New("run not found")
	}

	task, ok := s.tasks[run.TaskID]
	if !ok {
		return nil, nil, nil, nil, nil, errors.New("task not found")
	}

	plan, ok := s.planIndex[task.PlanID]
	if !ok {
		return nil, nil, nil, nil, nil, errors.New("plan not found")
	}

	project, ok := s.projects[run.ProjectID]
	if !ok {
		return nil, nil, nil, nil, nil, errors.New("project not found")
	}

	var sandbox *domain.Sandbox
	if run.SandboxID != "" {
		sandbox = cloneSandbox(s.sandboxIndex[run.SandboxID])
	}

	return cloneRun(run), cloneTask(task), clonePlan(plan), cloneProject(project), sandbox, nil
}

func (s *Service) failRun(runID string, failure error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.runs[runID]
	if !ok {
		return
	}

	run.Status = domain.RunStatusFailed
	run.Error = failure.Error()
	run.EndedAt = now
	if run.SandboxID != "" {
		if sandbox, exists := s.sandboxIndex[run.SandboxID]; exists {
			sandbox.Status = domain.SandboxStatusFailed
			sandbox.FailureReason = failure.Error()
			sandbox.UpdatedAt = now
		}
	}

	if task, exists := s.tasks[run.TaskID]; exists && task.Status == domain.TaskStatusInProgress {
		if err := task.TransitionTo(domain.TaskStatusFailed, "single agent execution failed", now); err != nil {
			s.logger.Error("failed to transition task to failed", "taskId", task.ID, "error", err)
		}
	}
}

func (s *Service) generateDeliveryBundle(project *domain.Project, task *domain.Task, plan *domain.Plan, run *domain.AgentRun, sandbox *domain.Sandbox) (*domain.Artifact, string, error) {
	runDir := filepath.Join(s.cfg.ArtifactRoot, project.ID, run.ID)
	bundleDir := filepath.Join(runDir, "bundle")
	if sandbox != nil && strings.TrimSpace(sandbox.RootPath) != "" {
		bundleDir = filepath.Join(sandbox.RootPath, "workspace", "bundle")
	}

	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create bundle directory: %w", err)
	}

	if err := writeFile(filepath.Join(bundleDir, "README.md"), []byte(renderBundleReadme(project, task, plan))); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "generated-app", "go.mod"), []byte("module generated-app\n\ngo 1.26\n")); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "generated-app", "main.go"), []byte(renderGeneratedSource(project, plan))); err != nil {
		return nil, "", err
	}

	if err := writeJSONFile(filepath.Join(bundleDir, "metadata", "prd.json"), plan); err != nil {
		return nil, "", err
	}
	if err := writeJSONFile(filepath.Join(bundleDir, "metadata", "task.json"), task); err != nil {
		return nil, "", err
	}
	if err := writeJSONFile(filepath.Join(bundleDir, "metadata", "run.json"), run); err != nil {
		return nil, "", err
	}
	if err := writeJSONFile(filepath.Join(bundleDir, "metadata", "manifest.json"), map[string]any{
		"projectId": project.ID,
		"runId":     run.ID,
		"taskId":    task.ID,
		"kind":      "delivery_bundle",
		"createdAt": time.Now().UTC(),
	}); err != nil {
		return nil, "", err
	}

	zipPath := filepath.Join(runDir, "delivery.zip")
	if err := zipDirectory(bundleDir, zipPath); err != nil {
		return nil, "", fmt.Errorf("zip bundle: %w", err)
	}

	checksum, size, err := fileChecksum(zipPath)
	if err != nil {
		return nil, "", fmt.Errorf("checksum bundle: %w", err)
	}

	artifact := &domain.Artifact{
		ID:        nextID("artifact"),
		ProjectID: project.ID,
		TaskID:    task.ID,
		RunID:     run.ID,
		Kind:      "delivery_bundle",
		URI:       zipPath,
		Checksum:  checksum,
		SizeBytes: size,
		CreatedAt: time.Now().UTC(),
	}

	summary := fmt.Sprintf("generated delivery bundle for plan v%d at %s", plan.Version, zipPath)
	return artifact, summary, nil
}

func (s *Service) resolveTaskLocked(projectID, taskID string) (*domain.Task, error) {
	if taskID != "" {
		task, ok := s.tasks[taskID]
		if !ok || task.ProjectID != projectID {
			return nil, newNotFoundError("task not found")
		}
		return task, nil
	}

	order := s.taskOrder[projectID]
	if len(order) == 0 {
		return nil, newValidationError("no task available for execution")
	}

	task := s.tasks[order[len(order)-1]]
	return task, nil
}

func (s *Service) resolveLatestPlanAndContractLocked(projectID string) (*domain.Plan, *domain.Contract, error) {
	plans := s.plans[projectID]
	if len(plans) == 0 {
		return nil, nil, newValidationError("no plan available for task dispatch")
	}
	contracts := s.contracts[projectID]
	if len(contracts) == 0 {
		return nil, nil, newValidationError("no contract available for task dispatch")
	}
	return plans[len(plans)-1], contracts[len(contracts)-1], nil
}

func (s *Service) buildProjectStatusMatrixLocked(project domain.Project) StatusMatrixProject {
	order := s.taskOrder[project.ID]
	taskMatrix := make([]StatusMatrixTask, 0, len(order))
	agentMap := make(map[string]*StatusMatrixAgent)

	var readyTasks, runningTasks, completedTasks, failedTasks int
	for _, taskID := range order {
		task, ok := s.tasks[taskID]
		if !ok {
			continue
		}

		latestRunStatus := s.latestRunStatusForTaskLocked(project.ID, task.ID)
		taskMatrix = append(taskMatrix, StatusMatrixTask{
			ID:              task.ID,
			Name:            task.Name,
			Type:            task.Type,
			AssigneeAgent:   task.AssigneeAgent,
			Status:          task.Status,
			DependsOn:       append([]string(nil), task.DependsOn...),
			LatestRunStatus: latestRunStatus,
		})

		switch task.Status {
		case domain.TaskStatusCreated:
			readyTasks++
		case domain.TaskStatusInProgress:
			runningTasks++
		case domain.TaskStatusDone:
			completedTasks++
		case domain.TaskStatusFailed:
			failedTasks++
		}

		agentName := task.AssigneeAgent
		if strings.TrimSpace(agentName) == "" {
			agentName = s.cfg.DefaultAgent
		}
		agent, ok := agentMap[agentName]
		if !ok {
			agent = &StatusMatrixAgent{Agent: agentName}
			agentMap[agentName] = agent
		}
		agent.TotalTasks++
		switch task.Status {
		case domain.TaskStatusCreated:
			agent.CreatedTasks++
		case domain.TaskStatusInProgress:
			agent.RunningTasks++
		case domain.TaskStatusDone:
			agent.DoneTasks++
		case domain.TaskStatusFailed:
			agent.FailedTasks++
		}
	}

	agentMatrix := make([]StatusMatrixAgent, 0, len(agentMap))
	for _, agent := range agentMap {
		agent.Status = deriveAgentMatrixStatus(*agent)
		agentMatrix = append(agentMatrix, *agent)
	}
	slices.SortFunc(agentMatrix, func(a, b StatusMatrixAgent) int {
		return strings.Compare(a.Agent, b.Agent)
	})

	return StatusMatrixProject{
		Project:        project,
		AgentMatrix:    agentMatrix,
		TaskMatrix:     taskMatrix,
		TotalTasks:     len(taskMatrix),
		ReadyTasks:     readyTasks,
		RunningTasks:   runningTasks,
		CompletedTasks: completedTasks,
		FailedTasks:    failedTasks,
	}
}

func (s *Service) latestRunStatusForTaskLocked(projectID, taskID string) domain.RunStatus {
	runIDs := s.runOrder[projectID]
	for idx := len(runIDs) - 1; idx >= 0; idx-- {
		run, ok := s.runs[runIDs[idx]]
		if !ok || run.TaskID != taskID {
			continue
		}
		return run.Status
	}
	return ""
}

func deriveAgentMatrixStatus(agent StatusMatrixAgent) string {
	switch {
	case agent.FailedTasks > 0:
		return "BLOCKED"
	case agent.RunningTasks > 0:
		return "RUNNING"
	case agent.CreatedTasks > 0:
		return "READY"
	case agent.DoneTasks > 0:
		return "COMPLETED"
	default:
		return "IDLE"
	}
}

func (s *Service) resolveTaskContractLocked(projectID string, task *domain.Task) (*domain.Contract, error) {
	if strings.HasPrefix(task.InputRef, "contract://") {
		contractID := strings.TrimPrefix(task.InputRef, "contract://")
		contract, ok := s.contractIndex[contractID]
		if ok && contract.ProjectID == projectID {
			return contract, nil
		}
	}

	contracts := s.contracts[projectID]
	if len(contracts) == 0 {
		return nil, newNotFoundError("contract not found for task")
	}
	return contracts[len(contracts)-1], nil
}

func (s *Service) findDispatchedTasksLocked(projectID, planID, contractID string) []*domain.Task {
	expectedInputRef := fmt.Sprintf("contract://%s", contractID)
	result := make([]*domain.Task, 0, 3)
	for _, taskID := range s.taskOrder[projectID] {
		task, ok := s.tasks[taskID]
		if !ok || task.PlanID != planID || task.InputRef != expectedInputRef {
			continue
		}
		switch task.Type {
		case "BACKEND_IMPLEMENTATION", "FRONTEND_IMPLEMENTATION", "INTEGRATION_REVIEW":
			result = append(result, task)
		}
	}
	return result
}

func (s *Service) resolveParallelTasksLocked(projectID string, taskIDs []string) ([]*domain.Task, []*domain.Task, error) {
	if len(taskIDs) > 0 {
		selected := make([]*domain.Task, 0, len(taskIDs))
		blocked := make([]*domain.Task, 0)
		seen := make(map[string]struct{}, len(taskIDs))
		for _, taskID := range taskIDs {
			task, err := s.resolveTaskLocked(projectID, taskID)
			if err != nil {
				return nil, nil, err
			}
			if _, ok := seen[task.ID]; ok {
				continue
			}
			seen[task.ID] = struct{}{}
			if err := s.ensureTaskReadyLocked(task); err != nil {
				blocked = append(blocked, task)
				continue
			}
			selected = append(selected, task)
		}
		if len(selected) == 0 {
			return nil, nil, newConflictError("no selected tasks are ready for parallel execution")
		}
		return selected, blocked, nil
	}

	selected := make([]*domain.Task, 0)
	blocked := make([]*domain.Task, 0)
	for _, taskID := range s.taskOrder[projectID] {
		task, ok := s.tasks[taskID]
		if !ok || task.Type == "SPRINT1_EXECUTE" {
			continue
		}
		if task.Status != domain.TaskStatusCreated {
			continue
		}
		if err := s.ensureTaskReadyLocked(task); err != nil {
			blocked = append(blocked, task)
			continue
		}
		selected = append(selected, task)
	}
	if len(selected) == 0 {
		return nil, nil, newConflictError("no ready tasks available for parallel execution")
	}
	return selected, blocked, nil
}

func (s *Service) ensureTaskReadyLocked(task *domain.Task) error {
	if task.Status != domain.TaskStatusCreated {
		return newConflictError("task is not ready to run")
	}
	for _, dependencyID := range task.DependsOn {
		dependency, ok := s.tasks[dependencyID]
		if !ok {
			return newConflictError("task dependency is missing")
		}
		if dependency.Status != domain.TaskStatusDone {
			return newConflictError("task dependencies are not completed")
		}
	}
	return nil
}

func (s *Service) startTaskRunLocked(projectID string, task *domain.Task, now time.Time, reason string) (*RunEnvelope, error) {
	if err := s.ensureTaskReadyLocked(task); err != nil {
		return nil, err
	}

	if err := task.TransitionTo(domain.TaskStatusInProgress, reason, now); err != nil {
		return nil, newConflictError(err.Error())
	}

	agentType := task.AssigneeAgent
	if strings.TrimSpace(agentType) == "" {
		agentType = s.cfg.DefaultAgent
	}

	sandbox := s.createPrivateSandboxLocked(projectID, task, agentType, now)

	run := &domain.AgentRun{
		ID:        nextID("run"),
		ProjectID: projectID,
		TaskID:    task.ID,
		AgentType: agentType,
		Model:     "rule-based-" + agentType,
		SandboxID: sandbox.ID,
		Status:    domain.RunStatusRunning,
		StartedAt: now,
	}
	sandbox.RunID = run.ID
	sandbox.UpdatedAt = now

	s.runs[run.ID] = run
	s.runOrder[projectID] = append(s.runOrder[projectID], run.ID)

	return &RunEnvelope{
		Task: *cloneTask(task),
		Run:  *cloneRun(run),
	}, nil
}

func (s *Service) createPrivateSandboxLocked(projectID string, task *domain.Task, agentType string, now time.Time) *domain.Sandbox {
	sandbox := &domain.Sandbox{
		ID:        nextID("sandbox"),
		ProjectID: projectID,
		TaskID:    task.ID,
		AgentType: agentType,
		Scope:     "PRIVATE",
		Status:    domain.SandboxStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	sandbox.RootPath = filepath.Join(s.cfg.SandboxRoot, projectID, sandbox.ID)
	_ = os.MkdirAll(filepath.Join(sandbox.RootPath, "workspace"), 0o755)

	s.sandboxIndex[sandbox.ID] = sandbox
	s.sandboxes[projectID] = append(s.sandboxes[projectID], sandbox)
	return sandbox
}

func (s *Service) sandboxFailureForTask(taskID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	failure := strings.TrimSpace(s.sandboxFaults[taskID])
	if failure == "" {
		return ""
	}
	delete(s.sandboxFaults, taskID)
	return failure
}

func (s *Service) resolveContractLocked(projectID, contractID string) (*domain.Contract, error) {
	if contractID != "" {
		contract, ok := s.contractIndex[contractID]
		if !ok || contract.ProjectID != projectID {
			return nil, newNotFoundError("contract not found")
		}
		return contract, nil
	}

	order := s.contracts[projectID]
	if len(order) == 0 {
		return nil, newValidationError("no contract available for validation")
	}

	return order[len(order)-1], nil
}

func (s *Service) resolveArtifactFromRunLocked(run *domain.AgentRun) (*domain.Artifact, error) {
	if run.Status != domain.RunStatusSucceeded {
		return nil, newConflictError("run has not completed successfully")
	}
	if len(run.ArtifactIDs) == 0 {
		return nil, newConflictError("run has no exported artifact")
	}
	artifact, ok := s.artifacts[run.ArtifactIDs[len(run.ArtifactIDs)-1]]
	if !ok {
		return nil, newNotFoundError("artifact not found")
	}
	return artifact, nil
}

func buildPlan(req *domain.Requirement, version int, now time.Time) *domain.Plan {
	constraints := compactStrings(append([]string{
		"优先跑通 Sprint 1 最小闭环",
		"当前版本使用内存存储与单 Agent 串行模式",
	}, req.Constraints...))

	acceptance := compactStrings(req.AcceptanceHints)
	if len(acceptance) == 0 {
		acceptance = []string{
			"系统可生成结构化 PRD",
			"任务可从 CREATED 流转到 DONE",
			"执行完成后可导出交付包",
		}
	}

	scope := deriveScope(req.Content)
	planTitle := req.Title
	if planTitle == "" {
		planTitle = trimSentence(req.Content)
	}

	return &domain.Plan{
		ID:                 nextID("plan"),
		ProjectID:          req.ProjectID,
		RequirementID:      req.ID,
		Version:            version,
		Title:              planTitle,
		Goal:               fmt.Sprintf("将需求“%s”收敛为可执行的 Sprint 1 交付闭环。", planTitle),
		Scope:              scope,
		Constraints:        constraints,
		AcceptanceCriteria: acceptance,
		Assumptions: []string{
			"第一个周期优先验证主链路，不引入并行调度与持久化依赖",
			"生成产物以可审阅、可下载、可追踪为核心目标",
		},
		CreatedAt: now,
	}
}

func buildContract(req *domain.Requirement, plan *domain.Plan, version int, now time.Time) *domain.Contract {
	resourceName, resourcePath := deriveContractResource(req, plan)
	title := strings.TrimSpace(plan.Title)
	if title == "" {
		title = resourceName
	}

	return &domain.Contract{
		ID:            nextID("contract"),
		ProjectID:     req.ProjectID,
		RequirementID: req.ID,
		PlanID:        plan.ID,
		Version:       version,
		Name:          fmt.Sprintf("%s Contract", title),
		Summary:       fmt.Sprintf("Contract-first API and schema definition for %s (v%d).", title, version),
		Endpoints: []domain.ContractEndpoint{
			{
				Name:        "List" + resourceName,
				Method:      "GET",
				Path:        "/api/" + resourcePath,
				Description: fmt.Sprintf("List %s resources for MVP review.", strings.ToLower(resourceName)),
			},
			{
				Name:        "Get" + resourceName,
				Method:      "GET",
				Path:        "/api/" + resourcePath + "/{id}",
				Description: fmt.Sprintf("Load a single %s resource by id.", strings.ToLower(resourceName)),
			},
			{
				Name:        "Create" + resourceName,
				Method:      "POST",
				Path:        "/api/" + resourcePath,
				Description: fmt.Sprintf("Create a new %s resource.", strings.ToLower(resourceName)),
			},
			{
				Name:        "Update" + resourceName,
				Method:      "PUT",
				Path:        "/api/" + resourcePath + "/{id}",
				Description: fmt.Sprintf("Update an existing %s resource.", strings.ToLower(resourceName)),
			},
			{
				Name:        "Delete" + resourceName,
				Method:      "DELETE",
				Path:        "/api/" + resourcePath + "/{id}",
				Description: fmt.Sprintf("Delete an existing %s resource.", strings.ToLower(resourceName)),
			},
		},
		Schemas: []domain.ContractSchema{
			{
				Name:        resourceName,
				Description: fmt.Sprintf("Primary resource model for %s.", title),
				Fields: []domain.ContractField{
					{Name: "id", Type: "string", Required: true, Description: "Resource identifier"},
					{Name: "title", Type: "string", Required: true, Description: "Display title"},
					{Name: "completed", Type: "boolean", Required: true, Description: "Completion status"},
					{Name: "createdAt", Type: "string(date-time)", Required: true, Description: "Creation timestamp"},
				},
			},
			{
				Name:        resourceName + "Input",
				Description: fmt.Sprintf("Payload for creating or updating %s.", strings.ToLower(resourceName)),
				Fields: []domain.ContractField{
					{Name: "title", Type: "string", Required: true, Description: "Display title"},
					{Name: "completed", Type: "boolean", Required: false, Description: "Optional completion flag"},
				},
			},
		},
		CreatedAt: now,
	}
}

func buildTaskContextInjection(task *domain.Task, plan *domain.Plan, contract *domain.Contract, requirement *domain.Requirement, version int, now time.Time) *domain.ContextInjection {
	role := deriveTaskContextRole(task)
	return &domain.ContextInjection{
		ID:        nextID("ctx"),
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Role:      role,
		Version:   version,
		Summary:   fmt.Sprintf("%s context for task %s", role, task.Name),
		Sources: []domain.ContextSource{
			{Kind: "requirement", Ref: "requirement://" + requirement.ID},
			{Kind: "plan", Ref: "plan://" + plan.ID, Version: fmt.Sprintf("v%d", plan.Version)},
			{Kind: "contract", Ref: "contract://" + contract.ID, Version: fmt.Sprintf("v%d", contract.Version)},
		},
		Sections:  buildContextSections(task, plan, contract, requirement, role),
		CreatedAt: now,
	}
}

func deriveTaskContextRole(task *domain.Task) string {
	switch task.Type {
	case "BACKEND_IMPLEMENTATION":
		return "backend"
	case "FRONTEND_IMPLEMENTATION":
		return "frontend"
	case "INTEGRATION_REVIEW":
		return "integration"
	default:
		return "general"
	}
}

func buildContextSections(task *domain.Task, plan *domain.Plan, contract *domain.Contract, requirement *domain.Requirement, role string) []domain.ContextSection {
	switch role {
	case "backend":
		return []domain.ContextSection{
			{
				Title: "Execution Focus",
				Items: []string{
					"实现后端 API 与数据模型，优先满足契约而不是扩展范围。",
					"只关注服务端职责，避免把 UI 细节带入后端实现。",
				},
			},
			{
				Title: "Requirement Signals",
				Items: compactStrings(append([]string{requirement.Title, requirement.Content}, filterConstraints(requirement.Constraints, "backend")...)),
			},
			{
				Title: "API Contract",
				Items: renderContextEndpoints(contract.Endpoints),
			},
			{
				Title: "Data Schemas",
				Items: renderContextSchemas(contract.Schemas),
			},
		}
	case "frontend":
		return []domain.ContextSection{
			{
				Title: "Execution Focus",
				Items: []string{
					"围绕用户交互和页面状态组织实现，避免扩展后端内部细节。",
					"优先消费既有契约，确保字段展示和交互流程与验收一致。",
				},
			},
			{
				Title: "UX Scope",
				Items: compactStrings(append([]string{}, plan.Scope...)),
			},
			{
				Title: "Acceptance Criteria",
				Items: compactStrings(append([]string{}, plan.AcceptanceCriteria...)),
			},
			{
				Title: "API Consumption",
				Items: renderContextEndpointSummaries(contract.Endpoints),
			},
		}
	case "integration":
		return []domain.ContextSection{
			{
				Title: "Execution Focus",
				Items: []string{
					"检查前后端产物是否满足同一份契约，并准备共享交付包。",
					"优先关注依赖完成状态、契约一致性和验收标准闭环。",
				},
			},
			{
				Title: "Dependencies",
				Items: renderDependencyItems(task.DependsOn),
			},
			{
				Title: "Contract Summary",
				Items: []string{contract.Summary},
			},
			{
				Title: "Acceptance Criteria",
				Items: compactStrings(append([]string{}, plan.AcceptanceCriteria...)),
			},
		}
	default:
		return []domain.ContextSection{
			{
				Title: "Goal",
				Items: []string{plan.Goal},
			},
			{
				Title: "Requirement",
				Items: []string{requirement.Content},
			},
		}
	}
}

func filterConstraints(items []string, role string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		lower := strings.ToLower(item)
		switch role {
		case "backend":
			if strings.Contains(lower, "go") || strings.Contains(lower, "后端") || strings.Contains(lower, "api") || strings.Contains(lower, "数据") {
				result = append(result, item)
			}
		case "frontend":
			if strings.Contains(lower, "vue") || strings.Contains(lower, "前端") || strings.Contains(lower, "ui") || strings.Contains(lower, "交互") {
				result = append(result, item)
			}
		}
	}
	return result
}

func renderContextEndpoints(items []domain.ContractEndpoint) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprintf("%s %s (%s)", item.Method, item.Path, item.Name))
	}
	return result
}

func renderContextEndpointSummaries(items []domain.ContractEndpoint) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, fmt.Sprintf("%s %s", item.Method, item.Path))
	}
	return result
}

func renderContextSchemas(items []domain.ContractSchema) []string {
	result := make([]string, 0, len(items))
	for _, schema := range items {
		fields := make([]string, 0, len(schema.Fields))
		for _, field := range schema.Fields {
			fields = append(fields, fmt.Sprintf("%s:%s", field.Name, field.Type))
		}
		result = append(result, fmt.Sprintf("%s => %s", schema.Name, strings.Join(fields, ", ")))
	}
	return result
}

func renderDependencyItems(items []string) []string {
	if len(items) == 0 {
		return []string{"No dependencies"}
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, "task://"+item)
	}
	return result
}

func validateContractDefinition(contract *domain.Contract, endpoints []domain.ContractEndpoint, schemas []domain.ContractSchema) []ContractValidationConflict {
	conflicts := make([]ContractValidationConflict, 0)

	endpointIndex := make(map[string]domain.ContractEndpoint, len(endpoints))
	for _, endpoint := range endpoints {
		endpointIndex[contractEndpointKey(endpoint.Method, endpoint.Path)] = endpoint
	}

	for _, expected := range contract.Endpoints {
		key := contractEndpointKey(expected.Method, expected.Path)
		actual, ok := endpointIndex[key]
		if !ok {
			conflicts = append(conflicts, ContractValidationConflict{
				Type:     "MISSING_ENDPOINT",
				Location: key,
				Message:  "candidate implementation is missing a required endpoint",
				Expected: expected.Method + " " + expected.Path,
			})
			continue
		}
		if strings.TrimSpace(actual.Name) == "" {
			conflicts = append(conflicts, ContractValidationConflict{
				Type:     "INCOMPLETE_ENDPOINT",
				Location: key,
				Message:  "candidate endpoint must include a name",
				Expected: expected.Name,
			})
		}
	}

	for _, actual := range endpoints {
		key := contractEndpointKey(actual.Method, actual.Path)
		if !hasContractEndpoint(contract.Endpoints, actual.Method, actual.Path) {
			conflicts = append(conflicts, ContractValidationConflict{
				Type:     "UNEXPECTED_ENDPOINT",
				Location: key,
				Message:  "candidate implementation defines an endpoint not present in the contract",
				Actual:   actual.Method + " " + actual.Path,
			})
		}
	}

	schemaIndex := make(map[string]domain.ContractSchema, len(schemas))
	for _, schema := range schemas {
		schemaIndex[schema.Name] = schema
	}

	for _, expectedSchema := range contract.Schemas {
		actualSchema, ok := schemaIndex[expectedSchema.Name]
		if !ok {
			conflicts = append(conflicts, ContractValidationConflict{
				Type:     "MISSING_SCHEMA",
				Location: expectedSchema.Name,
				Message:  "candidate implementation is missing a required schema",
				Expected: expectedSchema.Name,
			})
			continue
		}

		fieldIndex := make(map[string]domain.ContractField, len(actualSchema.Fields))
		for _, field := range actualSchema.Fields {
			fieldIndex[field.Name] = field
		}

		for _, expectedField := range expectedSchema.Fields {
			actualField, ok := fieldIndex[expectedField.Name]
			location := expectedSchema.Name + "." + expectedField.Name
			if !ok {
				conflicts = append(conflicts, ContractValidationConflict{
					Type:     "MISSING_FIELD",
					Location: location,
					Message:  "candidate implementation is missing a required field",
					Expected: fmt.Sprintf("%s:%s", expectedField.Name, expectedField.Type),
				})
				continue
			}
			if actualField.Type != expectedField.Type {
				conflicts = append(conflicts, ContractValidationConflict{
					Type:     "FIELD_TYPE_MISMATCH",
					Location: location,
					Message:  "candidate field type does not match the contract",
					Expected: expectedField.Type,
					Actual:   actualField.Type,
				})
			}
			if actualField.Required != expectedField.Required {
				conflicts = append(conflicts, ContractValidationConflict{
					Type:     "FIELD_REQUIRED_MISMATCH",
					Location: location,
					Message:  "candidate field required flag does not match the contract",
					Expected: fmt.Sprintf("%t", expectedField.Required),
					Actual:   fmt.Sprintf("%t", actualField.Required),
				})
			}
		}

		for _, actualField := range actualSchema.Fields {
			if !hasContractField(expectedSchema.Fields, actualField.Name) {
				conflicts = append(conflicts, ContractValidationConflict{
					Type:     "UNEXPECTED_FIELD",
					Location: actualSchema.Name + "." + actualField.Name,
					Message:  "candidate implementation defines a field not present in the contract",
					Actual:   fmt.Sprintf("%s:%s", actualField.Name, actualField.Type),
				})
			}
		}
	}

	for _, actualSchema := range schemas {
		if !hasContractSchema(contract.Schemas, actualSchema.Name) {
			conflicts = append(conflicts, ContractValidationConflict{
				Type:     "UNEXPECTED_SCHEMA",
				Location: actualSchema.Name,
				Message:  "candidate implementation defines a schema not present in the contract",
				Actual:   actualSchema.Name,
			})
		}
	}

	return conflicts
}

func contractEndpointKey(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

func hasContractEndpoint(items []domain.ContractEndpoint, method, path string) bool {
	expectedKey := contractEndpointKey(method, path)
	for _, item := range items {
		if contractEndpointKey(item.Method, item.Path) == expectedKey {
			return true
		}
	}
	return false
}

func hasContractSchema(items []domain.ContractSchema, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

func hasContractField(items []domain.ContractField, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

func deriveContractResource(req *domain.Requirement, plan *domain.Plan) (string, string) {
	text := strings.ToLower(strings.Join([]string{req.Title, req.Content, plan.Title, plan.Goal}, " "))
	switch {
	case strings.Contains(text, "todo"):
		return "Todo", "todos"
	case strings.Contains(text, "用户") || strings.Contains(text, "user"):
		return "User", "users"
	case strings.Contains(text, "任务") || strings.Contains(text, "task"):
		return "Task", "tasks"
	default:
		return "Item", "items"
	}
}

func deriveScope(content string) []string {
	lower := strings.ToLower(content)
	switch {
	case strings.Contains(content, "增删改查") || strings.Contains(lower, "crud"):
		return []string{
			"梳理需求目标与验收口径",
			"拆出创建、查询、更新、删除四类基本能力",
			"准备最小交付包与 README 模板",
		}
	case strings.Contains(content, "仪表盘") || strings.Contains(content, "dashboard"):
		return []string{
			"定义关键指标与展示范围",
			"输出结构化需求与最小视图范围",
			"生成首轮交付包用于后续实现",
		}
	default:
		return []string{
			"提炼业务目标与 MVP 边界",
			"确认约束、假设与验收标准",
			"生成首轮任务与可下载交付包",
		}
	}
}

func renderBundleReadme(project *domain.Project, task *domain.Task, plan *domain.Plan) string {
	return fmt.Sprintf(`# %s - Sprint 1 Delivery Bundle

此交付包由 MultiAgentCom 的任务执行器生成，用于验证当前开发闭环。

## Project

- Project ID: %s
- Task ID: %s
- Task Type: %s
- Assignee Agent: %s
- Plan Version: v%d

## Goal

%s

## Scope

%s

## Acceptance Criteria

%s

## Bundle Contents

- generated-app/: 占位源码
- metadata/prd.json: 结构化 PRD
- metadata/task.json: 任务快照
- metadata/run.json: 执行快照
- metadata/manifest.json: 交付清单
`, project.Name, project.ID, task.ID, task.Type, task.AssigneeAgent, plan.Version, plan.Goal, renderBulletList(plan.Scope), renderBulletList(plan.AcceptanceCriteria))
}

func renderGeneratedSource(project *domain.Project, plan *domain.Plan) string {
	return fmt.Sprintf(`package main

import "fmt"

func main() {
	fmt.Println("MultiAgentCom generated scaffold")
	fmt.Println("Project: %s")
	fmt.Println("Plan: %s")
	fmt.Println("Goal: %s")
}
`, escapeForDoubleQuotedString(project.Name), escapeForDoubleQuotedString(plan.Title), escapeForDoubleQuotedString(plan.Goal))
}

func renderBulletList(items []string) string {
	if len(items) == 0 {
		return "- N/A"
	}

	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, "- "+item)
	}
	return strings.Join(lines, "\n")
}

func escapeForDoubleQuotedString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func trimSentence(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return "Untitled Requirement"
	}
	if len(input) <= 60 {
		return input
	}
	return input[:60] + "..."
}

func compactStrings(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !slices.Contains(result, item) {
			result = append(result, item)
		}
	}
	return result
}

func resolveRequirementByID(items []*domain.Requirement, requirementID string) (*domain.Requirement, error) {
	for _, item := range items {
		if item.ID == requirementID {
			return item, nil
		}
	}
	return nil, newNotFoundError("requirement not found for plan")
}

func nextID(prefix string) string {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(raw[:]))
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write file %s: %w", path, err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	data = append(data, '\n')
	return writeFile(path, data)
}

func zipDirectory(sourceDir, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}

	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer file.Close()

	archive := zip.NewWriter(file)
	defer archive.Close()

	return filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		relativePath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relativePath)
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()

		_, err = io.Copy(writer, source)
		return err
	})
}

func fileChecksum(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}

	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func cloneProject(project *domain.Project) *domain.Project {
	if project == nil {
		return nil
	}
	copy := *project
	return &copy
}

func cloneRequirement(requirement *domain.Requirement) *domain.Requirement {
	if requirement == nil {
		return nil
	}
	copy := *requirement
	copy.Constraints = append([]string(nil), requirement.Constraints...)
	copy.AcceptanceHints = append([]string(nil), requirement.AcceptanceHints...)
	return &copy
}

func clonePlan(plan *domain.Plan) *domain.Plan {
	if plan == nil {
		return nil
	}
	copy := *plan
	copy.Scope = append([]string(nil), plan.Scope...)
	copy.Constraints = append([]string(nil), plan.Constraints...)
	copy.AcceptanceCriteria = append([]string(nil), plan.AcceptanceCriteria...)
	copy.Assumptions = append([]string(nil), plan.Assumptions...)
	return &copy
}

func cloneContract(contract *domain.Contract) *domain.Contract {
	if contract == nil {
		return nil
	}

	copy := *contract
	copy.Endpoints = append([]domain.ContractEndpoint(nil), contract.Endpoints...)
	copy.Schemas = make([]domain.ContractSchema, 0, len(contract.Schemas))
	for _, schema := range contract.Schemas {
		schemaCopy := schema
		schemaCopy.Fields = append([]domain.ContractField(nil), schema.Fields...)
		copy.Schemas = append(copy.Schemas, schemaCopy)
	}
	return &copy
}

func cloneTask(task *domain.Task) *domain.Task {
	if task == nil {
		return nil
	}
	copy := *task
	copy.DependsOn = append([]string(nil), task.DependsOn...)
	copy.Audit = append([]domain.TaskTransition(nil), task.Audit...)
	return &copy
}

func cloneContextInjection(injection *domain.ContextInjection) *domain.ContextInjection {
	if injection == nil {
		return nil
	}
	copy := *injection
	copy.Sources = append([]domain.ContextSource(nil), injection.Sources...)
	copy.Sections = make([]domain.ContextSection, 0, len(injection.Sections))
	for _, section := range injection.Sections {
		sectionCopy := section
		sectionCopy.Items = append([]string(nil), section.Items...)
		copy.Sections = append(copy.Sections, sectionCopy)
	}
	return &copy
}

func cloneSandbox(sandbox *domain.Sandbox) *domain.Sandbox {
	if sandbox == nil {
		return nil
	}
	copy := *sandbox
	return &copy
}

func cloneTasks(tasks []*domain.Task) []domain.Task {
	result := make([]domain.Task, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, *cloneTask(task))
	}
	return result
}

func cloneRun(run *domain.AgentRun) *domain.AgentRun {
	if run == nil {
		return nil
	}
	copy := *run
	copy.ArtifactIDs = append([]string(nil), run.ArtifactIDs...)
	return &copy
}

func cloneArtifact(artifact *domain.Artifact) *domain.Artifact {
	if artifact == nil {
		return nil
	}
	copy := *artifact
	return &copy
}
