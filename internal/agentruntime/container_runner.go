package agentruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type ContainerRunner struct {
	binary         string
	image          string
	network        string
	user           string
	readonlyRootFS bool
	workdir        string
	exec           containerCommandExecutor
}

type ContainerRunnerOptions struct {
	Binary         string
	Image          string
	Network        string
	User           string
	ReadonlyRootFS bool
	Workdir        string
	Executor       containerCommandExecutor
}

type containerCommandExecutor interface {
	Run(ctx context.Context, binary string, args []string, stdin []byte, env []string) ([]byte, []byte, error)
}

type osContainerCommandExecutor struct{}

func NewContainerRunnerWithOptions(options ContainerRunnerOptions) (*ContainerRunner, error) {
	binary := strings.TrimSpace(options.Binary)
	if binary == "" {
		binary = "docker"
	}
	image := strings.TrimSpace(options.Image)
	if image == "" {
		return nil, fmt.Errorf("%w: container image", ErrProviderRequired)
	}
	network := strings.TrimSpace(options.Network)
	if network == "" {
		network = "none"
	}
	workdir := strings.TrimSpace(options.Workdir)
	if workdir == "" {
		workdir = "/workspace"
	}
	executor := options.Executor
	if executor == nil {
		executor = osContainerCommandExecutor{}
	}

	return &ContainerRunner{
		binary:         binary,
		image:          image,
		network:        network,
		user:           strings.TrimSpace(options.User),
		readonlyRootFS: options.ReadonlyRootFS,
		workdir:        workdir,
		exec:           executor,
	}, nil
}

func (r *ContainerRunner) Run(ctx context.Context, req Request) (Response, error) {
	workspacePath := strings.TrimSpace(req.WorkspacePath)
	if workspacePath == "" {
		return Response{}, &ProviderError{Code: ProviderErrorContainerFailed, Message: "container runtime requires workspace path"}
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	payload, err := json.Marshal(containerRuntimePayload{
		ProtocolVersion:   ProtocolVersion,
		ProjectID:         req.ProjectID,
		TaskID:            req.TaskID,
		RunID:             req.RunID,
		AgentType:         req.AgentType,
		Prompt:            req.Prompt,
		Context:           req.Context,
		TimeoutMs:         req.Timeout.Milliseconds(),
		SandboxID:         req.SandboxID,
		SandboxRootPath:   req.SandboxRootPath,
		WorkspacePath:     req.WorkspacePath,
		WorkspaceProvider: req.WorkspaceProvider,
		WorkspaceBranch:   req.WorkspaceBranch,
		WorkspaceBaseRef:  req.WorkspaceBaseRef,
		WorkspaceHeadRef:  req.WorkspaceHeadRef,
	})
	if err != nil {
		return Response{}, &ProviderError{Code: ProviderErrorContainerFailed, Message: "marshal container runtime payload", Cause: err}
	}

	args := r.args(workspacePath)
	env := []string{
		"MULTI_AGENT_PROJECT_ID=" + req.ProjectID,
		"MULTI_AGENT_TASK_ID=" + req.TaskID,
		"MULTI_AGENT_RUN_ID=" + req.RunID,
		"MULTI_AGENT_AGENT_TYPE=" + req.AgentType,
	}
	stdout, stderr, err := r.exec.Run(ctx, r.binary, args, payload, env)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return Response{}, &ProviderError{Code: ProviderErrorTimeout, Message: "container runtime timed out", Retryable: true, Cause: err}
		}
		message := strings.TrimSpace(string(stderr))
		if message == "" {
			message = strings.TrimSpace(err.Error())
		}
		return Response{}, &ProviderError{Code: ProviderErrorContainerFailed, Message: message, Cause: err}
	}

	return Response{Model: "container-runtime", Output: strings.TrimSpace(string(stdout))}, nil
}

func (r *ContainerRunner) args(workspacePath string) []string {
	mount := workspacePath + ":" + r.workdir
	args := []string{"run", "--rm", "--network", r.network, "-v", mount, "-w", r.workdir}
	if r.readonlyRootFS {
		args = append(args, "--read-only")
	}
	if r.user != "" {
		args = append(args, "--user", r.user)
	}
	args = append(args, r.image)
	return args
}

type containerRuntimePayload struct {
	ProtocolVersion   string `json:"protocolVersion"`
	ProjectID         string `json:"projectId"`
	TaskID            string `json:"taskId"`
	RunID             string `json:"runId"`
	AgentType         string `json:"agentType"`
	Prompt            string `json:"prompt"`
	Context           string `json:"context"`
	TimeoutMs         int64  `json:"timeoutMs,omitempty"`
	SandboxID         string `json:"sandboxId"`
	SandboxRootPath   string `json:"sandboxRootPath"`
	WorkspacePath     string `json:"workspacePath"`
	WorkspaceProvider string `json:"workspaceProvider"`
	WorkspaceBranch   string `json:"workspaceBranch,omitempty"`
	WorkspaceBaseRef  string `json:"workspaceBaseRef,omitempty"`
	WorkspaceHeadRef  string `json:"workspaceHeadRef,omitempty"`
}

func (osContainerCommandExecutor) Run(ctx context.Context, binary string, args []string, stdin []byte, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(cmd.Environ(), env...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}
