package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"multiagentcom/internal/agentruntime"
	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
	"multiagentcom/internal/store"
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

func newInternalError(code, message string) *AppError {
	return &AppError{Code: code, StatusCode: http.StatusInternalServerError, Message: message}
}

type Service struct {
	cfg               config.Config
	logger            *slog.Logger
	alertClient       *http.Client
	runtimeRegistry   *agentruntime.Registry
	runtimeInitErr    error
	store             store.Store
	workspaceProvider workspaceProvider

	mu             sync.RWMutex
	projects       map[string]*domain.Project
	requirements   map[string][]*domain.Requirement
	planIndex      map[string]*domain.Plan
	plans          map[string][]*domain.Plan
	contractIndex  map[string]*domain.Contract
	contracts      map[string][]*domain.Contract
	contextIndex   map[string]*domain.ContextInjection
	contexts       map[string][]*domain.ContextInjection
	overrideIndex  map[string]*domain.HumanOverride
	overrides      map[string][]*domain.HumanOverride
	lockIndex      map[string]*domain.CodeLock
	locks          map[string][]*domain.CodeLock
	previewIndex   map[string]*domain.Preview
	previews       map[string][]*domain.Preview
	communications map[string][]*domain.CommunicationLog
	auditLogs      map[string][]*domain.AuditLog
	alerts         map[string][]*domain.Alert
	sandboxIndex   map[string]*domain.Sandbox
	sandboxes      map[string][]*domain.Sandbox
	sandboxFaults  map[string]string
	snapshotIndex  map[string]*domain.Snapshot
	snapshots      map[string][]*domain.Snapshot
	snapshotState  map[string]*projectSnapshotState
	projectBranch  map[string]string
	stableBranch   map[string]string
	branchSeq      map[string]int
	tasks          map[string]*domain.Task
	taskOrder      map[string][]string
	runs           map[string]*domain.AgentRun
	runOrder       map[string][]string
	artifacts      map[string]*domain.Artifact
	artifactOrder  map[string][]string
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
	Agent              string `json:"agent"`
	Status             string `json:"status"`
	CreatedTasks       int    `json:"createdTasks"`
	RunningTasks       int    `json:"runningTasks"`
	HumanOverrideTasks int    `json:"humanOverrideTasks"`
	DoneTasks          int    `json:"doneTasks"`
	FailedTasks        int    `json:"failedTasks"`
	TotalTasks         int    `json:"totalTasks"`
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
	OverrideTasks  int                 `json:"overrideTasks"`
	CompletedTasks int                 `json:"completedTasks"`
	FailedTasks    int                 `json:"failedTasks"`
}

type StatusMatrixView struct {
	Projects          []domain.Project      `json:"projects"`
	SelectedProjectID string                `json:"selectedProjectId,omitempty"`
	Matrices          []StatusMatrixProject `json:"matrices"`
	GeneratedAt       time.Time             `json:"generatedAt"`
}

type TokenCostPoint struct {
	RunID            string           `json:"runId"`
	TaskID           string           `json:"taskId"`
	TaskName         string           `json:"taskName"`
	AgentType        string           `json:"agentType"`
	Status           domain.RunStatus `json:"status"`
	PromptTokens     int              `json:"promptTokens"`
	CompletionTokens int              `json:"completionTokens"`
	TotalTokens      int              `json:"totalTokens"`
	EstimatedCostUSD float64          `json:"estimatedCostUsd"`
	Timestamp        time.Time        `json:"timestamp"`
}

type TokenCostTrend struct {
	ProjectID             string           `json:"projectId"`
	TaskID                string           `json:"taskId,omitempty"`
	TotalPromptTokens     int              `json:"totalPromptTokens"`
	TotalCompletionTokens int              `json:"totalCompletionTokens"`
	TotalTokens           int              `json:"totalTokens"`
	EstimatedCostUSD      float64          `json:"estimatedCostUsd"`
	MaxTokens             int              `json:"maxTokens"`
	BudgetWarnUSD         float64          `json:"budgetWarnUsd,omitempty"`
	BudgetBlockUSD        float64          `json:"budgetBlockUsd,omitempty"`
	BudgetStatus          string           `json:"budgetStatus"`
	Points                []TokenCostPoint `json:"points"`
}

type auditActorKey struct{}

func WithActor(ctx context.Context, actor string) context.Context {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, auditActorKey{}, actor)
}

func ActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	actor, _ := ctx.Value(auditActorKey{}).(string)
	return strings.TrimSpace(actor)
}

type SandboxView struct {
	Sandbox domain.Sandbox  `json:"sandbox"`
	Run     domain.AgentRun `json:"run"`
	Task    domain.Task     `json:"task"`
}

type InjectSandboxFailureInput struct {
	Reason string `json:"reason"`
}

type ApplyHumanOverrideInput struct {
	TaskID      string `json:"taskId"`
	Operator    string `json:"operator"`
	Instruction string `json:"instruction"`
	LockScope   string `json:"lockScope"`
}

type HumanOverrideResult struct {
	Override domain.HumanOverride `json:"override"`
	Task     domain.Task          `json:"task"`
	Run      *domain.AgentRun     `json:"run,omitempty"`
	Message  string               `json:"message,omitempty"`
}

type ApplyCodeLockInput struct {
	TaskID     string `json:"taskId"`
	Path       string `json:"path"`
	Content    string `json:"content"`
	LockMode   string `json:"lockMode"`
	Language   string `json:"language"`
	SymbolKind string `json:"symbolKind"`
	SymbolName string `json:"symbolName"`
	CreatedBy  string `json:"createdBy"`
}

type CodeLockResult struct {
	Lock    domain.CodeLock `json:"lock"`
	Message string          `json:"message,omitempty"`
}

type PreviewStartResult struct {
	Preview           domain.Preview `json:"preview"`
	RefreshIntervalMs int            `json:"refreshIntervalMs"`
}

type MergeSharedSandboxInput struct {
	TaskIDs         []string                  `json:"taskIds"`
	ContractID      string                    `json:"contractId"`
	Endpoints       []domain.ContractEndpoint `json:"endpoints"`
	Schemas         []domain.ContractSchema   `json:"schemas"`
	SimulateFailure bool                      `json:"simulateFailure"`
}

type SharedSandboxMergeResult struct {
	Sandbox           domain.Sandbox               `json:"sandbox"`
	Passed            bool                         `json:"passed"`
	ContractConflicts []ContractValidationConflict `json:"contractConflicts,omitempty"`
	RemediationTask   *domain.Task                 `json:"remediationTask,omitempty"`
	Rollback          *RollbackResult              `json:"rollback,omitempty"`
	ArtifactIDs       []string                     `json:"artifactIds,omitempty"`
	Cleanup           *CleanupWorkspacesResult     `json:"cleanup,omitempty"`
	Message           string                       `json:"message,omitempty"`
}

type CleanupWorkspacesInput struct {
	DryRun           bool     `json:"dryRun"`
	Scope            string   `json:"scope,omitempty"`
	SandboxIDs       []string `json:"sandboxIds,omitempty"`
	DeleteBranches   bool     `json:"deleteBranches,omitempty"`
	IncludeFailed    bool     `json:"includeFailed,omitempty"`
	OlderThanSeconds int64    `json:"olderThanSeconds,omitempty"`
}

type WorkspaceCleanupResult struct {
	SandboxID       string `json:"sandboxId"`
	Scope           string `json:"scope"`
	Provider        string `json:"provider"`
	Status          string `json:"status"`
	Reason          string `json:"reason,omitempty"`
	Error           string `json:"error,omitempty"`
	WorktreeRemoved bool   `json:"worktreeRemoved"`
	BranchDeleted   bool   `json:"branchDeleted"`
	RetainedRef     string `json:"retainedRef,omitempty"`
}

type CleanupWorkspacesResult struct {
	ProjectID        string                   `json:"projectId"`
	DryRun           bool                     `json:"dryRun"`
	Results          []WorkspaceCleanupResult `json:"results"`
	RemovedWorktrees int                      `json:"removedWorktrees"`
	DeletedBranches  int                      `json:"deletedBranches"`
	Skipped          int                      `json:"skipped"`
	Failed           int                      `json:"failed"`
}

type RebaseWorkspacesInput struct {
	DryRun     bool     `json:"dryRun"`
	All        bool     `json:"all,omitempty"`
	SandboxIDs []string `json:"sandboxIds,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	TargetRef  string   `json:"targetRef"`
	Fetch      bool     `json:"fetch,omitempty"`
	Publish    bool     `json:"publish,omitempty"`
}

type WorkspaceRebaseResult struct {
	SandboxID      string `json:"sandboxId"`
	Scope          string `json:"scope"`
	Provider       string `json:"provider"`
	Status         string `json:"status"`
	Reason         string `json:"reason,omitempty"`
	Branch         string `json:"branch,omitempty"`
	TargetRef      string `json:"targetRef,omitempty"`
	OldHeadRef     string `json:"oldHeadRef,omitempty"`
	NewHeadRef     string `json:"newHeadRef,omitempty"`
	Ahead          int    `json:"ahead,omitempty"`
	Behind         int    `json:"behind,omitempty"`
	Fetched        bool   `json:"fetched,omitempty"`
	RebaseAborted  bool   `json:"rebaseAborted,omitempty"`
	Published      bool   `json:"published,omitempty"`
	ConflictLog    string `json:"conflictLog,omitempty"`
	ConflictLogRef string `json:"conflictLogRef,omitempty"`
	Error          string `json:"error,omitempty"`
}

type RebaseWorkspacesResult struct {
	ProjectID       string                  `json:"projectId"`
	DryRun          bool                    `json:"dryRun"`
	TargetRef       string                  `json:"targetRef"`
	Results         []WorkspaceRebaseResult `json:"results"`
	Rebased         int                     `json:"rebased"`
	AlreadyUpToDate int                     `json:"alreadyUpToDate"`
	Skipped         int                     `json:"skipped"`
	Failed          int                     `json:"failed"`
	Published       int                     `json:"published"`
}

type RollbackSnapshotInput struct {
	SnapshotID string `json:"snapshotId"`
	Reason     string `json:"reason"`
}

type RollbackWorkspaceResult struct {
	Restored    bool           `json:"restored"`
	Sandbox     domain.Sandbox `json:"sandbox"`
	Branch      string         `json:"branch"`
	HeadRef     string         `json:"headRef"`
	StateRef    string         `json:"stateRef"`
	OriginalRef string         `json:"originalRef"`
}

type RollbackResult struct {
	Snapshot        domain.Snapshot          `json:"snapshot"`
	RestoredFrom    domain.Snapshot          `json:"restoredFrom"`
	PreviousBranch  string                   `json:"previousBranch"`
	ActiveBranch    string                   `json:"activeBranch"`
	ClearedContexts int                      `json:"clearedContexts"`
	RestoredTasks   int                      `json:"restoredTasks"`
	Workspace       *RollbackWorkspaceResult `json:"workspace,omitempty"`
	Message         string                   `json:"message,omitempty"`
}

type workspaceSnapshotRef struct {
	Provider string
	Branch   string
	Commit   string
}

type projectSnapshotState struct {
	Project       *domain.Project                       `json:"project,omitempty"`
	Requirements  []*domain.Requirement                 `json:"requirements,omitempty"`
	Plans         []*domain.Plan                        `json:"plans,omitempty"`
	Contracts     []*domain.Contract                    `json:"contracts,omitempty"`
	Contexts      map[string][]*domain.ContextInjection `json:"contexts,omitempty"`
	Sandboxes     []*domain.Sandbox                     `json:"sandboxes,omitempty"`
	Tasks         map[string]*domain.Task               `json:"tasks,omitempty"`
	TaskOrder     []string                              `json:"taskOrder,omitempty"`
	Runs          map[string]*domain.AgentRun           `json:"runs,omitempty"`
	RunOrder      []string                              `json:"runOrder,omitempty"`
	Artifacts     map[string]*domain.Artifact           `json:"artifacts,omitempty"`
	ArtifactOrder []string                              `json:"artifactOrder,omitempty"`
}

type snapshotManifest struct {
	SchemaVersion     int       `json:"schemaVersion"`
	ID                string    `json:"id"`
	ProjectID         string    `json:"projectId"`
	Branch            string    `json:"branch"`
	SourceSnapshotID  string    `json:"sourceSnapshotId,omitempty"`
	Reason            string    `json:"reason"`
	Stable            bool      `json:"stable"`
	StateRef          string    `json:"stateRef"`
	Checksum          string    `json:"checksum"`
	WorkspaceStateRef string    `json:"workspaceStateRef,omitempty"`
	WorkspaceChecksum string    `json:"workspaceChecksum,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

type persistedServiceState struct {
	Version        int                                   `json:"version"`
	Projects       map[string]*domain.Project            `json:"projects,omitempty"`
	Requirements   map[string][]*domain.Requirement      `json:"requirements,omitempty"`
	Plans          map[string][]*domain.Plan             `json:"plans,omitempty"`
	Contracts      map[string][]*domain.Contract         `json:"contracts,omitempty"`
	Contexts       map[string][]*domain.ContextInjection `json:"contexts,omitempty"`
	Overrides      map[string][]*domain.HumanOverride    `json:"overrides,omitempty"`
	Locks          map[string][]*domain.CodeLock         `json:"locks,omitempty"`
	Previews       map[string][]*domain.Preview          `json:"previews,omitempty"`
	Communications map[string][]*domain.CommunicationLog `json:"communications,omitempty"`
	AuditLogs      map[string][]*domain.AuditLog         `json:"auditLogs,omitempty"`
	Alerts         map[string][]*domain.Alert            `json:"alerts,omitempty"`
	Sandboxes      map[string][]*domain.Sandbox          `json:"sandboxes,omitempty"`
	SandboxFaults  map[string]string                     `json:"sandboxFaults,omitempty"`
	Snapshots      map[string][]*domain.Snapshot         `json:"snapshots,omitempty"`
	SnapshotState  map[string]*projectSnapshotState      `json:"snapshotState,omitempty"`
	ProjectBranch  map[string]string                     `json:"projectBranch,omitempty"`
	StableBranch   map[string]string                     `json:"stableBranch,omitempty"`
	BranchSeq      map[string]int                        `json:"branchSeq,omitempty"`
	Tasks          map[string]*domain.Task               `json:"tasks,omitempty"`
	TaskOrder      map[string][]string                   `json:"taskOrder,omitempty"`
	Runs           map[string]*domain.AgentRun           `json:"runs,omitempty"`
	RunOrder       map[string][]string                   `json:"runOrder,omitempty"`
	Artifacts      map[string]*domain.Artifact           `json:"artifacts,omitempty"`
	ArtifactOrder  map[string][]string                   `json:"artifactOrder,omitempty"`
}

func New(cfg config.Config, logger *slog.Logger) *Service {
	if strings.TrimSpace(cfg.ArtifactRoot) == "" {
		cfg.ArtifactRoot = filepath.Join(os.TempDir(), "multiagentcom", "artifacts")
	}
	if strings.TrimSpace(cfg.SandboxRoot) == "" {
		cfg.SandboxRoot = filepath.Join(os.TempDir(), "multiagentcom", "sandboxes")
	}
	if strings.TrimSpace(cfg.StoreProvider) == "" {
		cfg.StoreProvider = "memory"
	}
	if strings.TrimSpace(cfg.DataRoot) == "" {
		cfg.DataRoot = filepath.Join(os.TempDir(), "multiagentcom", "data")
	}
	if strings.TrimSpace(cfg.WorkspaceProvider) == "" {
		cfg.WorkspaceProvider = "directory"
	}
	if cfg.RuntimeTimeout == 0 {
		cfg.RuntimeTimeout = 30 * time.Second
	}
	if cfg.TokenPromptPricePerMillion == 0 {
		cfg.TokenPromptPricePerMillion = 1.5
	}
	if cfg.TokenOutputPricePerMillion == 0 {
		cfg.TokenOutputPricePerMillion = 2.5
	}

	runtimeRegistry := agentruntime.NewRegistry()
	_ = runtimeRegistry.Register("local", agentruntime.NewMockRunner())

	runtimeProvider := strings.TrimSpace(cfg.RuntimeProvider)
	if runtimeProvider == "" {
		runtimeProvider = "local"
	}
	explicitRuntimeProvider := strings.TrimSpace(cfg.RuntimeProvider) != ""
	var runtimeInitErr error

	if endpoint := strings.TrimSpace(cfg.RuntimeEndpoint); endpoint != "" {
		options := agentruntime.HTTPRunnerOptions{
			BearerToken:    cfg.RuntimeHTTPBearerToken,
			MaxAttempts:    cfg.RuntimeHTTPMaxAttempts,
			RetryBaseDelay: cfg.RuntimeHTTPRetryBaseDelay,
		}
		if runner, err := agentruntime.NewHTTPRunnerWithOptions(endpoint, &http.Client{Timeout: cfg.RuntimeTimeout}, options); err != nil {
			runtimeInitErr = fmt.Errorf("initialize http runtime runner: %w", err)
			if logger != nil {
				logger.Warn("failed to initialize http runtime runner", "endpoint", endpoint, "error", err)
			}
		} else if err := runtimeRegistry.Register("http", runner); err != nil {
			runtimeInitErr = fmt.Errorf("register http runtime runner: %w", err)
			if logger != nil {
				logger.Warn("failed to register http runtime runner", "endpoint", endpoint, "error", err)
			}
		}
	}

	if err := runtimeRegistry.SetDefault(runtimeProvider); err != nil {
		if explicitRuntimeProvider {
			runtimeInitErr = fmt.Errorf("configured runtime provider %q unavailable: %w", runtimeProvider, err)
			cfg.RuntimeProvider = runtimeProvider
		} else {
			if logger != nil {
				logger.Warn("configured runtime provider unavailable, falling back to local", "provider", runtimeProvider, "error", err)
			}
			_ = runtimeRegistry.SetDefault("local")
			cfg.RuntimeProvider = "local"
		}
	} else {
		cfg.RuntimeProvider = runtimeProvider
	}

	svc := &Service{
		cfg:               cfg,
		logger:            logger,
		alertClient:       &http.Client{Timeout: 3 * time.Second},
		runtimeRegistry:   runtimeRegistry,
		runtimeInitErr:    runtimeInitErr,
		store:             newServiceStore(cfg),
		workspaceProvider: newWorkspaceProvider(cfg),
	}
	svc.resetStateLocked()
	if err := svc.loadPersistedState(context.Background()); err != nil && logger != nil {
		logger.Warn("failed to load persisted service state", "error", err)
	}
	return svc
}

func newServiceStore(cfg config.Config) store.Store {
	switch strings.ToLower(strings.TrimSpace(cfg.StoreProvider)) {
	case "file":
		return store.NewFileStore(cfg.DataRoot)
	default:
		return store.NewMemoryStore()
	}
}

func (s *Service) resetStateLocked() {
	s.projects = make(map[string]*domain.Project)
	s.requirements = make(map[string][]*domain.Requirement)
	s.planIndex = make(map[string]*domain.Plan)
	s.plans = make(map[string][]*domain.Plan)
	s.contractIndex = make(map[string]*domain.Contract)
	s.contracts = make(map[string][]*domain.Contract)
	s.contextIndex = make(map[string]*domain.ContextInjection)
	s.contexts = make(map[string][]*domain.ContextInjection)
	s.overrideIndex = make(map[string]*domain.HumanOverride)
	s.overrides = make(map[string][]*domain.HumanOverride)
	s.lockIndex = make(map[string]*domain.CodeLock)
	s.locks = make(map[string][]*domain.CodeLock)
	s.previewIndex = make(map[string]*domain.Preview)
	s.previews = make(map[string][]*domain.Preview)
	s.communications = make(map[string][]*domain.CommunicationLog)
	s.auditLogs = make(map[string][]*domain.AuditLog)
	s.alerts = make(map[string][]*domain.Alert)
	s.sandboxIndex = make(map[string]*domain.Sandbox)
	s.sandboxes = make(map[string][]*domain.Sandbox)
	s.sandboxFaults = make(map[string]string)
	s.snapshotIndex = make(map[string]*domain.Snapshot)
	s.snapshots = make(map[string][]*domain.Snapshot)
	s.snapshotState = make(map[string]*projectSnapshotState)
	s.projectBranch = make(map[string]string)
	s.stableBranch = make(map[string]string)
	s.branchSeq = make(map[string]int)
	s.tasks = make(map[string]*domain.Task)
	s.taskOrder = make(map[string][]string)
	s.runs = make(map[string]*domain.AgentRun)
	s.runOrder = make(map[string][]string)
	s.artifacts = make(map[string]*domain.Artifact)
	s.artifactOrder = make(map[string][]string)
}

func (s *Service) loadPersistedState(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	payload, err := s.store.Load(ctx)
	if err != nil || len(payload) == 0 {
		return err
	}

	var state persistedServiceState
	if err := json.Unmarshal(payload, &state); err != nil {
		return err
	}
	if state.Version != 1 {
		return fmt.Errorf("unsupported service state version %d", state.Version)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.restorePersistedStateLocked(&state)
	return nil
}

func (s *Service) persistLocked() {
	if s.store == nil {
		return
	}
	payload, err := json.MarshalIndent(s.capturePersistedStateLocked(), "", "  ")
	if err != nil {
		s.logPersistError(err)
		return
	}
	if err := s.store.Save(context.Background(), payload); err != nil {
		s.logPersistError(err)
	}
}

func (s *Service) logPersistError(err error) {
	if err != nil && s.logger != nil {
		s.logger.Error("failed to persist service state", "error", err)
	}
}

func (s *Service) capturePersistedStateLocked() *persistedServiceState {
	state := &persistedServiceState{
		Version:        1,
		Projects:       cloneProjectMap(s.projects),
		Requirements:   cloneRequirementMap(s.requirements),
		Plans:          clonePlanMap(s.plans),
		Contracts:      cloneContractMap(s.contracts),
		Contexts:       cloneContextMap(s.contexts),
		Overrides:      cloneOverrideMap(s.overrides),
		Locks:          cloneLockMap(s.locks),
		Previews:       clonePreviewMap(s.previews),
		Communications: cloneCommunicationMap(s.communications),
		AuditLogs:      cloneAuditMap(s.auditLogs),
		Alerts:         cloneAlertMap(s.alerts),
		Sandboxes:      cloneSandboxMap(s.sandboxes),
		SandboxFaults:  cloneStringMap(s.sandboxFaults),
		Snapshots:      cloneSnapshotMap(s.snapshots),
		SnapshotState:  cloneSnapshotStateMap(s.snapshotState),
		ProjectBranch:  cloneStringMap(s.projectBranch),
		StableBranch:   cloneStringMap(s.stableBranch),
		BranchSeq:      cloneIntMap(s.branchSeq),
		Tasks:          cloneTaskMap(s.tasks),
		TaskOrder:      cloneStringSliceMap(s.taskOrder),
		Runs:           cloneRunMap(s.runs),
		RunOrder:       cloneStringSliceMap(s.runOrder),
		Artifacts:      cloneArtifactMap(s.artifacts),
		ArtifactOrder:  cloneStringSliceMap(s.artifactOrder),
	}
	return state
}

func (s *Service) restorePersistedStateLocked(state *persistedServiceState) {
	s.resetStateLocked()
	s.projects = cloneProjectMap(state.Projects)
	s.requirements = cloneRequirementMap(state.Requirements)
	s.plans = clonePlanMap(state.Plans)
	s.contracts = cloneContractMap(state.Contracts)
	s.contexts = cloneContextMap(state.Contexts)
	s.overrides = cloneOverrideMap(state.Overrides)
	s.locks = cloneLockMap(state.Locks)
	s.previews = clonePreviewMap(state.Previews)
	s.communications = cloneCommunicationMap(state.Communications)
	s.auditLogs = cloneAuditMap(state.AuditLogs)
	s.alerts = cloneAlertMap(state.Alerts)
	s.sandboxes = cloneSandboxMap(state.Sandboxes)
	s.sandboxFaults = cloneStringMap(state.SandboxFaults)
	s.snapshots = cloneSnapshotMap(state.Snapshots)
	s.snapshotState = cloneSnapshotStateMap(state.SnapshotState)
	s.projectBranch = cloneStringMap(state.ProjectBranch)
	s.stableBranch = cloneStringMap(state.StableBranch)
	s.branchSeq = cloneIntMap(state.BranchSeq)
	s.tasks = cloneTaskMap(state.Tasks)
	s.taskOrder = cloneStringSliceMap(state.TaskOrder)
	s.runs = cloneRunMap(state.Runs)
	s.runOrder = cloneStringSliceMap(state.RunOrder)
	s.artifacts = cloneArtifactMap(state.Artifacts)
	s.artifactOrder = cloneStringSliceMap(state.ArtifactOrder)
	s.rebuildIndexesLocked()
}

func (s *Service) rebuildIndexesLocked() {
	for _, items := range s.plans {
		for _, plan := range items {
			if plan != nil {
				s.planIndex[plan.ID] = plan
			}
		}
	}
	for _, items := range s.contracts {
		for _, contract := range items {
			if contract != nil {
				s.contractIndex[contract.ID] = contract
			}
		}
	}
	for _, items := range s.contexts {
		for _, contextInjection := range items {
			if contextInjection != nil {
				s.contextIndex[contextInjection.ID] = contextInjection
			}
		}
	}
	for _, items := range s.overrides {
		for _, override := range items {
			if override != nil {
				s.overrideIndex[override.ID] = override
			}
		}
	}
	for _, items := range s.locks {
		for _, lock := range items {
			if lock != nil {
				s.lockIndex[lock.ID] = lock
			}
		}
	}
	for _, items := range s.previews {
		for _, preview := range items {
			if preview != nil {
				s.previewIndex[preview.ID] = preview
			}
		}
	}
	for _, items := range s.sandboxes {
		for _, sandbox := range items {
			if sandbox != nil {
				s.sandboxIndex[sandbox.ID] = sandbox
			}
		}
	}
	for _, items := range s.snapshots {
		for _, snapshot := range items {
			if snapshot != nil {
				s.snapshotIndex[snapshot.ID] = snapshot
			}
		}
	}
}

func (s *Service) usesFileStore() bool {
	return strings.EqualFold(strings.TrimSpace(s.cfg.StoreProvider), "file")
}

func (s *Service) snapshotRoot(snapshot *domain.Snapshot) string {
	return filepath.Join(s.cfg.DataRoot, "snapshots", snapshot.ProjectID, snapshot.ID)
}

func (s *Service) writeSnapshotRecord(snapshot *domain.Snapshot, state *projectSnapshotState) (string, string, error) {
	statePath := filepath.Join(s.snapshotRoot(snapshot), "state.json")
	statePayload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal snapshot state: %w", err)
	}
	statePayload = append(statePayload, '\n')
	if err := writeFile(statePath, statePayload); err != nil {
		return "", "", err
	}
	checksum := sha256.Sum256(statePayload)
	stateRef := "file://" + statePath
	manifest := snapshotManifest{
		SchemaVersion:     1,
		ID:                snapshot.ID,
		ProjectID:         snapshot.ProjectID,
		Branch:            snapshot.Branch,
		SourceSnapshotID:  snapshot.SourceSnapshotID,
		Reason:            snapshot.Reason,
		Stable:            snapshot.Stable,
		StateRef:          stateRef,
		Checksum:          hex.EncodeToString(checksum[:]),
		WorkspaceStateRef: snapshot.WorkspaceStateRef,
		WorkspaceChecksum: snapshot.WorkspaceChecksum,
		CreatedAt:         snapshot.CreatedAt,
	}
	if err := writeJSONFile(filepath.Join(s.snapshotRoot(snapshot), "manifest.json"), manifest); err != nil {
		return "", "", err
	}
	return stateRef, manifest.Checksum, nil
}

func (s *Service) runtimeProviderName() string {
	provider := strings.TrimSpace(s.cfg.RuntimeProvider)
	if provider != "" {
		return provider
	}
	if s.runtimeRegistry != nil {
		if fallback := strings.TrimSpace(s.runtimeRegistry.DefaultProvider()); fallback != "" {
			return fallback
		}
	}
	return "local"
}

func (s *Service) CreateProject(ctx context.Context, input CreateProjectInput) (*domain.Project, error) {
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
	s.projectBranch[project.ID] = "main"
	s.recordAuditLocked(ctx, project.ID, "PROJECT_CREATE", "project", project.ID, "project created", now)
	s.persistLocked()
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
		view := SandboxView{Sandbox: *cloneSandbox(sandbox)}
		if run, ok := s.runs[sandbox.RunID]; ok {
			view.Run = *cloneRun(run)
		}
		if task, ok := s.tasks[sandbox.TaskID]; ok {
			view.Task = *cloneTask(task)
		}
		result = append(result, view)
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

func (s *Service) ListSnapshots(_ context.Context, projectID string) ([]domain.Snapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.snapshots[projectID]
	result := make([]domain.Snapshot, 0, len(items))
	for _, snapshot := range items {
		result = append(result, *cloneSnapshot(snapshot))
	}

	return result, nil
}

func (s *Service) ListCommunicationLogs(_ context.Context, projectID, taskID string) ([]domain.CommunicationLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.communications[projectID]
	result := make([]domain.CommunicationLog, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		if taskID != "" && item.TaskID != taskID {
			continue
		}
		result = append(result, *cloneCommunicationLog(item))
	}

	return result, nil
}

func (s *Service) ListAuditLogs(_ context.Context, projectID string) ([]domain.AuditLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.auditLogs[projectID]
	result := make([]domain.AuditLog, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, *cloneAuditLog(item))
	}

	return result, nil
}

func (s *Service) ListAlerts(_ context.Context, projectID string) ([]domain.Alert, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	items := s.alerts[projectID]
	result := make([]domain.Alert, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		result = append(result, *cloneAlert(item))
	}

	return result, nil
}

func (s *Service) GetTokenCostTrend(_ context.Context, projectID, taskID string) (*TokenCostTrend, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}

	result := &TokenCostTrend{
		ProjectID:      strings.TrimSpace(projectID),
		TaskID:         strings.TrimSpace(taskID),
		BudgetWarnUSD:  s.cfg.TokenBudgetWarnUSD,
		BudgetBlockUSD: s.cfg.TokenBudgetBlockUSD,
		BudgetStatus:   "ok",
		Points:         make([]TokenCostPoint, 0),
	}
	for _, runID := range s.runOrder[projectID] {
		run, ok := s.runs[runID]
		if !ok || run == nil {
			continue
		}
		if result.TaskID != "" && run.TaskID != result.TaskID {
			continue
		}
		if run.TotalTokens <= 0 {
			continue
		}

		taskName := run.TaskID
		if task, ok := s.tasks[run.TaskID]; ok && task != nil && strings.TrimSpace(task.Name) != "" {
			taskName = task.Name
		}
		timestamp := run.EndedAt
		if timestamp.IsZero() {
			timestamp = run.StartedAt
		}

		point := TokenCostPoint{
			RunID:            run.ID,
			TaskID:           run.TaskID,
			TaskName:         taskName,
			AgentType:        run.AgentType,
			Status:           run.Status,
			PromptTokens:     run.PromptTokens,
			CompletionTokens: run.CompletionTokens,
			TotalTokens:      run.TotalTokens,
			EstimatedCostUSD: run.EstimatedCostUSD,
			Timestamp:        timestamp,
		}
		result.Points = append(result.Points, point)
		result.TotalPromptTokens += point.PromptTokens
		result.TotalCompletionTokens += point.CompletionTokens
		result.TotalTokens += point.TotalTokens
		result.EstimatedCostUSD += point.EstimatedCostUSD
		if point.TotalTokens > result.MaxTokens {
			result.MaxTokens = point.TotalTokens
		}
	}
	result.BudgetStatus = budgetStatus(result.EstimatedCostUSD, result.BudgetWarnUSD, result.BudgetBlockUSD)

	return result, nil
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

func (s *Service) AddRequirement(ctx context.Context, projectID string, input AddRequirementInput) (*domain.Requirement, error) {
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
	s.recordAuditLocked(ctx, projectID, "REQUIREMENT_ADD", "requirement", req.ID, "requirement added", now)
	s.persistLocked()

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

func (s *Service) GeneratePlan(ctx context.Context, projectID string) (*PlanResult, error) {
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
	s.recordAuditLocked(ctx, projectID, "PLAN_GENERATE", "plan", plan.ID, "plan generated from latest requirement", now)
	s.persistLocked()

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
	s.persistLocked()

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
	s.recordCommunicationLocked(projectID, "context-engine", task.AssigneeAgent, "CONTEXT_INJECTION", task.ID, "context://"+injection.ID, now)
	s.persistLocked()

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

func (s *Service) ApplyHumanOverride(ctx context.Context, projectID string, input ApplyHumanOverrideInput) (*HumanOverrideResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	task, err := s.resolveTaskLocked(projectID, input.TaskID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Instruction) == "" {
		return nil, newValidationError("instruction is required for human override")
	}
	if task.Status != domain.TaskStatusInProgress && task.Status != domain.TaskStatusHumanOverride {
		return nil, newConflictError("only in-progress tasks can receive human override")
	}

	override := &domain.HumanOverride{
		ID:          nextID("override"),
		ProjectID:   projectID,
		TaskID:      task.ID,
		Operator:    strings.TrimSpace(input.Operator),
		Instruction: strings.TrimSpace(input.Instruction),
		LockScope:   strings.TrimSpace(input.LockScope),
		CreatedAt:   now,
	}
	if override.Operator == "" {
		override.Operator = "human-operator"
	}

	if task.Status == domain.TaskStatusInProgress {
		if err := task.TransitionTo(domain.TaskStatusHumanOverride, "human override requested by "+override.Operator, now); err != nil {
			return nil, newConflictError(err.Error())
		}
	}

	s.overrideIndex[override.ID] = override
	s.overrides[projectID] = append(s.overrides[projectID], override)
	s.recordCommunicationLocked(projectID, "human:"+override.Operator, task.AssigneeAgent, "HUMAN_OVERRIDE", task.ID, "override://"+override.ID, now)
	s.recordAuditLocked(ctx, projectID, "HUMAN_OVERRIDE_APPLY", "override", override.ID, "human override queued for task "+task.ID, now)
	project.UpdatedAt = now

	result := &HumanOverrideResult{
		Override: *cloneHumanOverride(override),
		Task:     *cloneTask(task),
		Message:  "human override queued for next safety checkpoint",
	}
	if run := s.findActiveRunForTaskLocked(projectID, task.ID); run != nil {
		result.Run = cloneRun(run)
	}
	s.persistLocked()

	return result, nil
}

func (s *Service) ApplyCodeLock(ctx context.Context, projectID string, input ApplyCodeLockInput) (*CodeLockResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	lockPath := strings.TrimSpace(input.Path)
	lockContent := input.Content
	if lockPath == "" {
		return nil, newValidationError("path is required for code lock")
	}
	if filepath.IsAbs(lockPath) || strings.Contains(lockPath, "..") {
		return nil, newValidationError("path must be a relative bundle path")
	}
	if strings.TrimSpace(lockContent) == "" {
		return nil, newValidationError("content is required for code lock")
	}
	if !strings.Contains(lockContent, "LOCKED BY HUMAN") {
		return nil, newValidationError("content must include LOCKED BY HUMAN marker")
	}
	lockMode := normalizeLockMode(input.LockMode)
	if lockMode == "" {
		return nil, newValidationError("lockMode must be file or go_symbol")
	}
	symbolKind := strings.TrimSpace(input.SymbolKind)
	symbolName := strings.TrimSpace(input.SymbolName)
	language := strings.TrimSpace(input.Language)
	if err := validateCodeLock(lockPath, lockContent, lockMode, language, symbolKind, symbolName); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.TaskID) != "" {
		if _, err := s.resolveTaskLocked(projectID, input.TaskID); err != nil {
			return nil, err
		}
	}

	lock := &domain.CodeLock{
		ID:         nextID("lock"),
		ProjectID:  projectID,
		TaskID:     strings.TrimSpace(input.TaskID),
		Path:       filepath.ToSlash(lockPath),
		Content:    lockContent,
		LockMode:   lockMode,
		Language:   language,
		SymbolKind: symbolKind,
		SymbolName: symbolName,
		CreatedBy:  strings.TrimSpace(input.CreatedBy),
		CreatedAt:  now,
	}
	if lock.CreatedBy == "" {
		lock.CreatedBy = "human-operator"
	}

	s.lockIndex[lock.ID] = lock
	s.locks[projectID] = append(s.locks[projectID], lock)
	s.recordCommunicationLocked(projectID, "human:"+lock.CreatedBy, "delivery-engine", "CODE_LOCK", lock.TaskID, "lock://"+lock.ID, now)
	s.recordAuditLocked(ctx, projectID, "CODE_LOCK_APPLY", "code_lock", lock.ID, "code lock registered for "+lock.Path, now)
	project.UpdatedAt = now
	s.persistLocked()

	return &CodeLockResult{
		Lock:    *cloneCodeLock(lock),
		Message: "code lock registered and will be preserved on future bundle generations",
	}, nil
}

func (s *Service) StartPreview(ctx context.Context, projectID string) (*PreviewStartResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}

	sandbox, err := s.resolveLatestReleasedSharedSandboxLocked(projectID)
	if err != nil {
		return nil, err
	}

	preview := &domain.Preview{
		ID:        nextID("preview"),
		ProjectID: projectID,
		SandboxID: sandbox.ID,
		Status:    "READY",
		Revision:  fmt.Sprintf("%s-%d", sandbox.ID, sandbox.UpdatedAt.UnixNano()),
		CreatedAt: now,
		UpdatedAt: now,
	}
	preview.URL = "/projects/" + projectID + "/preview/" + preview.ID

	s.previewIndex[preview.ID] = preview
	s.previews[projectID] = append(s.previews[projectID], preview)
	s.recordAuditLocked(ctx, projectID, "PREVIEW_START", "preview", preview.ID, "preview started from shared sandbox "+sandbox.ID, now)
	project.UpdatedAt = now
	s.persistLocked()

	return &PreviewStartResult{
		Preview:           *clonePreview(preview),
		RefreshIntervalMs: 3000,
	}, nil
}

func (s *Service) GetPreview(_ context.Context, projectID, previewID string) (*domain.Preview, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	preview, ok := s.previewIndex[previewID]
	if !ok || preview.ProjectID != projectID {
		return nil, newNotFoundError("preview not found")
	}

	return clonePreview(preview), nil
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
	s.persistLocked()
	return nil
}

func (s *Service) MergeToSharedSandbox(ctx context.Context, projectID string, input MergeSharedSandboxInput) (*SharedSandboxMergeResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok := s.projects[projectID]
	if !ok {
		return nil, newNotFoundError("project not found")
	}
	if len(input.TaskIDs) == 0 {
		return nil, newValidationError("taskIds are required for shared sandbox merge")
	}

	tasks, artifactIDs, err := s.resolveMergeTasksLocked(projectID, input.TaskIDs)
	if err != nil {
		return nil, err
	}

	var contract *domain.Contract
	if input.ContractID != "" || len(input.Endpoints) > 0 || len(input.Schemas) > 0 {
		contract, err = s.resolveContractLocked(projectID, input.ContractID)
		if err != nil {
			return nil, err
		}
	} else {
		contracts := s.contracts[projectID]
		if len(contracts) > 0 {
			contract = contracts[len(contracts)-1]
		}
	}

	sharedSandbox, err := s.createSharedSandboxLocked(projectID, tasks, now)
	if err != nil {
		return nil, err
	}
	project.UpdatedAt = now

	result := &SharedSandboxMergeResult{
		Sandbox:     *cloneSandbox(sharedSandbox),
		ArtifactIDs: append([]string(nil), artifactIDs...),
	}

	if contract != nil && (len(input.Endpoints) > 0 || len(input.Schemas) > 0) {
		conflicts := validateContractDefinition(contract, input.Endpoints, input.Schemas)
		if len(conflicts) > 0 {
			sharedSandbox.Status = domain.SandboxStatusFailed
			sharedSandbox.FailureReason = "contract validation failed in shared sandbox gate"
			sharedSandbox.UpdatedAt = time.Now().UTC()
			remediationTask := s.createContractRemediationTaskLocked(projectID, contract, sharedSandbox.UpdatedAt)
			result.Sandbox = *cloneSandbox(sharedSandbox)
			result.ContractConflicts = conflicts
			result.RemediationTask = cloneTask(remediationTask)
			result.Message = "merge blocked by contract conflicts"
			s.recordAuditLocked(ctx, projectID, "SHARED_SANDBOX_MERGE_BLOCKED", "sandbox", sharedSandbox.ID, result.Message, sharedSandbox.UpdatedAt)
			s.persistLocked()
			return result, nil
		}
	}

	if err := s.writeSharedSandboxManifest(sharedSandbox, tasks, artifactIDs); err != nil {
		sharedSandbox.Status = domain.SandboxStatusFailed
		sharedSandbox.FailureReason = err.Error()
		sharedSandbox.UpdatedAt = time.Now().UTC()
		result.Sandbox = *cloneSandbox(sharedSandbox)
		result.Message = "merge blocked because shared sandbox manifest could not be created"
		s.recordAuditLocked(ctx, projectID, "SHARED_SANDBOX_MERGE_BLOCKED", "sandbox", sharedSandbox.ID, result.Message, sharedSandbox.UpdatedAt)
		s.persistLocked()
		return result, nil
	}

	if input.SimulateFailure {
		sharedSandbox.Status = domain.SandboxStatusFailed
		sharedSandbox.FailureReason = "simulated shared sandbox integration failure"
		sharedSandbox.UpdatedAt = time.Now().UTC()
		_ = writeFile(filepath.Join(sharedSandbox.RootPath, "integration-error.log"), []byte(sharedSandbox.FailureReason+"\n"))
		result.Sandbox = *cloneSandbox(sharedSandbox)
		if stableSnapshotID := s.stableBranch[projectID]; stableSnapshotID != "" {
			rollback, rollbackErr := s.rollbackToSnapshotLocked(projectID, stableSnapshotID, "shared sandbox integration failure", sharedSandbox.UpdatedAt)
			if rollbackErr != nil {
				result.Message = "merge blocked by shared sandbox integration failure; automatic rollback failed: " + rollbackErr.Error()
			} else {
				result.Rollback = rollback
				result.Message = "merge blocked by shared sandbox integration failure and rolled back to latest stable snapshot"
				s.recordAlertLocked(projectID, "WARN", "SNAPSHOT_ROLLBACK", rollback.Snapshot.ID, result.Message, sharedSandbox.UpdatedAt)
			}
		} else {
			result.Message = "merge blocked by shared sandbox integration failure"
		}
		s.recordAlertLocked(projectID, "CRITICAL", "SHARED_SANDBOX_FAILURE", sharedSandbox.ID, result.Message, sharedSandbox.UpdatedAt)
		s.recordAuditLocked(ctx, projectID, "SHARED_SANDBOX_MERGE_BLOCKED", "sandbox", sharedSandbox.ID, result.Message, sharedSandbox.UpdatedAt)
		s.persistLocked()
		return result, nil
	}

	sharedSandbox.Status = domain.SandboxStatusReleased
	sharedSandbox.UpdatedAt = time.Now().UTC()
	result.Sandbox = *cloneSandbox(sharedSandbox)
	result.Passed = true
	snapshot, snapshotErr := s.recordSnapshotWithWorkspaceLocked(projectID, s.currentBranchLocked(projectID), "shared sandbox merge checkpoint", true, s.latestSnapshotIDLocked(projectID), sharedSandbox.UpdatedAt, sharedSandbox)
	if snapshotErr != nil {
		result.Message = "artifacts merged into shared sandbox, but checkpoint creation failed: " + snapshotErr.Error()
		s.persistLocked()
		return result, nil
	}
	_ = snapshot
	if strings.EqualFold(sharedSandbox.WorkspaceProvider, "git") {
		result.Message = "git branches merged into shared sandbox"
		if s.cfg.WorkspaceGitCleanupEnabled {
			result.Cleanup = s.cleanupWorkspacesLocked(ctx, projectID, CleanupWorkspacesInput{Scope: "PRIVATE", SandboxIDs: s.sourceSandboxIDsForArtifactsLocked(artifactIDs), DeleteBranches: s.cfg.WorkspaceGitCleanupDeleteBranches, IncludeFailed: s.cfg.WorkspaceGitCleanupFailedEnabled}, sharedSandbox.UpdatedAt)
		}
	} else {
		result.Message = "artifacts merged into shared sandbox"
	}
	s.recordAuditLocked(ctx, projectID, "SHARED_SANDBOX_MERGE", "sandbox", sharedSandbox.ID, result.Message, sharedSandbox.UpdatedAt)
	s.persistLocked()

	return result, nil
}

func (s *Service) CleanupWorkspaces(ctx context.Context, projectID string, input CleanupWorkspacesInput) (*CleanupWorkspacesResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}
	return s.cleanupWorkspacesLocked(ctx, projectID, input, now), nil
}

func (s *Service) RebaseWorkspaces(ctx context.Context, projectID string, input RebaseWorkspacesInput) (*RebaseWorkspacesResult, error) {
	now := time.Now().UTC()
	targetRef := strings.TrimSpace(input.TargetRef)
	if targetRef == "" {
		return nil, newValidationError("targetRef is required for workspace rebase")
	}
	selectedIDs := cleanIDSet(input.SandboxIDs)
	if input.All == (len(selectedIDs) > 0) {
		return nil, newValidationError("workspace rebase requires exactly one of sandboxIds or all=true")
	}
	scope := strings.ToUpper(strings.TrimSpace(input.Scope))
	if scope == "" {
		scope = "PRIVATE"
	}
	if scope != "PRIVATE" {
		return nil, newValidationError("workspace rebase only supports PRIVATE scope")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}
	result := &RebaseWorkspacesResult{ProjectID: projectID, DryRun: input.DryRun, TargetRef: targetRef, Results: make([]WorkspaceRebaseResult, 0)}
	for _, sandbox := range s.sandboxes[projectID] {
		if sandbox == nil {
			continue
		}
		if len(selectedIDs) > 0 {
			if _, ok := selectedIDs[sandbox.ID]; !ok {
				continue
			}
		} else if sandbox.Scope != scope {
			continue
		}
		if sandbox.Scope != "PRIVATE" || !strings.EqualFold(sandbox.WorkspaceProvider, "git") || sandbox.Status != domain.SandboxStatusReleased || sandbox.WorkspaceWorktreeGone {
			item := WorkspaceRebaseResult{SandboxID: sandbox.ID, Scope: sandbox.Scope, Provider: sandbox.WorkspaceProvider, Status: "SKIPPED", Reason: "sandbox is not an eligible released private git workspace", Branch: sandbox.WorkspaceBranch, TargetRef: targetRef, OldHeadRef: sandbox.WorkspaceHeadRef}
			result.Results = append(result.Results, item)
			result.Skipped++
			if !input.DryRun {
				s.recordAuditLocked(ctx, projectID, "WORKSPACE_REBASE_SKIPPED", "sandbox", sandbox.ID, item.Reason, now)
			}
			continue
		}
		providerResult, err := s.workspaceProvider.Rebase(ctx, sandbox, workspaceRebaseOptions{DryRun: input.DryRun, TargetRef: targetRef, Fetch: input.Fetch, Publish: input.Publish, Now: now})
		if providerResult == nil {
			providerResult = &workspaceRebaseResult{SandboxID: sandbox.ID, Provider: sandbox.WorkspaceProvider, Status: "FAILED"}
		}
		if err != nil {
			providerResult.Status = "FAILED"
			providerResult.Error = err.Error()
		}
		item := s.applyWorkspaceRebaseResultLocked(ctx, projectID, sandbox, providerResult, input.DryRun, now)
		result.Results = append(result.Results, item)
		switch item.Status {
		case "REBASED":
			result.Rebased++
		case "UP_TO_DATE":
			result.AlreadyUpToDate++
		case "REBASED_PUBLISH_FAILED":
			result.Rebased++
			result.Failed++
		case "FAILED":
			result.Failed++
		case "SKIPPED", "DRY_RUN":
			result.Skipped++
		}
		if item.Published {
			result.Published++
		}
	}
	if !input.DryRun {
		s.persistLocked()
	}
	return result, nil
}

func (s *Service) RollbackToSnapshot(ctx context.Context, projectID string, input RollbackSnapshotInput) (*RollbackResult, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}
	if strings.TrimSpace(input.SnapshotID) == "" {
		return nil, newValidationError("snapshotId is required for rollback")
	}

	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "manual snapshot rollback"
	}

	result, err := s.rollbackToSnapshotLocked(projectID, input.SnapshotID, reason, now)
	if err != nil {
		return nil, err
	}
	s.recordAlertLocked(projectID, "WARN", "SNAPSHOT_ROLLBACK", result.Snapshot.ID, result.Message, now)
	s.recordAuditLocked(ctx, projectID, "SNAPSHOT_ROLLBACK", "snapshot", result.Snapshot.ID, result.Message, now)
	s.persistLocked()
	return result, nil
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
		s.recordCommunicationLocked(projectID, "manager-agent", task.AssigneeAgent, "TASK_DISPATCH", task.ID, task.InputRef, now)
	}
	project.UpdatedAt = now
	s.persistLocked()

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
	task := s.createContractRemediationTaskLocked(projectID, contract, now)
	project.UpdatedAt = now
	result.RemediationTask = cloneTask(task)
	s.persistLocked()

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
	s.persistLocked()

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

	if err := s.ensureTokenBudgetAllowsRunLocked(projectID, task); err != nil {
		return nil, err
	}

	envelope, err := s.startTaskRunLocked(projectID, task, now, "single agent execution started")
	if err != nil {
		s.persistLocked()
		return nil, err
	}
	s.persistLocked()

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

	for _, task := range selected {
		if err := s.ensureTokenBudgetAllowsRunLocked(projectID, task); err != nil {
			return nil, err
		}
	}

	started := make([]RunEnvelope, 0, len(selected))
	for _, task := range selected {
		envelope, startErr := s.startTaskRunLocked(projectID, task, now, "parallel execution started")
		if startErr != nil {
			return nil, startErr
		}
		started = append(started, *envelope)
	}

	s.persistLocked()

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

func (s *Service) ExportDelivery(ctx context.Context, projectID string, input ExportDeliveryInput) (*domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

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
		s.recordAuditLocked(ctx, projectID, "DELIVERY_EXPORT", "artifact", artifact.ID, "delivery export requested for run "+run.ID, time.Now().UTC())
		s.persistLocked()
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
			s.recordAuditLocked(ctx, projectID, "DELIVERY_EXPORT", "artifact", artifact.ID, "delivery export requested from latest successful run", time.Now().UTC())
			s.persistLocked()
			return cloneArtifact(artifact), nil
		}
	}

	return nil, newConflictError("no exportable artifact found")
}

func (s *Service) artifactPathWithinConfiguredRoots(path string) bool {
	artifactRoot := strings.TrimSpace(s.cfg.ArtifactRoot)
	sandboxRoot := strings.TrimSpace(s.cfg.SandboxRoot)
	return pathWithinRoot(path, artifactRoot) || pathWithinRoot(path, sandboxRoot)
}

func pathWithinRoot(path, root string) bool {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(root) == "" {
		return false
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil {
		return false
	}
	return relative == "." || (!strings.HasPrefix(relative, "..") && !filepath.IsAbs(relative))
}

func (s *Service) GetArtifact(ctx context.Context, projectID, artifactID string) (*domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	artifact, ok := s.artifacts[artifactID]
	if !ok || artifact.ProjectID != projectID {
		return nil, newNotFoundError("artifact not found")
	}
	if !s.artifactPathWithinConfiguredRoots(artifact.URI) {
		return nil, newInternalError("ARTIFACT_PATH_INVALID", "artifact path is outside configured roots")
	}
	if _, err := os.Stat(artifact.URI); err != nil {
		return nil, newInternalError("ARTIFACT_MISSING", "artifact file is missing")
	}

	s.recordAuditLocked(ctx, projectID, "DELIVERY_DOWNLOAD", "artifact", artifact.ID, "delivery artifact downloaded", time.Now().UTC())
	s.persistLocked()
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

	// Simulate a scheduler safety checkpoint so a queued human override can be applied.
	time.Sleep(60 * time.Millisecond)
	appliedOverride, err := s.applyPendingHumanOverrideLocked(runID)
	if err != nil {
		s.logger.Error("failed to apply human override", "runId", run.ID, "taskId", task.ID, "error", err)
		s.failRun(runID, err)
		return
	}

	runtimeResponse, err := s.executeRuntimeRun(run, task, plan, project, sandbox)
	if err != nil {
		s.logger.Error("runtime execution failed", "runId", run.ID, "taskId", task.ID, "error", err)
		s.failRun(runID, err)
		return
	}

	artifact, summary, err := s.generateDeliveryBundle(project, task, plan, run, sandbox)
	if err != nil {
		s.logger.Error("run execution failed", "runId", run.ID, "taskId", task.ID, "error", err)
		s.failRun(runID, err)
		return
	}
	if sandbox != nil {
		if err := s.workspaceProvider.FinalizePrivateRun(context.Background(), sandbox, task, run); err != nil {
			s.logger.Error("workspace finalization failed", "runId", run.ID, "taskId", task.ID, "sandboxId", sandbox.ID, "error", err)
			s.failRun(runID, err)
			return
		}
		if err := writeWorkspaceManifest(sandbox, sandbox.WorkspacePath, nil); err != nil {
			s.logger.Error("workspace manifest failed", "runId", run.ID, "taskId", task.ID, "sandboxId", sandbox.ID, "error", err)
			s.failRun(runID, err)
			return
		}
		if err := s.workspaceProvider.Publish(context.Background(), sandbox); err != nil {
			s.logger.Error("workspace publish failed", "runId", run.ID, "taskId", task.ID, "sandboxId", sandbox.ID, "error", err)
			s.failRun(runID, err)
			return
		}
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

	communicationCount := s.communicationCountForTaskLocked(project.ID, storedTask.ID)
	promptTokens, completionTokens, totalTokens := estimateRunTokens(storedTask, plan, communicationCount, false)
	runtimeResponse = normalizeRuntimeUsage(runtimeResponse)
	if runtimeResponse.PromptTokens > 0 || runtimeResponse.CompletionTokens > 0 || runtimeResponse.TotalTokens > 0 {
		promptTokens = runtimeResponse.PromptTokens
		completionTokens = runtimeResponse.CompletionTokens
		totalTokens = runtimeResponse.TotalTokens
	}
	estimatedCostUSD := s.estimateCostFromTokens(promptTokens, completionTokens)
	storedRun.Status = domain.RunStatusSucceeded
	if runtimeModel := strings.TrimSpace(runtimeResponse.Model); runtimeModel != "" {
		storedRun.Model = runtimeModel
	}
	storedRun.PromptTokens = promptTokens
	storedRun.CompletionTokens = completionTokens
	storedRun.TotalTokens = totalTokens
	storedRun.EstimatedCostUSD = estimatedCostUSD
	if runtimeSummary := compactRuntimeSummary(runtimeResponse.Output); runtimeSummary != "" {
		summary += "; runtime output: " + runtimeSummary
	}
	if appliedOverride != nil {
		summary += "; applied human override by " + appliedOverride.Operator + ": " + appliedOverride.Instruction
	}
	storedRun.ResultSummary = summary
	storedRun.ArtifactIDs = append(storedRun.ArtifactIDs, artifact.ID)
	storedRun.EndedAt = now

	storedTask.OutputRef = artifact.URI
	if err := storedTask.TransitionTo(domain.TaskStatusDone, "single agent execution completed", now); err != nil {
		s.logger.Error("failed to transition task to done", "taskId", storedTask.ID, "error", err)
	}
	if storedRun.SandboxID != "" {
		if storedSandbox, exists := s.sandboxIndex[storedRun.SandboxID]; exists {
			if sandbox != nil {
				storedSandbox.WorkspaceHeadRef = sandbox.WorkspaceHeadRef
				storedSandbox.WorkspaceBranch = sandbox.WorkspaceBranch
				storedSandbox.WorkspaceBaseRef = sandbox.WorkspaceBaseRef
			}
			storedSandbox.Status = domain.SandboxStatusReleased
			storedSandbox.UpdatedAt = now
		}
	}
	s.persistLocked()

	s.logger.Info("run execution completed", "runId", storedRun.ID, "taskId", storedTask.ID, "artifactId", artifact.ID)
}

func (s *Service) executeRuntimeRun(run *domain.AgentRun, task *domain.Task, plan *domain.Plan, project *domain.Project, sandbox *domain.Sandbox) (agentruntime.Response, error) {
	if s.runtimeInitErr != nil {
		return agentruntime.Response{}, s.runtimeInitErr
	}
	if s.runtimeRegistry == nil {
		return agentruntime.Response{}, errors.New("runtime registry is not initialized")
	}

	runner, err := s.runtimeRegistry.Get("")
	if err != nil {
		return agentruntime.Response{}, fmt.Errorf("resolve runtime provider: %w", err)
	}

	ctx := context.Background()
	request := agentruntime.Request{
		ProjectID: project.ID,
		TaskID:    task.ID,
		RunID:     run.ID,
		AgentType: run.AgentType,
		Timeout:   s.cfg.RuntimeTimeout,
		Prompt: fmt.Sprintf(
			"Execute task %s (%s) for project %s plan v%d",
			task.Name,
			task.Type,
			project.Name,
			plan.Version,
		),
		Context: strings.Join([]string{
			"project=" + project.ID,
			"task=" + task.ID,
			"plan=" + plan.ID,
			"sandbox=" + run.SandboxID,
			"path=" + sandboxRootPath(sandbox),
			"workspacePath=" + sandboxWorkspacePath(sandbox),
			"workspaceProvider=" + sandboxWorkspaceProvider(sandbox),
			"workspaceBranch=" + sandboxWorkspaceBranch(sandbox),
			"workspaceBaseRef=" + sandboxWorkspaceBaseRef(sandbox),
		}, "; "),
	}

	return runner.Run(ctx, request)
}

func normalizeRuntimeUsage(response agentruntime.Response) agentruntime.Response {
	if response.PromptTokens < 0 {
		response.PromptTokens = 0
	}
	if response.CompletionTokens < 0 {
		response.CompletionTokens = 0
	}
	if response.TotalTokens < 0 {
		response.TotalTokens = 0
	}
	if response.TotalTokens <= 0 {
		response.TotalTokens = response.PromptTokens + response.CompletionTokens
	}
	if response.TotalTokens > 0 && response.PromptTokens == 0 && response.CompletionTokens == 0 {
		response.PromptTokens = response.TotalTokens
	}
	return response
}

func sandboxRootPath(sandbox *domain.Sandbox) string {
	if sandbox == nil {
		return ""
	}
	return strings.TrimSpace(sandbox.RootPath)
}

func sandboxWorkspacePath(sandbox *domain.Sandbox) string {
	if sandbox == nil {
		return ""
	}
	return strings.TrimSpace(sandbox.WorkspacePath)
}

func sandboxWorkspaceProvider(sandbox *domain.Sandbox) string {
	if sandbox == nil {
		return ""
	}
	return strings.TrimSpace(sandbox.WorkspaceProvider)
}

func sandboxWorkspaceBranch(sandbox *domain.Sandbox) string {
	if sandbox == nil {
		return ""
	}
	return strings.TrimSpace(sandbox.WorkspaceBranch)
}

func sandboxWorkspaceBaseRef(sandbox *domain.Sandbox) string {
	if sandbox == nil {
		return ""
	}
	return strings.TrimSpace(sandbox.WorkspaceBaseRef)
}

func (s *Service) estimateCostFromTokens(promptTokens, completionTokens int) float64 {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	return estimateCostFromPricing(promptTokens, completionTokens, s.cfg.TokenPromptPricePerMillion, s.cfg.TokenOutputPricePerMillion)
}

func estimateCostFromPricing(promptTokens, completionTokens int, promptPricePerMillion, outputPricePerMillion float64) float64 {
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	if promptPricePerMillion <= 0 {
		promptPricePerMillion = 1.5
	}
	if outputPricePerMillion <= 0 {
		outputPricePerMillion = 2.5
	}
	return (float64(promptTokens)*promptPricePerMillion + float64(completionTokens)*outputPricePerMillion) / 1_000_000
}

func budgetStatus(cost, warnBudget, blockBudget float64) string {
	if blockBudget > 0 && cost >= blockBudget {
		return "blocked"
	}
	if warnBudget > 0 && cost >= warnBudget {
		return "warning"
	}
	return "ok"
}

func compactRuntimeSummary(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	output = strings.Join(strings.Fields(output), " ")
	const maxLen = 220
	if len(output) > maxLen {
		return output[:maxLen] + "..."
	}
	return output
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
	s.recordAlertLocked(run.ProjectID, "ERROR", "RUN_FAILURE", run.ID, failure.Error(), now)
	if run.SandboxID != "" {
		if sandbox, exists := s.sandboxIndex[run.SandboxID]; exists {
			sandbox.Status = domain.SandboxStatusFailed
			sandbox.FailureReason = failure.Error()
			sandbox.UpdatedAt = now
		}
	}

	if task, exists := s.tasks[run.TaskID]; exists && (task.Status == domain.TaskStatusInProgress || task.Status == domain.TaskStatusHumanOverride) {
		if err := task.TransitionTo(domain.TaskStatusFailed, "single agent execution failed", now); err != nil {
			s.logger.Error("failed to transition task to failed", "taskId", task.ID, "error", err)
		}
	}
	var task *domain.Task
	if existing, ok := s.tasks[run.TaskID]; ok {
		task = existing
	}
	var plan *domain.Plan
	if task != nil {
		plan = s.planIndex[task.PlanID]
	}
	communicationCount := s.communicationCountForTaskLocked(run.ProjectID, run.TaskID)
	promptTokens, completionTokens, totalTokens := estimateRunTokens(task, plan, communicationCount, true)
	run.PromptTokens = promptTokens
	run.CompletionTokens = completionTokens
	run.TotalTokens = totalTokens
	run.EstimatedCostUSD = s.estimateCostFromTokens(promptTokens, completionTokens)
	s.persistLocked()
}

func (s *Service) generateDeliveryBundle(project *domain.Project, task *domain.Task, plan *domain.Plan, run *domain.AgentRun, sandbox *domain.Sandbox) (*domain.Artifact, string, error) {
	runDir := filepath.Join(s.cfg.ArtifactRoot, project.ID, run.ID)
	bundleDir := filepath.Join(runDir, "bundle")
	if sandbox != nil && strings.TrimSpace(sandbox.WorkspacePath) != "" {
		bundleDir = s.workspaceProvider.BundleDir(sandbox, task)
	} else if sandbox != nil && strings.TrimSpace(sandbox.RootPath) != "" {
		bundleDir = filepath.Join(sandbox.RootPath, "workspace", "bundle")
	}

	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return nil, "", fmt.Errorf("create bundle directory: %w", err)
	}

	if err := writeFile(filepath.Join(bundleDir, "README.md"), []byte(renderBundleReadme(project, task, plan))); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "generated-app", "go.mod"), []byte("module generated-app\n\ngo 1.25\n")); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "generated-app", "main.go"), []byte(renderGeneratedSource(project, plan))); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "generated-app", "Dockerfile"), []byte(renderBackendDockerfile())); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "web-app", "package.json"), []byte(renderFrontendPackageJSON(project))); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "web-app", "server.js"), []byte(renderFrontendServerJS(project, plan))); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "web-app", "index.html"), []byte(renderFrontendIndexHTML(project, plan))); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "web-app", "Dockerfile"), []byte(renderFrontendDockerfile())); err != nil {
		return nil, "", err
	}
	if err := writeFile(filepath.Join(bundleDir, "docker-compose.yml"), []byte(renderDockerCompose())); err != nil {
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
	if err := s.applyProjectLocksToBundle(project.ID, task.ID, bundleDir); err != nil {
		return nil, "", err
	}
	now := time.Now().UTC()
	releaseGate, err := buildDeliveryReleaseGate(bundleDir, now)
	if err != nil {
		return nil, "", err
	}
	if err := writeJSONFile(filepath.Join(bundleDir, "metadata", "release-gate.json"), releaseGate); err != nil {
		return nil, "", err
	}
	descriptors, err := collectDeliveryFileDescriptors(bundleDir, requiredDeliveryBundleFiles)
	if err != nil {
		return nil, "", err
	}
	manifest := deliveryBundleManifest{
		SchemaVersion: deliveryBundleSchemaVersion,
		Kind:          "delivery_bundle",
		ProjectID:     project.ID,
		TaskID:        task.ID,
		RunID:         run.ID,
		PlanID:        plan.ID,
		PlanVersion:   plan.Version,
		CreatedAt:     now,
		Generator: deliveryBundleGenerator{
			Name:            "MultiAgentCom",
			ContractVersion: deliveryBundleSchemaVersion,
		},
		Entrypoints: deliveryBundleEntrypoints{
			Frontend:      "http://127.0.0.1:3000",
			BackendHealth: "http://127.0.0.1:8081/health",
			ComposeFile:   "docker-compose.yml",
		},
		Files: descriptors,
		ReleaseGate: deliveryBundleReleaseGateRef{
			Path:   "metadata/release-gate.json",
			Status: releaseGate.Status,
		},
	}
	if err := writeJSONFile(filepath.Join(bundleDir, "metadata", "manifest.json"), manifest); err != nil {
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

	summary := fmt.Sprintf("generated standard delivery bundle for plan v%d at %s", plan.Version, zipPath)
	return artifact, summary, nil
}

const (
	deliveryBundleSchemaVersion = "delivery.bundle.v1"
	deliveryGateSchemaVersion   = "delivery.release_gate.v1"
)

type deliveryRequiredFile struct {
	Path string
	Role string
}

type deliveryFileDescriptor struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	Required  bool   `json:"required"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type deliveryBundleGenerator struct {
	Name            string `json:"name"`
	ContractVersion string `json:"contractVersion"`
}

type deliveryBundleEntrypoints struct {
	Frontend      string `json:"frontend"`
	BackendHealth string `json:"backendHealth"`
	ComposeFile   string `json:"composeFile"`
}

type deliveryBundleReleaseGateRef struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type deliveryBundleManifest struct {
	SchemaVersion string                       `json:"schemaVersion"`
	Kind          string                       `json:"kind"`
	ProjectID     string                       `json:"projectId"`
	TaskID        string                       `json:"taskId"`
	RunID         string                       `json:"runId"`
	PlanID        string                       `json:"planId"`
	PlanVersion   int                          `json:"planVersion"`
	CreatedAt     time.Time                    `json:"createdAt"`
	Generator     deliveryBundleGenerator      `json:"generator"`
	Entrypoints   deliveryBundleEntrypoints    `json:"entrypoints"`
	Files         []deliveryFileDescriptor     `json:"files"`
	ReleaseGate   deliveryBundleReleaseGateRef `json:"releaseGate"`
}

type deliveryGateCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type deliveryReleaseGate struct {
	SchemaVersion string              `json:"schemaVersion"`
	Status        string              `json:"status"`
	GeneratedAt   time.Time           `json:"generatedAt"`
	Checks        []deliveryGateCheck `json:"checks"`
}

var requiredDeliveryBundleFiles = []deliveryRequiredFile{
	{Path: "README.md", Role: "documentation"},
	{Path: "docker-compose.yml", Role: "orchestration"},
	{Path: "generated-app/go.mod", Role: "backend_dependency_manifest"},
	{Path: "generated-app/main.go", Role: "backend_source"},
	{Path: "generated-app/Dockerfile", Role: "backend_container"},
	{Path: "web-app/package.json", Role: "frontend_dependency_manifest"},
	{Path: "web-app/server.js", Role: "frontend_server"},
	{Path: "web-app/index.html", Role: "frontend_source"},
	{Path: "web-app/Dockerfile", Role: "frontend_container"},
	{Path: "metadata/prd.json", Role: "metadata"},
	{Path: "metadata/task.json", Role: "metadata"},
	{Path: "metadata/run.json", Role: "metadata"},
	{Path: "metadata/release-gate.json", Role: "release_gate"},
}

func buildDeliveryReleaseGate(bundleDir string, generatedAt time.Time) (deliveryReleaseGate, error) {
	checks := []deliveryGateCheck{
		{ID: "required_files_present", Status: "PASS", Message: "all required bundle files are present"},
		{ID: "metadata_json_valid", Status: "PASS", Message: "metadata/prd.json, metadata/task.json and metadata/run.json are valid JSON"},
		{ID: "local_entrypoints_declared", Status: "PASS", Message: "frontend, backend health and compose entrypoints are declared"},
	}
	if err := validateRequiredDeliveryFiles(bundleDir, requiredDeliveryBundleFiles[:len(requiredDeliveryBundleFiles)-1]); err != nil {
		return deliveryReleaseGate{}, err
	}
	if err := validateDeliveryMetadataJSON(bundleDir); err != nil {
		return deliveryReleaseGate{}, err
	}
	if _, err := os.Stat(filepath.Join(bundleDir, "metadata", "lock-conflicts.log")); err == nil {
		checks = append(checks, deliveryGateCheck{ID: "code_locks_applied", Status: "WARN", Message: "human code locks produced conflict notes in metadata/lock-conflicts.log"})
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return deliveryReleaseGate{}, err
	}
	return deliveryReleaseGate{
		SchemaVersion: deliveryGateSchemaVersion,
		Status:        "PASS",
		GeneratedAt:   generatedAt,
		Checks:        checks,
	}, nil
}

func validateRequiredDeliveryFiles(bundleDir string, required []deliveryRequiredFile) error {
	for _, item := range required {
		path := filepath.Join(bundleDir, filepath.FromSlash(item.Path))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("delivery bundle missing required file %s: %w", item.Path, err)
		}
		if info.IsDir() {
			return fmt.Errorf("delivery bundle required path %s is a directory", item.Path)
		}
		if info.Size() <= 0 {
			return fmt.Errorf("delivery bundle required file %s is empty", item.Path)
		}
	}
	return nil
}

func validateDeliveryMetadataJSON(bundleDir string) error {
	for _, path := range []string{"metadata/prd.json", "metadata/task.json", "metadata/run.json"} {
		payload, err := os.ReadFile(filepath.Join(bundleDir, filepath.FromSlash(path)))
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			return fmt.Errorf("invalid delivery metadata json %s: %w", path, err)
		}
	}
	return nil
}

func collectDeliveryFileDescriptors(bundleDir string, required []deliveryRequiredFile) ([]deliveryFileDescriptor, error) {
	descriptors := make([]deliveryFileDescriptor, 0, len(required))
	for _, item := range required {
		path := filepath.Join(bundleDir, filepath.FromSlash(item.Path))
		checksum, size, err := fileChecksum(path)
		if err != nil {
			return nil, fmt.Errorf("checksum delivery file %s: %w", item.Path, err)
		}
		if size <= 0 {
			return nil, fmt.Errorf("delivery bundle required file %s is empty", item.Path)
		}
		descriptors = append(descriptors, deliveryFileDescriptor{
			Path:      item.Path,
			Role:      item.Role,
			Required:  true,
			SHA256:    checksum,
			SizeBytes: size,
		})
	}
	return descriptors, nil
}

type workspaceFileDescriptor struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type workspaceManifest struct {
	SchemaVersion string                    `json:"schemaVersion"`
	SandboxID     string                    `json:"sandboxId"`
	ProjectID     string                    `json:"projectId"`
	TaskID        string                    `json:"taskId,omitempty"`
	Provider      string                    `json:"provider"`
	WorkspacePath string                    `json:"workspacePath"`
	Branch        string                    `json:"branch,omitempty"`
	BaseRef       string                    `json:"baseRef,omitempty"`
	HeadRef       string                    `json:"headRef,omitempty"`
	TreeHash      string                    `json:"treeHash"`
	Files         []workspaceFileDescriptor `json:"files"`
	Artifacts     any                       `json:"artifacts,omitempty"`
	GeneratedAt   time.Time                 `json:"generatedAt"`
}

func writeWorkspaceManifest(sandbox *domain.Sandbox, workspacePath string, artifacts any) error {
	files, treeHash, err := collectWorkspaceFiles(workspacePath)
	if err != nil {
		return err
	}
	manifest := workspaceManifest{
		SchemaVersion: "workspace.manifest.v1",
		SandboxID:     sandbox.ID,
		ProjectID:     sandbox.ProjectID,
		TaskID:        sandbox.TaskID,
		Provider:      sandbox.WorkspaceProvider,
		WorkspacePath: workspacePath,
		Branch:        sandbox.WorkspaceBranch,
		BaseRef:       sandbox.WorkspaceBaseRef,
		HeadRef:       sandbox.WorkspaceHeadRef,
		TreeHash:      treeHash,
		Files:         files,
		Artifacts:     artifacts,
		GeneratedAt:   time.Now().UTC(),
	}
	return writeJSONFile(filepath.Join(workspacePath, ".multiagent", "workspace-manifest.json"), manifest)
}

func collectWorkspaceFiles(workspacePath string) ([]workspaceFileDescriptor, string, error) {
	manifestPath := filepath.Join(workspacePath, ".multiagent", "workspace-manifest.json")
	files := make([]workspaceFileDescriptor, 0)
	if err := filepath.WalkDir(workspacePath, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == ".git" || path == manifestPath {
			return nil
		}
		relativePath, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return err
		}
		checksum, size, err := fileChecksum(path)
		if err != nil {
			return err
		}
		pathSlash := filepath.ToSlash(relativePath)
		files = append(files, workspaceFileDescriptor{
			Path:      pathSlash,
			Role:      workspaceFileRole(pathSlash),
			SHA256:    checksum,
			SizeBytes: size,
		})
		return nil
	}); err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	treeHash := sha256.New()
	for _, file := range files {
		fmt.Fprintf(treeHash, "%s\x00%s\x00%d\n", file.Path, file.SHA256, file.SizeBytes)
	}
	return files, hex.EncodeToString(treeHash.Sum(nil)), nil
}

func workspaceFileRole(path string) string {
	switch {
	case strings.HasPrefix(path, "bundle/metadata/"):
		return "bundle_metadata"
	case strings.HasPrefix(path, "bundle/generated-app/"):
		return "backend_source"
	case strings.HasPrefix(path, "bundle/web-app/"):
		return "frontend_source"
	case strings.HasPrefix(path, "bundle/"):
		return "delivery_bundle"
	case strings.HasPrefix(path, "artifacts/"):
		return "materialized_artifact"
	case path == "manifest.json":
		return "shared_manifest"
	default:
		return "workspace_file"
	}
}

func materializeArtifact(sourcePath, destinationDir string) error {
	if err := os.RemoveAll(destinationDir); err != nil {
		return err
	}
	if err := os.MkdirAll(destinationDir, 0o755); err != nil {
		return err
	}
	archive, err := zip.OpenReader(sourcePath)
	if err != nil {
		payload, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			return fmt.Errorf("read artifact %s: %w", sourcePath, readErr)
		}
		return writeFile(filepath.Join(destinationDir, filepath.Base(sourcePath)), payload)
	}
	defer archive.Close()

	for _, file := range archive.File {
		targetPath := filepath.Join(destinationDir, filepath.FromSlash(file.Name))
		cleanDestination := filepath.Clean(destinationDir) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(targetPath), cleanDestination) {
			return fmt.Errorf("artifact %s contains unsafe path %s", sourcePath, file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, file.Mode()); err != nil {
				return err
			}
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		payload, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := writeFile(targetPath, payload); err != nil {
			return err
		}
	}
	return nil
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

	var readyTasks, runningTasks, overrideTasks, completedTasks, failedTasks int
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
		case domain.TaskStatusHumanOverride:
			overrideTasks++
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
		case domain.TaskStatusHumanOverride:
			agent.HumanOverrideTasks++
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
		OverrideTasks:  overrideTasks,
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
	case agent.HumanOverrideTasks > 0:
		return "HUMAN_OVERRIDE"
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

func (s *Service) findActiveRunForTaskLocked(projectID, taskID string) *domain.AgentRun {
	runIDs := s.runOrder[projectID]
	for idx := len(runIDs) - 1; idx >= 0; idx-- {
		run, ok := s.runs[runIDs[idx]]
		if !ok || run.TaskID != taskID {
			continue
		}
		if run.Status == domain.RunStatusRunning {
			return run
		}
	}
	return nil
}

func (s *Service) applyPendingHumanOverrideLocked(runID string) (*domain.HumanOverride, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	run, ok := s.runs[runID]
	if !ok {
		return nil, newNotFoundError("run not found")
	}
	task, ok := s.tasks[run.TaskID]
	if !ok {
		return nil, newNotFoundError("task not found")
	}

	for idx := len(s.overrides[run.ProjectID]) - 1; idx >= 0; idx-- {
		override := s.overrides[run.ProjectID][idx]
		if override.TaskID != task.ID || !override.AppliedAt.IsZero() {
			continue
		}

		override.AppliedAt = now
		if task.Status == domain.TaskStatusHumanOverride {
			if err := task.TransitionTo(domain.TaskStatusInProgress, "human override applied at safety checkpoint", now); err != nil {
				return nil, newConflictError(err.Error())
			}
		}
		s.persistLocked()
		return cloneHumanOverride(override), nil
	}

	return nil, nil
}

func (s *Service) applyProjectLocksToBundle(projectID, taskID, bundleDir string) error {
	s.mu.RLock()
	locks := append([]*domain.CodeLock(nil), s.locks[projectID]...)
	s.mu.RUnlock()

	conflicts := make([]string, 0)
	for _, lock := range locks {
		if lock == nil {
			continue
		}
		if lock.TaskID != "" && lock.TaskID != taskID {
			continue
		}

		targetPath := filepath.Join(bundleDir, filepath.FromSlash(lock.Path))
		switch normalizeLockMode(lock.LockMode) {
		case "file":
			if existing, err := os.ReadFile(targetPath); err == nil && string(existing) != lock.Content {
				conflicts = append(conflicts, fmt.Sprintf("overwrote generated content for %s because LOCKED BY HUMAN content from %s takes precedence", lock.Path, lock.CreatedBy))
			}
			if err := writeFile(targetPath, []byte(lock.Content)); err != nil {
				return err
			}
		case "go_symbol":
			changed, conflict, err := applyGoSymbolLock(targetPath, lock)
			if err != nil {
				return err
			}
			if changed && conflict != "" {
				conflicts = append(conflicts, conflict)
			}
		default:
			return fmt.Errorf("unsupported code lock mode %q", lock.LockMode)
		}
	}

	if len(conflicts) > 0 {
		return writeFile(filepath.Join(bundleDir, "metadata", "lock-conflicts.log"), []byte(strings.Join(conflicts, "\n")+"\n"))
	}

	return nil
}

func normalizeLockMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return "file"
	}
	if mode == "file" || mode == "go_symbol" {
		return mode
	}
	return ""
}

func validateCodeLock(path, content, lockMode, language, symbolKind, symbolName string) error {
	if lockMode == "file" {
		return nil
	}
	if lockMode != "go_symbol" {
		return newValidationError("lockMode must be file or go_symbol")
	}
	if !strings.HasSuffix(filepath.ToSlash(path), ".go") {
		return newValidationError("go_symbol locks require a Go source path")
	}
	if language != "" && !strings.EqualFold(language, "go") {
		return newValidationError("go_symbol locks require language go")
	}
	if !isSupportedGoSymbolKind(symbolKind) {
		return newValidationError("go_symbol locks support symbolKind func, method, type, var, or const")
	}
	if symbolName == "" {
		return newValidationError("symbolName is required for go_symbol locks")
	}
	symbolSelector := parseGoSymbolSelector(symbolKind, symbolName)
	start, end, err := findGoSymbol([]byte(content), symbolKind, symbolSelector)
	if err != nil {
		return newValidationError(err.Error())
	}
	if !strings.Contains(content[start:end], "LOCKED BY HUMAN") {
		return newValidationError("go_symbol lock marker must be inside selected Go symbol")
	}
	return nil
}

func applyGoSymbolLock(targetPath string, lock *domain.CodeLock) (bool, string, error) {
	target, err := os.ReadFile(targetPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, "", err
	}
	locked := []byte(lock.Content)
	symbolSelector := parseGoSymbolSelector(lock.SymbolKind, lock.SymbolName)
	lockedStart, lockedEnd, err := findGoSymbol(locked, lock.SymbolKind, symbolSelector)
	if err != nil {
		return false, "", err
	}
	lockedSymbol := locked[lockedStart:lockedEnd]
	if len(target) == 0 {
		merged, err := goLockedSymbolFile(locked, lock.SymbolKind, symbolSelector)
		if err != nil {
			return false, "", err
		}
		return true, fmt.Sprintf("created %s with locked Go %s %s from %s", lock.Path, lock.SymbolKind, lock.SymbolName, lock.CreatedBy), writeFile(targetPath, merged)
	}
	targetStart, targetEnd, err := findGoSymbol(target, lock.SymbolKind, symbolSelector)
	if err != nil {
		if _, parseErr := parser.ParseFile(token.NewFileSet(), targetPath, target, parser.ParseComments); parseErr != nil {
			return false, "", fmt.Errorf("parse generated Go target %s: %w", lock.Path, parseErr)
		}
		merged, err := reconcileGoImports(append(appendNewline(target), appendNewline(lockedSymbol)...), locked)
		if err != nil {
			return false, "", err
		}
		return true, fmt.Sprintf("appended locked Go %s %s to %s because generated target was missing it", lock.SymbolKind, lock.SymbolName, lock.Path), writeFile(targetPath, merged)
	}
	merged := make([]byte, 0, len(target)-targetEnd+targetStart+len(lockedSymbol))
	merged = append(merged, target[:targetStart]...)
	merged = append(merged, lockedSymbol...)
	merged = append(merged, target[targetEnd:]...)
	merged, err = reconcileGoImports(merged, locked)
	if err != nil {
		return false, "", err
	}
	return true, fmt.Sprintf("replaced generated Go %s %s in %s with LOCKED BY HUMAN content from %s", lock.SymbolKind, lock.SymbolName, lock.Path, lock.CreatedBy), writeFile(targetPath, merged)
}

func reconcileGoImports(source, lockedSource []byte) ([]byte, error) {
	imports, err := goImportSpecs(lockedSource)
	if err != nil {
		return nil, err
	}
	withImports, err := addMissingGoImports(source, imports)
	if err != nil {
		return nil, err
	}
	cleaned, err := removeUnusedGoImports(withImports)
	if err != nil {
		return nil, err
	}
	formatted, err := format.Source(cleaned)
	if err != nil {
		return nil, fmt.Errorf("format reconciled Go source: %w", err)
	}
	return formatted, nil
}

func goImportSpecs(source []byte) ([]goImportSpec, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "lock.go", source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse Go imports: %w", err)
	}
	imports := make([]goImportSpec, 0, len(file.Imports))
	for _, item := range file.Imports {
		if item.Path == nil {
			continue
		}
		name := ""
		if item.Name != nil {
			name = item.Name.Name
		}
		imports = append(imports, goImportSpec{Name: name, Path: item.Path.Value})
	}
	return imports, nil
}

type goImportSpec struct {
	Name string
	Path string
}

func addMissingGoImports(source []byte, imports []goImportSpec) ([]byte, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "target.go", source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse merged Go source: %w", err)
	}
	existing := make(map[string]struct{}, len(file.Imports))
	for _, item := range file.Imports {
		if item.Path != nil {
			existing[item.Path.Value] = struct{}{}
		}
	}
	missing := make([]goImportSpec, 0)
	for _, item := range imports {
		if _, ok := existing[item.Path]; !ok {
			missing = append(missing, item)
		}
	}
	if len(missing) == 0 {
		return append([]byte(nil), source...), nil
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Path < missing[j].Path })
	insertOffset := goImportInsertOffset(fileSet, file, source)
	block := renderGoImportBlock(missing, fileHasImports(file))
	merged := make([]byte, 0, len(source)+len(block))
	merged = append(merged, source[:insertOffset]...)
	merged = append(merged, block...)
	merged = append(merged, source[insertOffset:]...)
	return merged, nil
}

func fileHasImports(file *ast.File) bool {
	return len(file.Imports) > 0
}

func goImportInsertOffset(fileSet *token.FileSet, file *ast.File, source []byte) int {
	if len(file.Imports) > 0 {
		return fileSet.Position(file.Imports[len(file.Imports)-1].End()).Offset
	}
	packageOffset := fileSet.Position(file.Name.End()).Offset
	for packageOffset < len(source) && (source[packageOffset] == '\r' || source[packageOffset] == '\n') {
		packageOffset++
	}
	return packageOffset
}

func renderGoImportBlock(imports []goImportSpec, appendToExisting bool) []byte {
	var builder strings.Builder
	if appendToExisting {
		for _, item := range imports {
			builder.WriteByte('\n')
			builder.WriteString(renderGoImportSpec(item))
		}
		return []byte(builder.String())
	}
	builder.WriteString("\n\nimport (\n")
	for _, item := range imports {
		builder.WriteByte('\t')
		builder.WriteString(renderGoImportSpec(item))
		builder.WriteByte('\n')
	}
	builder.WriteString(")")
	return []byte(builder.String())
}

func renderGoImportSpec(item goImportSpec) string {
	if item.Name != "" {
		return item.Name + " " + item.Path
	}
	return item.Path
}

func removeUnusedGoImports(source []byte) ([]byte, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "target.go", source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse merged Go source imports: %w", err)
	}
	used := goUsedSelectors(file)
	unused := make([]goImportRange, 0)
	for _, item := range file.Imports {
		if item.Path == nil || item.Name != nil {
			continue
		}
		alias := goDefaultImportName(item.Path.Value)
		if alias == "" || used[alias] {
			continue
		}
		unused = append(unused, goImportRange{Start: fileSet.Position(item.Pos()).Offset, End: fileSet.Position(item.End()).Offset})
	}
	if len(unused) == 0 {
		return append([]byte(nil), source...), nil
	}
	sort.Slice(unused, func(i, j int) bool { return unused[i].Start > unused[j].Start })
	cleaned := append([]byte(nil), source...)
	for _, item := range unused {
		start, end := expandGoImportRemoval(cleaned, item.Start, item.End)
		cleaned = append(cleaned[:start], cleaned[end:]...)
	}
	return cleaned, nil
}

type goImportRange struct {
	Start int
	End   int
}

func expandGoImportRemoval(source []byte, start, end int) (int, int) {
	for start > 0 && (source[start-1] == ' ' || source[start-1] == '\t') {
		start--
	}
	for end < len(source) && (source[end] == ' ' || source[end] == '\t') {
		end++
	}
	if end < len(source) && source[end] == '\r' {
		end++
	}
	if end < len(source) && source[end] == '\n' {
		end++
	}
	return start, end
}

func goUsedSelectors(file *ast.File) map[string]bool {
	used := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if ok {
			used[ident.Name] = true
		}
		return true
	})
	return used
}

func goDefaultImportName(pathValue string) string {
	pathValue = strings.Trim(pathValue, "\"")
	if pathValue == "" {
		return ""
	}
	parts := strings.Split(pathValue, "/")
	return parts[len(parts)-1]
}

func isSupportedGoSymbolKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "func", "method", "type", "var", "const":
		return true
	default:
		return false
	}
}

type goSymbolSelector struct {
	Name     string
	Receiver string
}

func parseGoSymbolSelector(symbolKind, symbolName string) goSymbolSelector {
	selector := goSymbolSelector{Name: strings.TrimSpace(symbolName)}
	if strings.EqualFold(strings.TrimSpace(symbolKind), "method") {
		if receiver, name, ok := strings.Cut(selector.Name, "."); ok {
			selector.Receiver = strings.TrimSpace(receiver)
			selector.Name = strings.TrimSpace(name)
		}
	}
	return selector
}

func findGoSymbol(source []byte, symbolKind string, selector goSymbolSelector) (int, int, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "lock.go", source, parser.ParseComments)
	if err != nil {
		return 0, 0, fmt.Errorf("go_symbol lock content must be parseable Go: %w", err)
	}
	symbolKind = strings.ToLower(strings.TrimSpace(symbolKind))
	for _, decl := range file.Decls {
		if start, end, ok := goDeclSymbolRange(fileSet, decl, symbolKind, selector); ok {
			return start, end, nil
		}
	}
	return 0, 0, fmt.Errorf("go_symbol lock content must contain Go %s %s", symbolKind, selector.DisplayName())
}

func (s goSymbolSelector) DisplayName() string {
	if s.Receiver != "" {
		return s.Receiver + "." + s.Name
	}
	return s.Name
}

func goDeclSymbolRange(fileSet *token.FileSet, decl ast.Decl, symbolKind string, selector goSymbolSelector) (int, int, bool) {
	switch typedDecl := decl.(type) {
	case *ast.FuncDecl:
		if typedDecl.Name == nil || typedDecl.Name.Name != selector.Name {
			return 0, 0, false
		}
		if symbolKind == "func" && typedDecl.Recv == nil || symbolKind == "method" && goMethodReceiverMatches(typedDecl, selector.Receiver) {
			return goNodeStartOffset(fileSet, typedDecl.Doc, typedDecl.Pos()), fileSet.Position(typedDecl.End()).Offset, true
		}
	case *ast.GenDecl:
		if typedDecl.Tok == token.TYPE && symbolKind != "type" || typedDecl.Tok == token.VAR && symbolKind != "var" || typedDecl.Tok == token.CONST && symbolKind != "const" {
			return 0, 0, false
		}
		for _, spec := range typedDecl.Specs {
			if goSpecName(spec) == selector.Name {
				return goNodeStartOffset(fileSet, typedDecl.Doc, typedDecl.Pos()), fileSet.Position(typedDecl.End()).Offset, true
			}
		}
	}
	return 0, 0, false
}

func goMethodReceiverMatches(decl *ast.FuncDecl, receiver string) bool {
	if decl.Recv == nil {
		return false
	}
	if receiver == "" {
		return true
	}
	for _, field := range decl.Recv.List {
		if goReceiverTypeName(field.Type) == receiver {
			return true
		}
	}
	return false
}

func goReceiverTypeName(expr ast.Expr) string {
	switch typedExpr := expr.(type) {
	case *ast.Ident:
		return typedExpr.Name
	case *ast.StarExpr:
		return goReceiverTypeName(typedExpr.X)
	case *ast.IndexExpr:
		return goReceiverTypeName(typedExpr.X)
	case *ast.IndexListExpr:
		return goReceiverTypeName(typedExpr.X)
	}
	return ""
}

func goNodeStartOffset(fileSet *token.FileSet, doc *ast.CommentGroup, fallback token.Pos) int {
	if doc != nil {
		return fileSet.Position(doc.Pos()).Offset
	}
	return fileSet.Position(fallback).Offset
}

func goLockedSymbolFile(source []byte, symbolKind string, selector goSymbolSelector) ([]byte, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "lock.go", source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("go_symbol lock content must be parseable Go: %w", err)
	}
	start, end, err := findGoSymbol(source, symbolKind, selector)
	if err != nil {
		return nil, err
	}
	imports, err := goImportSpecs(source)
	if err != nil {
		return nil, err
	}
	var builder strings.Builder
	builder.WriteString("package ")
	builder.WriteString(file.Name.Name)
	builder.WriteByte('\n')
	if len(imports) > 0 {
		builder.Write(renderGoImportBlock(imports, false))
		builder.WriteByte('\n')
	}
	builder.WriteByte('\n')
	builder.Write(appendNewline(source[start:end]))
	return reconcileGoImports([]byte(builder.String()), source)
}

func goSpecName(spec ast.Spec) string {
	switch typedSpec := spec.(type) {
	case *ast.TypeSpec:
		if typedSpec.Name != nil {
			return typedSpec.Name.Name
		}
	case *ast.ValueSpec:
		if len(typedSpec.Names) == 1 && typedSpec.Names[0] != nil {
			return typedSpec.Names[0].Name
		}
	}
	return ""
}

func appendNewline(payload []byte) []byte {
	if len(payload) == 0 || payload[len(payload)-1] == '\n' {
		return append([]byte(nil), payload...)
	}
	copyPayload := append([]byte(nil), payload...)
	return append(copyPayload, '\n')
}

func (s *Service) resolveLatestReleasedSharedSandboxLocked(projectID string) (*domain.Sandbox, error) {
	items := s.sandboxes[projectID]
	for idx := len(items) - 1; idx >= 0; idx-- {
		sandbox := items[idx]
		if sandbox.Scope == "SHARED" && sandbox.Status == domain.SandboxStatusReleased {
			return sandbox, nil
		}
	}
	return nil, newConflictError("no released shared sandbox available for preview")
}

func (s *Service) recordCommunicationLocked(projectID, from, to, messageType, taskID, payloadRef string, now time.Time) {
	base := strings.Join([]string{
		"v1",
		strings.TrimSpace(from),
		strings.TrimSpace(to),
		strings.TrimSpace(messageType),
		strings.TrimSpace(taskID),
		strings.TrimSpace(payloadRef),
		now.Format(time.RFC3339Nano),
	}, "|")
	sum := sha256.Sum256([]byte(base))

	entry := &domain.CommunicationLog{
		ID:         nextID("comm"),
		ProjectID:  projectID,
		Version:    "v1",
		From:       strings.TrimSpace(from),
		To:         strings.TrimSpace(to),
		Type:       strings.TrimSpace(messageType),
		TaskID:     strings.TrimSpace(taskID),
		PayloadRef: strings.TrimSpace(payloadRef),
		Checksum:   hex.EncodeToString(sum[:4]),
		Timestamp:  now,
	}
	s.communications[projectID] = append(s.communications[projectID], entry)
}

func (s *Service) recordAuditLocked(ctx context.Context, projectID, action, resourceType, resourceID, summary string, now time.Time) {
	actor := ActorFromContext(ctx)
	if actor == "" {
		actor = "system"
	}
	entry := &domain.AuditLog{
		ID:           nextID("audit"),
		ProjectID:    projectID,
		Actor:        actor,
		Action:       strings.TrimSpace(action),
		ResourceType: strings.TrimSpace(resourceType),
		ResourceID:   strings.TrimSpace(resourceID),
		Summary:      strings.TrimSpace(summary),
		Timestamp:    now,
	}
	s.auditLogs[projectID] = append(s.auditLogs[projectID], entry)
}

func (s *Service) recordAlertLocked(projectID, severity, alertType, resourceID, message string, now time.Time) {
	entry := &domain.Alert{
		ID:         nextID("alert"),
		ProjectID:  projectID,
		Severity:   strings.TrimSpace(severity),
		Type:       strings.TrimSpace(alertType),
		ResourceID: strings.TrimSpace(resourceID),
		Message:    strings.TrimSpace(message),
		Timestamp:  now,
	}
	s.alerts[projectID] = append(s.alerts[projectID], entry)
	s.notifyAlertAsync(cloneAlert(entry))
}

func (s *Service) notifyAlertAsync(alert *domain.Alert) {
	if alert == nil || strings.TrimSpace(s.cfg.AlertWebhookURL) == "" {
		return
	}

	webhookURL := s.cfg.AlertWebhookURL
	client := s.alertClient
	logger := s.logger
	serviceName := s.cfg.ServiceName
	go func() {
		payload, err := json.Marshal(map[string]any{
			"service": serviceName,
			"alert":   alert,
		})
		if err != nil {
			logger.Error("alert webhook marshal failed", "error", err, "alertId", alert.ID)
			return
		}

		req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(payload))
		if err != nil {
			logger.Error("alert webhook request failed", "error", err, "alertId", alert.ID)
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			logger.Error("alert webhook delivery failed", "error", err, "alertId", alert.ID)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 300 {
			logger.Error("alert webhook rejected response", "status", resp.StatusCode, "alertId", alert.ID)
		}
	}()
}

func (s *Service) communicationCountForTaskLocked(projectID, taskID string) int {
	if strings.TrimSpace(taskID) == "" {
		return 0
	}
	count := 0
	for _, item := range s.communications[projectID] {
		if item == nil || item.TaskID != taskID {
			continue
		}
		count++
	}
	return count
}

func estimateRunTokens(task *domain.Task, plan *domain.Plan, communicationCount int, failed bool) (int, int, int) {
	promptTokens := 220 + communicationCount*36
	completionTokens := 180 + communicationCount*24

	if plan != nil {
		promptTokens += len(plan.Scope) * 42
		promptTokens += len(plan.Constraints) * 28
		completionTokens += len(plan.AcceptanceCriteria) * 30
	}
	if task != nil {
		promptTokens += len(task.DependsOn) * 32
		switch task.Type {
		case "BACKEND_IMPLEMENTATION":
			promptTokens += 160
			completionTokens += 240
		case "FRONTEND_IMPLEMENTATION":
			promptTokens += 140
			completionTokens += 220
		case "INTEGRATION_REVIEW":
			promptTokens += 120
			completionTokens += 180
		default:
			promptTokens += 100
			completionTokens += 140
		}
	}
	if failed {
		completionTokens /= 2
	}

	return promptTokens, completionTokens, promptTokens + completionTokens
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

func (s *Service) ensureTokenBudgetAllowsRunLocked(projectID string, task *domain.Task) error {
	blockBudget := s.cfg.TokenBudgetBlockUSD
	if blockBudget <= 0 {
		return nil
	}

	currentCost := 0.0
	for _, runID := range s.runOrder[projectID] {
		run := s.runs[runID]
		if run == nil {
			continue
		}
		currentCost += run.EstimatedCostUSD
	}

	plan := s.planIndex[task.PlanID]
	communicationCount := s.communicationCountForTaskLocked(projectID, task.ID)
	promptTokens, completionTokens, _ := estimateRunTokens(task, plan, communicationCount, false)
	projectedCost := currentCost + s.estimateCostFromTokens(promptTokens, completionTokens)
	if projectedCost >= blockBudget {
		return newConflictError(fmt.Sprintf("token budget block threshold exceeded: projected %.6f USD >= %.6f USD", projectedCost, blockBudget))
	}
	return nil
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

	sandbox, err := s.createPrivateSandboxLocked(projectID, task, agentType, now)
	if err != nil {
		_ = task.TransitionTo(domain.TaskStatusFailed, err.Error(), now)
		return nil, err
	}

	run := &domain.AgentRun{
		ID:        nextID("run"),
		ProjectID: projectID,
		TaskID:    task.ID,
		AgentType: agentType,
		Model:     "runtime-" + s.runtimeProviderName(),
		SandboxID: sandbox.ID,
		Status:    domain.RunStatusRunning,
		StartedAt: now,
	}
	sandbox.RunID = run.ID
	sandbox.UpdatedAt = now

	s.runs[run.ID] = run
	s.runOrder[projectID] = append(s.runOrder[projectID], run.ID)
	s.recordCommunicationLocked(projectID, "orchestrator", agentType, "RUN_START", task.ID, "run://"+run.ID, now)

	return &RunEnvelope{
		Task: *cloneTask(task),
		Run:  *cloneRun(run),
	}, nil
}

func (s *Service) createPrivateSandboxLocked(projectID string, task *domain.Task, agentType string, now time.Time) (*domain.Sandbox, error) {
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
	sandbox.WorkspacePath = filepath.Join(sandbox.RootPath, "workspace")
	sandbox.WorkspaceProvider = s.workspaceProvider.Name()
	sandbox.WorkspaceManifestRef = "file://" + filepath.Join(sandbox.WorkspacePath, ".multiagent", "workspace-manifest.json")
	if err := s.workspaceProvider.CreatePrivate(context.Background(), sandbox, task); err != nil {
		return nil, newInternalError("SANDBOX_CREATE_FAILED", "sandbox workspace could not be created")
	}

	s.sandboxIndex[sandbox.ID] = sandbox
	s.sandboxes[projectID] = append(s.sandboxes[projectID], sandbox)
	return sandbox, nil
}

func (s *Service) createSharedSandboxLocked(projectID string, tasks []*domain.Task, now time.Time) (*domain.Sandbox, error) {
	sandbox := &domain.Sandbox{
		ID:        nextID("sandbox"),
		ProjectID: projectID,
		AgentType: "shared-merge-gate",
		Scope:     "SHARED",
		Status:    domain.SandboxStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if len(tasks) > 0 {
		sandbox.TaskID = tasks[len(tasks)-1].ID
	}
	sandbox.RootPath = filepath.Join(s.cfg.SandboxRoot, "shared", projectID, sandbox.ID)
	sandbox.WorkspacePath = filepath.Join(sandbox.RootPath, "workspace")
	sandbox.WorkspaceProvider = s.workspaceProvider.Name()
	sandbox.WorkspaceManifestRef = "file://" + filepath.Join(sandbox.WorkspacePath, ".multiagent", "workspace-manifest.json")
	if err := s.workspaceProvider.CreateShared(context.Background(), sandbox, tasks, s.latestWorkspaceHeadRefLocked(projectID)); err != nil {
		return nil, newInternalError("SANDBOX_CREATE_FAILED", "sandbox workspace could not be created")
	}

	s.sandboxIndex[sandbox.ID] = sandbox
	s.sandboxes[projectID] = append(s.sandboxes[projectID], sandbox)
	return sandbox, nil
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

func (s *Service) resolveMergeTasksLocked(projectID string, taskIDs []string) ([]*domain.Task, []string, error) {
	selected := make([]*domain.Task, 0, len(taskIDs))
	artifactIDs := make([]string, 0, len(taskIDs))
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

		if task.Status != domain.TaskStatusDone {
			return nil, nil, newConflictError("only completed tasks can be merged into shared sandbox")
		}

		artifact, err := s.resolveLatestSucceededArtifactForTaskLocked(projectID, task.ID)
		if err != nil {
			return nil, nil, err
		}

		selected = append(selected, task)
		artifactIDs = append(artifactIDs, artifact.ID)
	}

	if len(selected) == 0 {
		return nil, nil, newValidationError("taskIds are required for shared sandbox merge")
	}

	return selected, artifactIDs, nil
}

func (s *Service) resolveLatestSucceededArtifactForTaskLocked(projectID, taskID string) (*domain.Artifact, error) {
	runIDs := s.runOrder[projectID]
	for idx := len(runIDs) - 1; idx >= 0; idx-- {
		run, ok := s.runs[runIDs[idx]]
		if !ok || run.TaskID != taskID || run.Status != domain.RunStatusSucceeded {
			continue
		}
		artifact, err := s.resolveArtifactFromRunLocked(run)
		if err == nil {
			return artifact, nil
		}
	}
	return nil, newConflictError("completed task has no successful artifact to merge")
}

func (s *Service) createContractRemediationTaskLocked(projectID string, contract *domain.Contract, now time.Time) *domain.Task {
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
	return task
}

func (s *Service) writeSharedSandboxManifest(sharedSandbox *domain.Sandbox, tasks []*domain.Task, artifactIDs []string) error {
	type sharedArtifactRef struct {
		ID        string `json:"id"`
		TaskID    string `json:"taskId"`
		RunID     string `json:"runId"`
		Kind      string `json:"kind"`
		URI       string `json:"uri"`
		Checksum  string `json:"checksum"`
		SizeBytes int64  `json:"sizeBytes"`
	}

	artifactRefs := make([]sharedArtifactRef, 0, len(artifactIDs))
	artifacts := make([]*domain.Artifact, 0, len(artifactIDs))
	sourceSandboxes := make([]*domain.Sandbox, 0, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		artifact, ok := s.artifacts[artifactID]
		if !ok {
			return newNotFoundError("artifact not found for shared sandbox merge")
		}
		artifactRefs = append(artifactRefs, sharedArtifactRef{
			ID:        artifact.ID,
			TaskID:    artifact.TaskID,
			RunID:     artifact.RunID,
			Kind:      artifact.Kind,
			URI:       artifact.URI,
			Checksum:  artifact.Checksum,
			SizeBytes: artifact.SizeBytes,
		})
		artifacts = append(artifacts, artifact)
		run, ok := s.runs[artifact.RunID]
		if !ok || strings.TrimSpace(run.SandboxID) == "" {
			return newConflictError("artifact has no source sandbox for shared merge")
		}
		sourceSandbox, ok := s.sandboxIndex[run.SandboxID]
		if !ok {
			return newConflictError("artifact source sandbox not found for shared merge")
		}
		sourceSandboxes = append(sourceSandboxes, sourceSandbox)
	}
	if err := s.workspaceProvider.MergeShared(context.Background(), sharedSandbox, tasks, artifacts, sourceSandboxes); err != nil {
		return err
	}

	manifest := map[string]any{
		"sandbox":     cloneSandbox(sharedSandbox),
		"tasks":       cloneTasks(tasks),
		"artifacts":   artifactRefs,
		"createdAt":   time.Now().UTC(),
		"mergePassed": true,
	}

	if err := writeJSONFile(filepath.Join(sharedSandbox.WorkspacePath, "manifest.json"), manifest); err != nil {
		return err
	}
	if err := writeWorkspaceManifest(sharedSandbox, sharedSandbox.WorkspacePath, artifactRefs); err != nil {
		return err
	}
	if strings.EqualFold(sharedSandbox.WorkspaceProvider, "git") {
		if err := commitGitWorkspaceChanges(context.Background(), sharedSandbox.WorkspacePath, "multiagent: shared workspace manifest"); err != nil {
			return err
		}
		head, err := gitRevParse(context.Background(), sharedSandbox.WorkspacePath, "HEAD")
		if err != nil {
			return err
		}
		sharedSandbox.WorkspaceHeadRef = head
		if err := writeWorkspaceManifest(sharedSandbox, sharedSandbox.WorkspacePath, artifactRefs); err != nil {
			return err
		}
		return s.workspaceProvider.Publish(context.Background(), sharedSandbox)
	}
	return nil
}

func (s *Service) cleanupWorkspacesLocked(ctx context.Context, projectID string, input CleanupWorkspacesInput, now time.Time) *CleanupWorkspacesResult {
	scope := strings.ToUpper(strings.TrimSpace(input.Scope))
	if scope == "" {
		scope = "PRIVATE"
	}
	selectedIDs := cleanIDSet(input.SandboxIDs)
	result := &CleanupWorkspacesResult{ProjectID: projectID, DryRun: input.DryRun, Results: make([]WorkspaceCleanupResult, 0)}
	retainedHeads := s.retainedWorkspaceHeadsLocked(projectID)
	for _, sandbox := range s.sandboxes[projectID] {
		if sandbox == nil || !strings.EqualFold(sandbox.WorkspaceProvider, "git") {
			continue
		}
		if len(selectedIDs) > 0 {
			if _, ok := selectedIDs[sandbox.ID]; !ok {
				continue
			}
		}
		if scope != "ALL" && sandbox.Scope != scope {
			continue
		}
		if sandbox.Scope == "SHARED" {
			continue
		}
		if sandbox.Status == domain.SandboxStatusFailed && !input.IncludeFailed {
			continue
		}
		if input.OlderThanSeconds > 0 && now.Sub(sandbox.UpdatedAt) < time.Duration(input.OlderThanSeconds)*time.Second {
			continue
		}
		providerResult, err := s.workspaceProvider.Cleanup(ctx, sandbox, workspaceCleanupPolicy{
			DryRun:        input.DryRun,
			DeleteBranch:  input.DeleteBranches,
			RetainedHeads: retainedHeads,
			Reason:        "workspace cleanup",
			Now:           now,
		})
		if providerResult == nil {
			providerResult = &workspaceCleanupResult{SandboxID: sandbox.ID, Provider: sandbox.WorkspaceProvider}
		}
		if err != nil {
			providerResult.Error = err.Error()
		}
		item := s.applyWorkspaceCleanupResultLocked(ctx, projectID, sandbox, providerResult, input.DryRun, now)
		result.Results = append(result.Results, item)
		switch item.Status {
		case "FAILED":
			result.Failed++
		case "SKIPPED":
			result.Skipped++
		}
		if item.WorktreeRemoved {
			result.RemovedWorktrees++
		}
		if item.BranchDeleted {
			result.DeletedBranches++
		}
	}
	return result
}

func (s *Service) applyWorkspaceRebaseResultLocked(ctx context.Context, projectID string, sandbox *domain.Sandbox, providerResult *workspaceRebaseResult, dryRun bool, now time.Time) WorkspaceRebaseResult {
	status := strings.TrimSpace(providerResult.Status)
	if status == "" {
		if providerResult.Error != "" {
			status = "FAILED"
		} else {
			status = "SKIPPED"
		}
	}
	reason := strings.TrimSpace(providerResult.Reason)
	if reason == "" && providerResult.Error != "" {
		reason = providerResult.Error
	}
	if !dryRun {
		switch status {
		case "REBASED", "REBASED_PUBLISH_FAILED", "UP_TO_DATE":
			if strings.TrimSpace(providerResult.NewHeadRef) != "" {
				sandbox.WorkspaceHeadRef = providerResult.NewHeadRef
			}
			if strings.TrimSpace(providerResult.TargetRef) != "" {
				sandbox.WorkspaceBaseRef = providerResult.TargetRef
			}
			sandbox.UpdatedAt = now
			_ = writeWorkspaceManifest(sandbox, sandbox.WorkspacePath, nil)
		}
		action := "WORKSPACE_REBASE_SKIPPED"
		summary := reason
		switch status {
		case "REBASED":
			action = "WORKSPACE_REBASE"
			summary = "workspace rebased"
		case "UP_TO_DATE":
			action = "WORKSPACE_REBASE_UP_TO_DATE"
			summary = "workspace already contains target ref"
		case "FAILED":
			action = "WORKSPACE_REBASE_FAILED"
		case "REBASED_PUBLISH_FAILED":
			action = "WORKSPACE_REBASE_PUBLISH_FAILED"
			summary = "workspace rebased but publish failed"
		}
		if summary == "" {
			summary = status
		}
		s.recordAuditLocked(ctx, projectID, action, "sandbox", sandbox.ID, summary, now)
		if status == "FAILED" || status == "REBASED_PUBLISH_FAILED" {
			alertType := "WORKSPACE_REBASE_FAILED"
			if providerResult.RebaseAborted {
				alertType = "WORKSPACE_REBASE_CONFLICT"
			}
			s.recordAlertLocked(projectID, "ERROR", alertType, sandbox.ID, summary, now)
		}
	}
	return WorkspaceRebaseResult{
		SandboxID:      sandbox.ID,
		Scope:          sandbox.Scope,
		Provider:       providerResult.Provider,
		Status:         status,
		Reason:         reason,
		Branch:         providerResult.Branch,
		TargetRef:      providerResult.TargetRef,
		OldHeadRef:     providerResult.OldHeadRef,
		NewHeadRef:     providerResult.NewHeadRef,
		Ahead:          providerResult.Ahead,
		Behind:         providerResult.Behind,
		Fetched:        providerResult.Fetched,
		RebaseAborted:  providerResult.RebaseAborted,
		Published:      providerResult.Published,
		ConflictLog:    providerResult.ConflictLog,
		ConflictLogRef: providerResult.ConflictLogRef,
		Error:          providerResult.Error,
	}
}

func (s *Service) applyWorkspaceCleanupResultLocked(ctx context.Context, projectID string, sandbox *domain.Sandbox, providerResult *workspaceCleanupResult, dryRun bool, now time.Time) WorkspaceCleanupResult {
	status := "CLEANED"
	reason := strings.TrimSpace(providerResult.SkipReason)
	if providerResult.Error != "" {
		status = "FAILED"
		reason = providerResult.Error
	} else if providerResult.Skipped {
		status = "SKIPPED"
	} else if !providerResult.WorktreeRemoved && !providerResult.BranchDeleted {
		status = "SKIPPED"
		reason = "no cleanup action was needed"
	}
	if dryRun && status == "CLEANED" {
		reason = "dry run"
	}
	if !dryRun {
		sandbox.WorkspaceCleanupStatus = status
		sandbox.WorkspaceCleanedAt = now
		sandbox.WorkspaceCleanupReason = reason
		sandbox.WorkspaceCleanupError = providerResult.Error
		if providerResult.WorktreeRemoved {
			sandbox.WorkspaceWorktreeGone = true
		}
		if providerResult.BranchDeleted {
			sandbox.WorkspaceBranchGone = true
		}
		sandbox.WorkspaceRetainedRef = providerResult.RetainedRef
		sandbox.UpdatedAt = now
	}
	action := "WORKSPACE_CLEANUP_SKIPPED"
	if status == "FAILED" {
		action = "WORKSPACE_CLEANUP_FAILED"
	} else if providerResult.BranchDeleted {
		action = "WORKSPACE_BRANCH_CLEANED"
	} else if providerResult.WorktreeRemoved {
		action = "WORKSPACE_WORKTREE_CLEANED"
	}
	if !dryRun {
		s.recordAuditLocked(ctx, projectID, action, "sandbox", sandbox.ID, reason, now)
	}
	return WorkspaceCleanupResult{
		SandboxID:       sandbox.ID,
		Scope:           sandbox.Scope,
		Provider:        providerResult.Provider,
		Status:          status,
		Reason:          reason,
		Error:           providerResult.Error,
		WorktreeRemoved: providerResult.WorktreeRemoved,
		BranchDeleted:   providerResult.BranchDeleted,
		RetainedRef:     providerResult.RetainedRef,
	}
}

func cleanIDSet(ids []string) map[string]struct{} {
	selected := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			selected[id] = struct{}{}
		}
	}
	return selected
}

func (s *Service) sourceSandboxIDsForArtifactsLocked(artifactIDs []string) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(artifactIDs))
	for _, artifactID := range artifactIDs {
		artifact := s.artifacts[artifactID]
		if artifact == nil {
			continue
		}
		run := s.runs[artifact.RunID]
		if run == nil || strings.TrimSpace(run.SandboxID) == "" {
			continue
		}
		if _, ok := seen[run.SandboxID]; ok {
			continue
		}
		seen[run.SandboxID] = struct{}{}
		ids = append(ids, run.SandboxID)
	}
	return ids
}

func (s *Service) retainedWorkspaceHeadsLocked(projectID string) []string {
	seen := map[string]struct{}{}
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	for _, sandbox := range s.sandboxes[projectID] {
		if sandbox != nil && sandbox.Scope == "SHARED" && sandbox.Status == domain.SandboxStatusReleased {
			add(sandbox.WorkspaceHeadRef)
		}
	}
	for _, snapshot := range s.snapshots[projectID] {
		if snapshot != nil {
			add(snapshot.WorkspaceChecksum)
		}
	}
	add(s.cfg.WorkspaceGitBaseRef)
	return refs
}

func (s *Service) currentBranchLocked(projectID string) string {
	branch := strings.TrimSpace(s.projectBranch[projectID])
	if branch == "" {
		return "main"
	}
	return branch
}

func (s *Service) latestSnapshotIDLocked(projectID string) string {
	items := s.snapshots[projectID]
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1].ID
}

func (s *Service) latestWorkspaceHeadRefLocked(projectID string) string {
	items := s.snapshots[projectID]
	for idx := len(items) - 1; idx >= 0; idx-- {
		if ref := strings.TrimSpace(items[idx].WorkspaceChecksum); ref != "" {
			return ref
		}
	}
	return ""
}

func (s *Service) recordSnapshotLocked(projectID, branch, reason string, stable bool, sourceSnapshotID string, now time.Time) (*domain.Snapshot, error) {
	snapshot, err := s.recordSnapshotWithWorkspaceLocked(projectID, branch, reason, stable, sourceSnapshotID, now, nil)
	return snapshot, err
}

func (s *Service) recordSnapshotWithWorkspaceLocked(projectID, branch, reason string, stable bool, sourceSnapshotID string, now time.Time, workspaceSandbox *domain.Sandbox) (*domain.Snapshot, error) {
	if _, ok := s.projects[projectID]; !ok {
		return nil, newNotFoundError("project not found")
	}
	if strings.TrimSpace(branch) == "" {
		branch = s.currentBranchLocked(projectID)
	}

	snapshot := &domain.Snapshot{
		ID:               nextID("snapshot"),
		ProjectID:        projectID,
		Branch:           branch,
		SourceSnapshotID: strings.TrimSpace(sourceSnapshotID),
		Reason:           strings.TrimSpace(reason),
		StateRef:         "",
		Stable:           stable,
		CreatedAt:        now,
	}
	if workspaceSandbox != nil && strings.TrimSpace(workspaceSandbox.WorkspaceHeadRef) != "" {
		snapshot.WorkspaceChecksum = strings.TrimSpace(workspaceSandbox.WorkspaceHeadRef)
		snapshot.WorkspaceStateRef = fmt.Sprintf("repo://local/%s@%s", workspaceSandbox.WorkspaceBranch, workspaceSandbox.WorkspaceHeadRef)
	}
	state := s.captureProjectSnapshotStateLocked(projectID)
	snapshot.StateRef = "memory://" + snapshot.ID
	if s.usesFileStore() {
		stateRef, checksum, err := s.writeSnapshotRecord(snapshot, state)
		if err != nil {
			return nil, err
		}
		snapshot.StateRef = stateRef
		snapshot.Checksum = checksum
	}

	s.snapshotIndex[snapshot.ID] = snapshot
	s.snapshots[projectID] = append(s.snapshots[projectID], snapshot)
	s.snapshotState[snapshot.ID] = state
	s.projectBranch[projectID] = branch
	if stable {
		s.stableBranch[projectID] = snapshot.ID
	}

	return snapshot, nil
}

func (s *Service) rollbackToSnapshotLocked(projectID, snapshotID, reason string, now time.Time) (*RollbackResult, error) {
	snapshot, ok := s.snapshotIndex[snapshotID]
	if !ok || snapshot.ProjectID != projectID {
		return nil, newNotFoundError("snapshot not found")
	}
	state, err := s.resolveSnapshotStateLocked(snapshot)
	if err != nil {
		return nil, err
	}

	if err := s.validateWorkspaceSnapshotLocked(context.Background(), snapshot); err != nil {
		return nil, err
	}

	previousBranch := s.currentBranchLocked(projectID)
	activeBranch := s.nextRollbackBranchLocked(projectID, snapshot.Branch)
	s.restoreProjectFromSnapshotLocked(projectID, state)
	clearedContexts := s.clearProjectContextsLocked(projectID)
	if project := s.projects[projectID]; project != nil {
		project.UpdatedAt = now
	}

	var workspaceResult *RollbackWorkspaceResult
	if restoredSandbox, restoredResult, err := s.restoreWorkspaceSnapshotLocked(context.Background(), projectID, snapshot, activeBranch, now); err != nil {
		return nil, err
	} else {
		workspaceResult = restoredResult
		if restoredSandbox != nil {
			rollbackSnapshot, err := s.recordSnapshotWithWorkspaceLocked(projectID, activeBranch, reason, snapshot.Stable, snapshot.ID, now, restoredSandbox)
			if err != nil {
				return nil, err
			}
			return &RollbackResult{
				Snapshot:        *cloneSnapshot(rollbackSnapshot),
				RestoredFrom:    *cloneSnapshot(snapshot),
				PreviousBranch:  previousBranch,
				ActiveBranch:    activeBranch,
				ClearedContexts: clearedContexts,
				RestoredTasks:   len(s.taskOrder[projectID]),
				Workspace:       workspaceResult,
				Message:         "project rolled back to snapshot " + snapshot.ID,
			}, nil
		}
	}

	rollbackSnapshot, err := s.recordSnapshotLocked(projectID, activeBranch, reason, snapshot.Stable, snapshot.ID, now)
	if err != nil {
		return nil, err
	}

	return &RollbackResult{
		Snapshot:        *cloneSnapshot(rollbackSnapshot),
		RestoredFrom:    *cloneSnapshot(snapshot),
		PreviousBranch:  previousBranch,
		ActiveBranch:    activeBranch,
		ClearedContexts: clearedContexts,
		RestoredTasks:   len(s.taskOrder[projectID]),
		Workspace:       workspaceResult,
		Message:         "project rolled back to snapshot " + snapshot.ID,
	}, nil
}

func parseWorkspaceSnapshotRef(ref string) (workspaceSnapshotRef, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "repo://") {
		return workspaceSnapshotRef{}, newConflictError("unsupported workspace snapshot state ref")
	}
	body := strings.TrimPrefix(ref, "repo://")
	provider, rest, ok := strings.Cut(body, "/")
	if !ok || strings.TrimSpace(provider) == "" {
		return workspaceSnapshotRef{}, newConflictError("workspace snapshot ref provider is required")
	}
	at := strings.LastIndex(rest, "@")
	if at <= 0 || at == len(rest)-1 {
		return workspaceSnapshotRef{}, newConflictError("workspace snapshot ref must include branch and commit")
	}
	branch := strings.TrimSpace(rest[:at])
	commit := strings.TrimSpace(rest[at+1:])
	if branch == "" || commit == "" {
		return workspaceSnapshotRef{}, newConflictError("workspace snapshot ref must include branch and commit")
	}
	return workspaceSnapshotRef{Provider: provider, Branch: branch, Commit: commit}, nil
}

func (s *Service) validateWorkspaceSnapshotLocked(ctx context.Context, snapshot *domain.Snapshot) error {
	_, _, _, err := s.resolveWorkspaceSnapshotRefLocked(ctx, snapshot)
	return err
}

func (s *Service) resolveWorkspaceSnapshotRefLocked(ctx context.Context, snapshot *domain.Snapshot) (workspaceSnapshotRef, string, string, error) {
	workspaceRef := strings.TrimSpace(snapshot.WorkspaceStateRef)
	if workspaceRef == "" {
		return workspaceSnapshotRef{}, "", "", nil
	}
	if !strings.EqualFold(s.workspaceProvider.Name(), "git") {
		return workspaceSnapshotRef{}, "", "", newConflictError("git workspace snapshot restore requires git workspace provider")
	}
	parsed, err := parseWorkspaceSnapshotRef(workspaceRef)
	if err != nil {
		return workspaceSnapshotRef{}, "", "", err
	}
	if parsed.Provider != "local" {
		return workspaceSnapshotRef{}, "", "", newConflictError("unsupported workspace snapshot provider")
	}
	if !strings.HasPrefix(parsed.Branch, "multiagent/") || !strings.Contains(parsed.Branch, "/shared/") {
		return workspaceSnapshotRef{}, "", "", newConflictError("workspace snapshot branch is not a managed shared branch")
	}
	checksum := strings.TrimSpace(snapshot.WorkspaceChecksum)
	if checksum == "" {
		return workspaceSnapshotRef{}, "", "", newConflictError("workspace snapshot checksum is required")
	}
	resolvedCommit, err := gitRevParse(ctx, s.cfg.WorkspaceGitRepoPath, parsed.Commit+"^{commit}")
	if err != nil {
		return workspaceSnapshotRef{}, "", "", newConflictError("workspace snapshot commit is not available")
	}
	resolvedChecksum, err := gitRevParse(ctx, s.cfg.WorkspaceGitRepoPath, checksum+"^{commit}")
	if err != nil {
		return workspaceSnapshotRef{}, "", "", newConflictError("workspace snapshot checksum is not available")
	}
	if resolvedCommit != resolvedChecksum {
		return workspaceSnapshotRef{}, "", "", newConflictError("workspace snapshot checksum mismatch")
	}
	return parsed, workspaceRef, resolvedCommit, nil
}

func (s *Service) restoreWorkspaceSnapshotLocked(ctx context.Context, projectID string, snapshot *domain.Snapshot, activeBranch string, now time.Time) (*domain.Sandbox, *RollbackWorkspaceResult, error) {
	_, workspaceRef, resolvedCommit, err := s.resolveWorkspaceSnapshotRefLocked(ctx, snapshot)
	if err != nil {
		return nil, nil, err
	}
	if workspaceRef == "" {
		return nil, nil, nil
	}

	sandbox := &domain.Sandbox{
		ID:                   nextID("sandbox"),
		ProjectID:            projectID,
		AgentType:            "snapshot-rollback",
		Scope:                "SHARED",
		Status:               domain.SandboxStatusActive,
		CreatedAt:            now,
		UpdatedAt:            now,
		WorkspaceProvider:    "git",
		WorkspaceBaseRef:     resolvedCommit,
		WorkspaceManifestRef: "file://" + filepath.Join(s.cfg.SandboxRoot, "shared", projectID, "workspace-manifest-pending"),
	}
	sandbox.RootPath = filepath.Join(s.cfg.SandboxRoot, "shared", projectID, sandbox.ID)
	sandbox.WorkspacePath = filepath.Join(sandbox.RootPath, "workspace")
	sandbox.WorkspaceManifestRef = "file://" + filepath.Join(sandbox.WorkspacePath, ".multiagent", "workspace-manifest.json")
	branch := gitBranchName("multiagent", projectID, "rollback", activeBranch, sandbox.ID)
	if err := s.workspaceProvider.RestoreSharedSnapshot(ctx, sandbox, branch, resolvedCommit); err != nil {
		return nil, nil, err
	}
	if err := writeWorkspaceManifest(sandbox, sandbox.WorkspacePath, nil); err != nil {
		return nil, nil, err
	}
	sandbox.Status = domain.SandboxStatusReleased
	sandbox.UpdatedAt = now
	s.sandboxIndex[sandbox.ID] = sandbox
	s.sandboxes[projectID] = append(s.sandboxes[projectID], sandbox)
	result := &RollbackWorkspaceResult{
		Restored:    true,
		Sandbox:     *cloneSandbox(sandbox),
		Branch:      sandbox.WorkspaceBranch,
		HeadRef:     sandbox.WorkspaceHeadRef,
		StateRef:    fmt.Sprintf("repo://local/%s@%s", sandbox.WorkspaceBranch, sandbox.WorkspaceHeadRef),
		OriginalRef: workspaceRef,
	}
	return sandbox, result, nil
}

func (s *Service) resolveSnapshotStateLocked(snapshot *domain.Snapshot) (*projectSnapshotState, error) {
	if snapshot == nil {
		return nil, newNotFoundError("snapshot not found")
	}
	if state, ok := s.snapshotState[snapshot.ID]; ok {
		return state, nil
	}
	stateRef := strings.TrimSpace(snapshot.StateRef)
	if stateRef == "" {
		return nil, newNotFoundError("snapshot state not found")
	}
	if strings.HasPrefix(stateRef, "memory://") {
		stateID := strings.TrimPrefix(stateRef, "memory://")
		if state, ok := s.snapshotState[stateID]; ok {
			return state, nil
		}
		return nil, newNotFoundError("snapshot memory state not found")
	}
	if strings.HasPrefix(stateRef, "file://") {
		path := strings.TrimPrefix(stateRef, "file://")
		payload, err := os.ReadFile(path)
		if err != nil {
			return nil, newNotFoundError("snapshot file state not found")
		}
		if checksum := strings.TrimSpace(snapshot.Checksum); checksum != "" {
			sum := sha256.Sum256(payload)
			if !strings.EqualFold(hex.EncodeToString(sum[:]), checksum) {
				return nil, newConflictError("snapshot file checksum mismatch")
			}
		}
		var state projectSnapshotState
		if err := json.Unmarshal(payload, &state); err != nil {
			return nil, newConflictError("snapshot file state is invalid")
		}
		return &state, nil
	}
	if strings.HasPrefix(stateRef, "repo://") {
		return nil, newConflictError("repo service-state restore is not implemented; Git workspace restore uses workspaceStateRef")
	}
	return nil, newNotFoundError("unsupported snapshot state ref")
}

func (s *Service) nextRollbackBranchLocked(projectID, baseBranch string) string {
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch == "" {
		baseBranch = s.currentBranchLocked(projectID)
	}
	s.branchSeq[projectID]++
	return fmt.Sprintf("%s-rollback-%d", baseBranch, s.branchSeq[projectID])
}

func (s *Service) captureProjectSnapshotStateLocked(projectID string) *projectSnapshotState {
	state := &projectSnapshotState{
		Project:       cloneProject(s.projects[projectID]),
		Requirements:  cloneRequirements(s.requirements[projectID]),
		Plans:         clonePlans(s.plans[projectID]),
		Contracts:     cloneContracts(s.contracts[projectID]),
		Contexts:      make(map[string][]*domain.ContextInjection),
		Sandboxes:     cloneSandboxes(s.sandboxes[projectID]),
		Tasks:         make(map[string]*domain.Task),
		TaskOrder:     append([]string(nil), s.taskOrder[projectID]...),
		Runs:          make(map[string]*domain.AgentRun),
		RunOrder:      append([]string(nil), s.runOrder[projectID]...),
		Artifacts:     make(map[string]*domain.Artifact),
		ArtifactOrder: append([]string(nil), s.artifactOrder[projectID]...),
	}

	for _, taskID := range state.TaskOrder {
		if task, ok := s.tasks[taskID]; ok {
			state.Tasks[taskID] = cloneTask(task)
		}
		if history := s.contexts[taskID]; len(history) > 0 {
			state.Contexts[taskID] = cloneContextHistory(history)
		}
	}
	for _, runID := range state.RunOrder {
		if run, ok := s.runs[runID]; ok {
			state.Runs[runID] = cloneRun(run)
		}
	}
	for _, artifactID := range state.ArtifactOrder {
		if artifact, ok := s.artifacts[artifactID]; ok {
			state.Artifacts[artifactID] = cloneArtifact(artifact)
		}
	}

	return state
}

func (s *Service) restoreProjectFromSnapshotLocked(projectID string, state *projectSnapshotState) {
	s.clearProjectStateLocked(projectID)

	s.projects[projectID] = cloneProject(state.Project)
	s.requirements[projectID] = cloneRequirements(state.Requirements)
	s.plans[projectID] = clonePlans(state.Plans)
	for _, plan := range s.plans[projectID] {
		s.planIndex[plan.ID] = plan
	}

	s.contracts[projectID] = cloneContracts(state.Contracts)
	for _, contract := range s.contracts[projectID] {
		s.contractIndex[contract.ID] = contract
	}

	s.taskOrder[projectID] = append([]string(nil), state.TaskOrder...)
	for _, taskID := range state.TaskOrder {
		if task, ok := state.Tasks[taskID]; ok {
			s.tasks[taskID] = cloneTask(task)
		}
		if history, ok := state.Contexts[taskID]; ok {
			s.contexts[taskID] = cloneContextHistory(history)
			for _, injection := range s.contexts[taskID] {
				s.contextIndex[injection.ID] = injection
			}
		}
	}

	s.sandboxes[projectID] = cloneSandboxes(state.Sandboxes)
	for _, sandbox := range s.sandboxes[projectID] {
		s.sandboxIndex[sandbox.ID] = sandbox
	}

	s.runOrder[projectID] = append([]string(nil), state.RunOrder...)
	for _, runID := range state.RunOrder {
		if run, ok := state.Runs[runID]; ok {
			s.runs[runID] = cloneRun(run)
		}
	}

	s.artifactOrder[projectID] = append([]string(nil), state.ArtifactOrder...)
	for _, artifactID := range state.ArtifactOrder {
		if artifact, ok := state.Artifacts[artifactID]; ok {
			s.artifacts[artifactID] = cloneArtifact(artifact)
		}
	}
}

func (s *Service) clearProjectStateLocked(projectID string) {
	for _, plan := range s.plans[projectID] {
		delete(s.planIndex, plan.ID)
	}
	for _, contract := range s.contracts[projectID] {
		delete(s.contractIndex, contract.ID)
	}
	for _, taskID := range s.taskOrder[projectID] {
		for _, injection := range s.contexts[taskID] {
			delete(s.contextIndex, injection.ID)
		}
		delete(s.contexts, taskID)
		delete(s.tasks, taskID)
		delete(s.sandboxFaults, taskID)
	}
	for _, sandbox := range s.sandboxes[projectID] {
		delete(s.sandboxIndex, sandbox.ID)
	}
	for _, runID := range s.runOrder[projectID] {
		delete(s.runs, runID)
	}
	for _, artifactID := range s.artifactOrder[projectID] {
		delete(s.artifacts, artifactID)
	}
	for _, override := range s.overrides[projectID] {
		delete(s.overrideIndex, override.ID)
	}

	delete(s.requirements, projectID)
	delete(s.plans, projectID)
	delete(s.contracts, projectID)
	delete(s.overrides, projectID)
	delete(s.sandboxes, projectID)
	delete(s.taskOrder, projectID)
	delete(s.runOrder, projectID)
	delete(s.artifactOrder, projectID)
}

func (s *Service) clearProjectContextsLocked(projectID string) int {
	cleared := 0
	for _, taskID := range s.taskOrder[projectID] {
		history := s.contexts[taskID]
		cleared += len(history)
		for _, injection := range history {
			delete(s.contextIndex, injection.ID)
		}
		delete(s.contexts, taskID)
	}
	return cleared
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
	return fmt.Sprintf(`# %s - Standard Delivery Bundle

此交付包由 MultiAgentCom 的交付引擎生成，包含最小可运行的后端、前端和本地编排文件。

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

## Quick Start

1. 安装 Docker 和 Docker Compose。
2. 在交付包根目录执行: docker compose up --build
3. 打开: http://127.0.0.1:3000 验证前端预览。
4. 访问: http://127.0.0.1:8081/health 验证后端服务。

## Local Entrypoints

- Frontend: http://127.0.0.1:3000
- Backend health: http://127.0.0.1:8081/health
- Compose file: docker-compose.yml

## Bundle Contract

- Contract version: delivery.bundle.v1
- metadata/manifest.json: 机器可读交付清单，包含必需文件、SHA-256、大小和本地入口。
- metadata/release-gate.json: 本地 release gate 报告，status 为 PASS 表示交付包结构已通过生成时校验。

## Bundle Contents

- generated-app/: Go 后端服务，包含 go.mod、main.go、Dockerfile
- web-app/: Node 前端服务，包含 package.json、server.js、index.html、Dockerfile
- docker-compose.yml: 本地一键启动编排文件
- metadata/prd.json: 结构化 PRD
- metadata/task.json: 任务快照
- metadata/run.json: 执行快照
- metadata/release-gate.json: 本地 release gate 报告
- metadata/manifest.json: delivery.bundle.v1 交付清单
`, project.Name, project.ID, task.ID, task.Type, task.AssigneeAgent, plan.Version, plan.Goal, renderBulletList(plan.Scope), renderBulletList(plan.AcceptanceCriteria))
}

func renderGeneratedSource(project *domain.Project, plan *domain.Plan) string {
	return fmt.Sprintf("package main\n\n"+
		"import (\n"+
		"\t\"encoding/json\"\n"+
		"\t\"fmt\"\n"+
		"\t\"net/http\"\n"+
		")\n\n"+
		"type todo struct {\n"+
		"\tID        string `json:\"id\"`\n"+
		"\tTitle     string `json:\"title\"`\n"+
		"\tCompleted bool   `json:\"completed\"`\n"+
		"}\n\n"+
		"func main() {\n"+
		"\tmux := http.NewServeMux()\n"+
		"\tmux.HandleFunc(\"/health\", func(w http.ResponseWriter, r *http.Request) {\n"+
		"\t\tw.Header().Set(\"Content-Type\", \"application/json\")\n"+
		"\t\t_ = json.NewEncoder(w).Encode(map[string]any{\n"+
		"\t\t\t\"status\": \"ok\",\n"+
		"\t\t\t\"project\": %q,\n"+
		"\t\t\t\"plan\": %q,\n"+
		"\t\t})\n"+
		"\t})\n"+
		"\tmux.HandleFunc(\"/api/todos\", func(w http.ResponseWriter, r *http.Request) {\n"+
		"\t\tw.Header().Set(\"Content-Type\", \"application/json\")\n"+
		"\t\t_ = json.NewEncoder(w).Encode([]todo{\n"+
		"\t\t\t{ID: \"todo-1\", Title: \"Review delivery bundle\", Completed: false},\n"+
		"\t\t\t{ID: \"todo-2\", Title: \"Verify docker compose startup\", Completed: true},\n"+
		"\t\t})\n"+
		"\t})\n\n"+
		"\tfmt.Println(\"MultiAgentCom generated backend listening on :8081\")\n"+
		"\tif err := http.ListenAndServe(\":8081\", mux); err != nil {\n"+
		"\t\tpanic(err)\n"+
		"\t}\n"+
		"}\n",
		project.Name,
		plan.Title,
	)
}

func renderBackendDockerfile() string {
	return `FROM golang:1.25-alpine
WORKDIR /app
COPY . .
RUN go build -o service .
EXPOSE 8081
CMD ["./service"]
`
}

func renderFrontendPackageJSON(project *domain.Project) string {
	return fmt.Sprintf(`{
  "name": "%s-preview",
  "version": "1.0.0",
  "private": true,
  "scripts": {
    "start": "node server.js"
  }
}
`, strings.ToLower(strings.ReplaceAll(project.Name, " ", "-")))
}

func renderFrontendServerJS(project *domain.Project, plan *domain.Plan) string {
	return fmt.Sprintf(`const http = require("http");
const fs = require("fs");
const path = require("path");

const indexPath = path.join(__dirname, "index.html");
const revision = %q;

http.createServer((req, res) => {
  if (req.url === "/status") {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ status: "ok", revision }));
    return;
  }

  res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
  res.end(fs.readFileSync(indexPath, "utf8"));
}).listen(3000, () => {
  console.log("Preview server for %s / %s listening on :3000");
});
`, escapeForDoubleQuotedString(project.ID+"-"+plan.ID), escapeForDoubleQuotedString(project.Name), escapeForDoubleQuotedString(plan.Title))
}

func renderFrontendIndexHTML(project *domain.Project, plan *domain.Plan) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>%s Preview</title>
  <style>
    body { font-family: "Avenir Next", "Segoe UI", sans-serif; margin: 0; background: #f4f7fb; color: #1f2937; }
    main { max-width: 880px; margin: 0 auto; padding: 32px 20px 48px; }
    .hero { background: white; border-radius: 24px; padding: 24px; box-shadow: 0 16px 40px rgba(15, 23, 42, 0.08); margin-bottom: 20px; }
    .todo { background: white; border-radius: 24px; padding: 20px; box-shadow: 0 16px 40px rgba(15, 23, 42, 0.08); }
    ul { list-style: none; padding: 0; display: grid; gap: 12px; }
    li { display: flex; gap: 10px; align-items: center; border: 1px solid #d7deea; border-radius: 16px; padding: 14px; }
    button { border: 0; border-radius: 999px; background: #0f62fe; color: white; padding: 10px 16px; cursor: pointer; }
    input[type="text"] { flex: 1; border: 0; font: inherit; background: transparent; }
    .muted { color: #6b7280; }
  </style>
</head>
<body>
  <main>
    <section class="hero">
      <p class="muted">Generated Preview</p>
      <h1>%s</h1>
      <p>%s</p>
      <button type="button" onclick="fetch('/status').then((r) => r.json()).then((data) => alert('Revision: ' + data.revision))">Check hot reload revision</button>
    </section>
    <section class="todo">
      <h2>Todo demo</h2>
      <ul id="todos"></ul>
    </section>
  </main>
  <script>
    const todos = [
      { id: "todo-1", title: "Open preview URL", completed: true },
      { id: "todo-2", title: "Verify README startup steps", completed: false },
      { id: "todo-3", title: "Export delivery bundle", completed: false }
    ];
    const root = document.getElementById("todos");
    function render() {
      root.innerHTML = "";
      todos.forEach((todo) => {
        const li = document.createElement("li");
        const checkbox = document.createElement("input");
        checkbox.type = "checkbox";
        checkbox.checked = todo.completed;
        checkbox.onchange = () => {
          todo.completed = checkbox.checked;
          render();
        };
        const input = document.createElement("input");
        input.type = "text";
        input.value = todo.title;
        input.oninput = (event) => {
          todo.title = event.target.value;
        };
        li.appendChild(checkbox);
        li.appendChild(input);
        root.appendChild(li);
      });
    }
    render();
  </script>
</body>
</html>
`, escapeForDoubleQuotedString(project.Name), escapeForDoubleQuotedString(project.Name), escapeForDoubleQuotedString(plan.Goal))
}

func renderFrontendDockerfile() string {
	return `FROM node:22-alpine
WORKDIR /app
COPY . .
EXPOSE 3000
CMD ["npm", "run", "start"]
`
}

func renderDockerCompose() string {
	return `services:
  backend:
    build: ./generated-app
    ports:
      - "8081:8081"
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8081/health"]
      interval: 10s
      timeout: 3s
      retries: 3
  frontend:
    build: ./web-app
    ports:
      - "3000:3000"
    depends_on:
      backend:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://127.0.0.1:3000/status"]
      interval: 10s
      timeout: 3s
      retries: 3
`
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

func cloneRequirements(items []*domain.Requirement) []*domain.Requirement {
	result := make([]*domain.Requirement, 0, len(items))
	for _, item := range items {
		result = append(result, cloneRequirement(item))
	}
	return result
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

func clonePlans(items []*domain.Plan) []*domain.Plan {
	result := make([]*domain.Plan, 0, len(items))
	for _, item := range items {
		result = append(result, clonePlan(item))
	}
	return result
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

func cloneContracts(items []*domain.Contract) []*domain.Contract {
	result := make([]*domain.Contract, 0, len(items))
	for _, item := range items {
		result = append(result, cloneContract(item))
	}
	return result
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

func cloneContextHistory(items []*domain.ContextInjection) []*domain.ContextInjection {
	result := make([]*domain.ContextInjection, 0, len(items))
	for _, item := range items {
		result = append(result, cloneContextInjection(item))
	}
	return result
}

func cloneSandbox(sandbox *domain.Sandbox) *domain.Sandbox {
	if sandbox == nil {
		return nil
	}
	copy := *sandbox
	return &copy
}

func cloneSandboxes(items []*domain.Sandbox) []*domain.Sandbox {
	result := make([]*domain.Sandbox, 0, len(items))
	for _, item := range items {
		result = append(result, cloneSandbox(item))
	}
	return result
}

func cloneSnapshot(snapshot *domain.Snapshot) *domain.Snapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	return &copy
}

func cloneHumanOverride(override *domain.HumanOverride) *domain.HumanOverride {
	if override == nil {
		return nil
	}
	copy := *override
	return &copy
}

func cloneCodeLock(lock *domain.CodeLock) *domain.CodeLock {
	if lock == nil {
		return nil
	}
	copy := *lock
	return &copy
}

func clonePreview(preview *domain.Preview) *domain.Preview {
	if preview == nil {
		return nil
	}
	copy := *preview
	return &copy
}

func cloneCommunicationLog(item *domain.CommunicationLog) *domain.CommunicationLog {
	if item == nil {
		return nil
	}
	copy := *item
	return &copy
}

func cloneAuditLog(item *domain.AuditLog) *domain.AuditLog {
	if item == nil {
		return nil
	}
	copy := *item
	return &copy
}

func cloneAlert(item *domain.Alert) *domain.Alert {
	if item == nil {
		return nil
	}
	copy := *item
	return &copy
}

func cloneProjectMap(items map[string]*domain.Project) map[string]*domain.Project {
	result := make(map[string]*domain.Project, len(items))
	for key, item := range items {
		result[key] = cloneProject(item)
	}
	return result
}

func cloneRequirementMap(items map[string][]*domain.Requirement) map[string][]*domain.Requirement {
	result := make(map[string][]*domain.Requirement, len(items))
	for key, item := range items {
		result[key] = cloneRequirements(item)
	}
	return result
}

func clonePlanMap(items map[string][]*domain.Plan) map[string][]*domain.Plan {
	result := make(map[string][]*domain.Plan, len(items))
	for key, item := range items {
		result[key] = clonePlans(item)
	}
	return result
}

func cloneContractMap(items map[string][]*domain.Contract) map[string][]*domain.Contract {
	result := make(map[string][]*domain.Contract, len(items))
	for key, item := range items {
		result[key] = cloneContracts(item)
	}
	return result
}

func cloneContextMap(items map[string][]*domain.ContextInjection) map[string][]*domain.ContextInjection {
	result := make(map[string][]*domain.ContextInjection, len(items))
	for key, item := range items {
		result[key] = cloneContextHistory(item)
	}
	return result
}

func cloneOverrideMap(items map[string][]*domain.HumanOverride) map[string][]*domain.HumanOverride {
	result := make(map[string][]*domain.HumanOverride, len(items))
	for key, history := range items {
		result[key] = make([]*domain.HumanOverride, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], cloneHumanOverride(item))
		}
	}
	return result
}

func cloneLockMap(items map[string][]*domain.CodeLock) map[string][]*domain.CodeLock {
	result := make(map[string][]*domain.CodeLock, len(items))
	for key, history := range items {
		result[key] = make([]*domain.CodeLock, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], cloneCodeLock(item))
		}
	}
	return result
}

func clonePreviewMap(items map[string][]*domain.Preview) map[string][]*domain.Preview {
	result := make(map[string][]*domain.Preview, len(items))
	for key, history := range items {
		result[key] = make([]*domain.Preview, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], clonePreview(item))
		}
	}
	return result
}

func cloneCommunicationMap(items map[string][]*domain.CommunicationLog) map[string][]*domain.CommunicationLog {
	result := make(map[string][]*domain.CommunicationLog, len(items))
	for key, history := range items {
		result[key] = make([]*domain.CommunicationLog, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], cloneCommunicationLog(item))
		}
	}
	return result
}

func cloneAuditMap(items map[string][]*domain.AuditLog) map[string][]*domain.AuditLog {
	result := make(map[string][]*domain.AuditLog, len(items))
	for key, history := range items {
		result[key] = make([]*domain.AuditLog, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], cloneAuditLog(item))
		}
	}
	return result
}

func cloneAlertMap(items map[string][]*domain.Alert) map[string][]*domain.Alert {
	result := make(map[string][]*domain.Alert, len(items))
	for key, history := range items {
		result[key] = make([]*domain.Alert, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], cloneAlert(item))
		}
	}
	return result
}

func cloneSandboxMap(items map[string][]*domain.Sandbox) map[string][]*domain.Sandbox {
	result := make(map[string][]*domain.Sandbox, len(items))
	for key, item := range items {
		result[key] = cloneSandboxes(item)
	}
	return result
}

func cloneSnapshotMap(items map[string][]*domain.Snapshot) map[string][]*domain.Snapshot {
	result := make(map[string][]*domain.Snapshot, len(items))
	for key, history := range items {
		result[key] = make([]*domain.Snapshot, 0, len(history))
		for _, item := range history {
			result[key] = append(result[key], cloneSnapshot(item))
		}
	}
	return result
}

func cloneSnapshotStateMap(items map[string]*projectSnapshotState) map[string]*projectSnapshotState {
	result := make(map[string]*projectSnapshotState, len(items))
	for key, item := range items {
		result[key] = cloneProjectSnapshotState(item)
	}
	return result
}

func cloneProjectSnapshotState(state *projectSnapshotState) *projectSnapshotState {
	if state == nil {
		return nil
	}
	return &projectSnapshotState{
		Project:       cloneProject(state.Project),
		Requirements:  cloneRequirements(state.Requirements),
		Plans:         clonePlans(state.Plans),
		Contracts:     cloneContracts(state.Contracts),
		Contexts:      cloneContextMap(state.Contexts),
		Sandboxes:     cloneSandboxes(state.Sandboxes),
		Tasks:         cloneTaskMap(state.Tasks),
		TaskOrder:     append([]string(nil), state.TaskOrder...),
		Runs:          cloneRunMap(state.Runs),
		RunOrder:      append([]string(nil), state.RunOrder...),
		Artifacts:     cloneArtifactMap(state.Artifacts),
		ArtifactOrder: append([]string(nil), state.ArtifactOrder...),
	}
}

func cloneTaskMap(items map[string]*domain.Task) map[string]*domain.Task {
	result := make(map[string]*domain.Task, len(items))
	for key, item := range items {
		result[key] = cloneTask(item)
	}
	return result
}

func cloneRunMap(items map[string]*domain.AgentRun) map[string]*domain.AgentRun {
	result := make(map[string]*domain.AgentRun, len(items))
	for key, item := range items {
		result[key] = cloneRun(item)
	}
	return result
}

func cloneArtifactMap(items map[string]*domain.Artifact) map[string]*domain.Artifact {
	result := make(map[string]*domain.Artifact, len(items))
	for key, item := range items {
		result[key] = cloneArtifact(item)
	}
	return result
}

func cloneStringMap(items map[string]string) map[string]string {
	result := make(map[string]string, len(items))
	for key, item := range items {
		result[key] = item
	}
	return result
}

func cloneStringSliceMap(items map[string][]string) map[string][]string {
	result := make(map[string][]string, len(items))
	for key, item := range items {
		result[key] = append([]string(nil), item...)
	}
	return result
}

func cloneIntMap(items map[string]int) map[string]int {
	result := make(map[string]int, len(items))
	for key, item := range items {
		result[key] = item
	}
	return result
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
