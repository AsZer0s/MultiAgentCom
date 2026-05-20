package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeContainerExecutor struct {
	binary string
	args   []string
	stdin  []byte
	env    []string
	stdout []byte
	stderr []byte
	err    error
}

func (f *fakeContainerExecutor) Run(_ context.Context, binary string, args []string, stdin []byte, env []string) ([]byte, []byte, error) {
	f.binary = binary
	f.args = append([]string(nil), args...)
	f.stdin = append([]byte(nil), stdin...)
	f.env = append([]string(nil), env...)
	return f.stdout, f.stderr, f.err
}

func TestContainerRunnerRunBuildsIsolatedCommand(t *testing.T) {
	executor := &fakeContainerExecutor{stdout: []byte("done\n")}
	runner, err := NewContainerRunnerWithOptions(ContainerRunnerOptions{
		Binary:         "podman",
		Image:          "multiagent-runtime:test",
		Network:        "none",
		User:           "1000:1000",
		ReadonlyRootFS: true,
		Workdir:        "/workspace",
		CPUs:           "1.5",
		Memory:         "256m",
		PidsLimit:      128,
		Tmpfs:          []string{"/tmp:rw,nosuid,nodev,noexec,size=64m", "/run:rw,size=16m"},
		Entrypoint:     "/usr/local/bin/runtime",
		Command:        "--mode worker --once",
		Executor:       executor,
	})
	if err != nil {
		t.Fatalf("new container runner: %v", err)
	}

	resp, err := runner.Run(context.Background(), Request{
		ProjectID:         "proj_1",
		TaskID:            "task_1",
		RunID:             "run_1",
		AgentType:         "go-backend-agent",
		Prompt:            "build api",
		Context:           "contract=v1",
		Timeout:           time.Second,
		SandboxID:         "sandbox_1",
		SandboxRootPath:   "/tmp/sandbox",
		WorkspacePath:     "/tmp/sandbox/workspace",
		WorkspaceProvider: "directory",
		WorkspaceBranch:   "branch",
		WorkspaceBaseRef:  "main",
		WorkspaceHeadRef:  "abc123",
	})
	if err != nil {
		t.Fatalf("run container runner: %v", err)
	}
	if resp.Model != "container-runtime" || resp.Output != "done" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if executor.binary != "podman" {
		t.Fatalf("binary = %q", executor.binary)
	}
	expectedArgs := []string{"run", "--rm", "--network", "none", "-v", "/tmp/sandbox/workspace:/workspace", "-w", "/workspace", "--read-only", "--user", "1000:1000", "--cpus", "1.5", "--memory", "256m", "--pids-limit", "128", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m", "--tmpfs", "/run:rw,size=16m", "--entrypoint", "/usr/local/bin/runtime", "multiagent-runtime:test", "--mode", "worker", "--once"}
	if !reflect.DeepEqual(executor.args, expectedArgs) {
		t.Fatalf("args = %#v, want %#v", executor.args, expectedArgs)
	}
	for _, expected := range []string{"MULTI_AGENT_PROJECT_ID=proj_1", "MULTI_AGENT_TASK_ID=task_1", "MULTI_AGENT_RUN_ID=run_1", "MULTI_AGENT_AGENT_TYPE=go-backend-agent"} {
		if !containsString(executor.env, expected) {
			t.Fatalf("expected env %q in %v", expected, executor.env)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(executor.stdin, &payload); err != nil {
		t.Fatalf("decode stdin payload: %v", err)
	}
	if payload["workspacePath"] != "/tmp/sandbox/workspace" || payload["workspaceProvider"] != "directory" || payload["sandboxId"] != "sandbox_1" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestContainerRunnerRunRequiresWorkspacePath(t *testing.T) {
	runner, err := NewContainerRunnerWithOptions(ContainerRunnerOptions{Image: "multiagent-runtime:test", Executor: &fakeContainerExecutor{}})
	if err != nil {
		t.Fatalf("new container runner: %v", err)
	}
	_, err = runner.Run(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != ProviderErrorContainerFailed {
		t.Fatalf("expected container provider error, got %T: %v", err, err)
	}
}

func TestContainerRunnerRunMapsTimeout(t *testing.T) {
	runner, err := NewContainerRunnerWithOptions(ContainerRunnerOptions{Image: "multiagent-runtime:test", Executor: &fakeContainerExecutor{err: context.DeadlineExceeded}})
	if err != nil {
		t.Fatalf("new container runner: %v", err)
	}
	_, err = runner.Run(context.Background(), Request{WorkspacePath: t.TempDir(), Timeout: time.Second})
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != ProviderErrorTimeout || !providerErr.Retryable {
		t.Fatalf("expected timeout provider error, got %T: %v", err, err)
	}
}

func TestContainerRunnerRunReturnsStderrOnFailure(t *testing.T) {
	runner, err := NewContainerRunnerWithOptions(ContainerRunnerOptions{Image: "multiagent-runtime:test", Executor: &fakeContainerExecutor{stderr: []byte("container failed"), err: errors.New("exit status 1")}})
	if err != nil {
		t.Fatalf("new container runner: %v", err)
	}
	_, err = runner.Run(context.Background(), Request{WorkspacePath: t.TempDir()})
	if err == nil {
		t.Fatal("expected error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != ProviderErrorContainerFailed || !strings.Contains(providerErr.Message, "container failed") {
		t.Fatalf("expected stderr provider error, got %T: %v", err, err)
	}
}

func TestCleanContainerListDropsEmptyValues(t *testing.T) {
	values := cleanContainerList([]string{" /tmp:rw ", "", "  ", "/run:rw"})
	if !reflect.DeepEqual(values, []string{"/tmp:rw", "/run:rw"}) {
		t.Fatalf("unexpected cleaned list: %#v", values)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
