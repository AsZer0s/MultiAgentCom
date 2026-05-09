package domain

import (
	"fmt"
	"time"
)

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Requirement struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"projectId"`
	Title           string    `json:"title"`
	Content         string    `json:"content"`
	Constraints     []string  `json:"constraints,omitempty"`
	AcceptanceHints []string  `json:"acceptanceHints,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

type Plan struct {
	ID                 string    `json:"id"`
	ProjectID          string    `json:"projectId"`
	RequirementID      string    `json:"requirementId"`
	Version            int       `json:"version"`
	Title              string    `json:"title"`
	Goal               string    `json:"goal"`
	Scope              []string  `json:"scope"`
	Constraints        []string  `json:"constraints"`
	AcceptanceCriteria []string  `json:"acceptanceCriteria"`
	Assumptions        []string  `json:"assumptions"`
	CreatedAt          time.Time `json:"createdAt"`
}

type ContractField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type ContractSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Fields      []ContractField `json:"fields"`
}

type ContractEndpoint struct {
	Name        string `json:"name"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type Contract struct {
	ID            string             `json:"id"`
	ProjectID     string             `json:"projectId"`
	RequirementID string             `json:"requirementId"`
	PlanID        string             `json:"planId"`
	Version       int                `json:"version"`
	Name          string             `json:"name"`
	Summary       string             `json:"summary"`
	Endpoints     []ContractEndpoint `json:"endpoints"`
	Schemas       []ContractSchema   `json:"schemas"`
	CreatedAt     time.Time          `json:"createdAt"`
}

type ContextSource struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Version string `json:"version,omitempty"`
}

type ContextSection struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

type ContextInjection struct {
	ID        string           `json:"id"`
	ProjectID string           `json:"projectId"`
	TaskID    string           `json:"taskId"`
	Role      string           `json:"role"`
	Version   int              `json:"version"`
	Summary   string           `json:"summary"`
	Sources   []ContextSource  `json:"sources"`
	Sections  []ContextSection `json:"sections"`
	CreatedAt time.Time        `json:"createdAt"`
}

type SandboxStatus string

const (
	SandboxStatusActive   SandboxStatus = "ACTIVE"
	SandboxStatusReleased SandboxStatus = "RELEASED"
	SandboxStatusFailed   SandboxStatus = "FAILED"
)

type Sandbox struct {
	ID                   string        `json:"id"`
	ProjectID            string        `json:"projectId"`
	RunID                string        `json:"runId"`
	TaskID               string        `json:"taskId"`
	AgentType            string        `json:"agentType"`
	Scope                string        `json:"scope"`
	RootPath             string        `json:"rootPath"`
	WorkspacePath        string        `json:"workspacePath,omitempty"`
	WorkspaceProvider    string        `json:"workspaceProvider,omitempty"`
	WorkspaceManifestRef string        `json:"workspaceManifestRef,omitempty"`
	Status               SandboxStatus `json:"status"`
	FailureReason        string        `json:"failureReason,omitempty"`
	CreatedAt            time.Time     `json:"createdAt"`
	UpdatedAt            time.Time     `json:"updatedAt"`
}

type Snapshot struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"projectId"`
	Branch           string    `json:"branch"`
	SourceSnapshotID string    `json:"sourceSnapshotId,omitempty"`
	Reason           string    `json:"reason"`
	StateRef         string    `json:"stateRef"`
	Checksum         string    `json:"checksum,omitempty"`
	Stable           bool      `json:"stable"`
	CreatedAt        time.Time `json:"createdAt"`
}

type HumanOverride struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	TaskID      string    `json:"taskId"`
	Operator    string    `json:"operator"`
	Instruction string    `json:"instruction"`
	LockScope   string    `json:"lockScope,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	AppliedAt   time.Time `json:"appliedAt,omitempty"`
}

type CodeLock struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	TaskID     string    `json:"taskId,omitempty"`
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	LockMode   string    `json:"lockMode,omitempty"`
	Language   string    `json:"language,omitempty"`
	SymbolKind string    `json:"symbolKind,omitempty"`
	SymbolName string    `json:"symbolName,omitempty"`
	CreatedBy  string    `json:"createdBy"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Preview struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	SandboxID string    `json:"sandboxId"`
	Status    string    `json:"status"`
	URL       string    `json:"url"`
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CommunicationLog struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	Version    string    `json:"version"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Type       string    `json:"type"`
	TaskID     string    `json:"taskId"`
	PayloadRef string    `json:"payloadRef,omitempty"`
	Checksum   string    `json:"checksum"`
	Timestamp  time.Time `json:"timestamp"`
}

type AuditLog struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"projectId"`
	Actor        string    `json:"actor"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Summary      string    `json:"summary"`
	Timestamp    time.Time `json:"timestamp"`
}

type Alert struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"projectId"`
	Severity   string    `json:"severity"`
	Type       string    `json:"type"`
	ResourceID string    `json:"resourceId"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
}

type TaskStatus string

const (
	TaskStatusCreated       TaskStatus = "CREATED"
	TaskStatusInProgress    TaskStatus = "IN_PROGRESS"
	TaskStatusHumanOverride TaskStatus = "HUMAN_OVERRIDE"
	TaskStatusDone          TaskStatus = "DONE"
	TaskStatusFailed        TaskStatus = "FAILED"
)

type TaskTransition struct {
	From   TaskStatus `json:"from"`
	To     TaskStatus `json:"to"`
	Reason string     `json:"reason"`
	At     time.Time  `json:"at"`
}

type Task struct {
	ID            string           `json:"id"`
	ProjectID     string           `json:"projectId"`
	PlanID        string           `json:"planId"`
	Name          string           `json:"name"`
	Type          string           `json:"type"`
	Status        TaskStatus       `json:"status"`
	AssigneeAgent string           `json:"assigneeAgent"`
	DependsOn     []string         `json:"dependsOn,omitempty"`
	InputRef      string           `json:"inputRef,omitempty"`
	OutputRef     string           `json:"outputRef,omitempty"`
	Audit         []TaskTransition `json:"audit"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

func NewTask(id, projectID, planID, name, taskType, assigneeAgent string, dependsOn []string, inputRef string, now time.Time) *Task {
	return &Task{
		ID:            id,
		ProjectID:     projectID,
		PlanID:        planID,
		Name:          name,
		Type:          taskType,
		Status:        TaskStatusCreated,
		AssigneeAgent: assigneeAgent,
		DependsOn:     append([]string(nil), dependsOn...),
		InputRef:      inputRef,
		Audit: []TaskTransition{
			{
				From:   "",
				To:     TaskStatusCreated,
				Reason: "task created",
				At:     now,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func (t *Task) TransitionTo(next TaskStatus, reason string, now time.Time) error {
	if !isAllowedTransition(t.Status, next) {
		return fmt.Errorf("invalid task transition: %s -> %s", t.Status, next)
	}

	previous := t.Status
	t.Status = next
	t.UpdatedAt = now
	t.Audit = append(t.Audit, TaskTransition{
		From:   previous,
		To:     next,
		Reason: reason,
		At:     now,
	})

	return nil
}

func isAllowedTransition(current, next TaskStatus) bool {
	switch current {
	case TaskStatusCreated:
		return next == TaskStatusInProgress
	case TaskStatusInProgress:
		return next == TaskStatusDone || next == TaskStatusFailed || next == TaskStatusHumanOverride
	case TaskStatusHumanOverride:
		return next == TaskStatusInProgress || next == TaskStatusDone || next == TaskStatusFailed
	default:
		return false
	}
}

type RunStatus string

const (
	RunStatusPending   RunStatus = "PENDING"
	RunStatusRunning   RunStatus = "RUNNING"
	RunStatusSucceeded RunStatus = "SUCCEEDED"
	RunStatusFailed    RunStatus = "FAILED"
)

type AgentRun struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"projectId"`
	TaskID           string    `json:"taskId"`
	AgentType        string    `json:"agentType"`
	Model            string    `json:"model"`
	SandboxID        string    `json:"sandboxId,omitempty"`
	Status           RunStatus `json:"status"`
	PromptTokens     int       `json:"promptTokens,omitempty"`
	CompletionTokens int       `json:"completionTokens,omitempty"`
	TotalTokens      int       `json:"totalTokens,omitempty"`
	EstimatedCostUSD float64   `json:"estimatedCostUsd,omitempty"`
	ResultSummary    string    `json:"resultSummary,omitempty"`
	Error            string    `json:"error,omitempty"`
	ArtifactIDs      []string  `json:"artifactIds,omitempty"`
	StartedAt        time.Time `json:"startedAt"`
	EndedAt          time.Time `json:"endedAt,omitempty"`
}

type Artifact struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	TaskID    string    `json:"taskId"`
	RunID     string    `json:"runId"`
	Kind      string    `json:"kind"`
	URI       string    `json:"uri"`
	Checksum  string    `json:"checksum"`
	SizeBytes int64     `json:"sizeBytes"`
	CreatedAt time.Time `json:"createdAt"`
}
