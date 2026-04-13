package domain

import (
	"testing"
	"time"
)

func TestTaskTransitionHappyPath(t *testing.T) {
	now := time.Now().UTC()
	task := NewTask("task_1", "proj_1", "plan_1", "demo", "SPRINT1_EXECUTE", "agent", nil, "plan://plan_1", now)

	if err := task.TransitionTo(TaskStatusInProgress, "start", now.Add(time.Second)); err != nil {
		t.Fatalf("expected start transition to succeed: %v", err)
	}

	if err := task.TransitionTo(TaskStatusDone, "finish", now.Add(2*time.Second)); err != nil {
		t.Fatalf("expected finish transition to succeed: %v", err)
	}

	if task.Status != TaskStatusDone {
		t.Fatalf("expected status DONE, got %s", task.Status)
	}

	if len(task.Audit) != 3 {
		t.Fatalf("expected 3 audit records, got %d", len(task.Audit))
	}
}

func TestTaskTransitionRejectsInvalidFlow(t *testing.T) {
	now := time.Now().UTC()
	task := NewTask("task_1", "proj_1", "plan_1", "demo", "SPRINT1_EXECUTE", "agent", nil, "plan://plan_1", now)

	if err := task.TransitionTo(TaskStatusDone, "skip work", now.Add(time.Second)); err == nil {
		t.Fatal("expected invalid transition to fail")
	}
}
