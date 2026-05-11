package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"multiagentcom/internal/config"
	"multiagentcom/internal/domain"
)

type workspaceProvider interface {
	Name() string
	CreatePrivate(ctx context.Context, sandbox *domain.Sandbox, task *domain.Task) error
	CreateShared(ctx context.Context, sandbox *domain.Sandbox, tasks []*domain.Task, baseRef string) error
	BundleDir(sandbox *domain.Sandbox, task *domain.Task) string
	FinalizePrivateRun(ctx context.Context, sandbox *domain.Sandbox, task *domain.Task, run *domain.AgentRun) error
	MergeShared(ctx context.Context, sharedSandbox *domain.Sandbox, tasks []*domain.Task, artifacts []*domain.Artifact, sourceSandboxes []*domain.Sandbox) error
}

type directoryWorkspaceProvider struct{}

func (p directoryWorkspaceProvider) Name() string {
	return "directory"
}

func (p directoryWorkspaceProvider) CreatePrivate(_ context.Context, sandbox *domain.Sandbox, _ *domain.Task) error {
	return os.MkdirAll(sandbox.WorkspacePath, 0o755)
}

func (p directoryWorkspaceProvider) CreateShared(_ context.Context, sandbox *domain.Sandbox, _ []*domain.Task, _ string) error {
	return os.MkdirAll(sandbox.WorkspacePath, 0o755)
}

func (p directoryWorkspaceProvider) BundleDir(sandbox *domain.Sandbox, _ *domain.Task) string {
	return filepath.Join(sandbox.WorkspacePath, "bundle")
}

func (p directoryWorkspaceProvider) FinalizePrivateRun(_ context.Context, _ *domain.Sandbox, _ *domain.Task, _ *domain.AgentRun) error {
	return nil
}

func (p directoryWorkspaceProvider) MergeShared(_ context.Context, sharedSandbox *domain.Sandbox, _ []*domain.Task, artifacts []*domain.Artifact, _ []*domain.Sandbox) error {
	for _, artifact := range artifacts {
		artifactDir := filepath.Join(sharedSandbox.WorkspacePath, "artifacts", artifact.ID)
		if err := materializeArtifact(artifact.URI, artifactDir); err != nil {
			return err
		}
	}
	return nil
}

type gitWorkspaceProvider struct {
	repoPath string
	baseRef  string
}

func newWorkspaceProvider(cfg config.Config) workspaceProvider {
	if strings.EqualFold(strings.TrimSpace(cfg.WorkspaceProvider), "git") {
		baseRef := strings.TrimSpace(cfg.WorkspaceGitBaseRef)
		if baseRef == "" {
			baseRef = "HEAD"
		}
		return gitWorkspaceProvider{repoPath: strings.TrimSpace(cfg.WorkspaceGitRepoPath), baseRef: baseRef}
	}
	return directoryWorkspaceProvider{}
}

func (p gitWorkspaceProvider) Name() string {
	return "git"
}

func (p gitWorkspaceProvider) CreatePrivate(ctx context.Context, sandbox *domain.Sandbox, task *domain.Task) error {
	sandbox.WorkspaceBranch = gitBranchName("multiagent", sandbox.ProjectID, "task", task.ID, sandbox.ID)
	sandbox.WorkspaceBaseRef = p.baseRef
	return p.addWorktree(ctx, sandbox.WorkspacePath, sandbox.WorkspaceBranch, sandbox.WorkspaceBaseRef)
}

func (p gitWorkspaceProvider) CreateShared(ctx context.Context, sandbox *domain.Sandbox, _ []*domain.Task, baseRef string) error {
	sandbox.WorkspaceBranch = gitBranchName("multiagent", sandbox.ProjectID, "shared", sandbox.ID)
	if strings.TrimSpace(baseRef) == "" {
		baseRef = p.baseRef
	}
	sandbox.WorkspaceBaseRef = baseRef
	return p.addWorktree(ctx, sandbox.WorkspacePath, sandbox.WorkspaceBranch, sandbox.WorkspaceBaseRef)
}

func (p gitWorkspaceProvider) BundleDir(sandbox *domain.Sandbox, task *domain.Task) string {
	return filepath.Join(sandbox.WorkspacePath, "tasks", task.ID, "bundle")
}

func (p gitWorkspaceProvider) FinalizePrivateRun(ctx context.Context, sandbox *domain.Sandbox, task *domain.Task, run *domain.AgentRun) error {
	message := fmt.Sprintf("multiagent: task %s run %s", task.ID, run.ID)
	if err := commitGitWorkspaceChanges(ctx, sandbox.WorkspacePath, message); err != nil {
		return err
	}
	head, err := gitRevParse(ctx, sandbox.WorkspacePath, "HEAD")
	if err != nil {
		return err
	}
	sandbox.WorkspaceHeadRef = head
	return nil
}

func (p gitWorkspaceProvider) MergeShared(ctx context.Context, sharedSandbox *domain.Sandbox, tasks []*domain.Task, _ []*domain.Artifact, sourceSandboxes []*domain.Sandbox) error {
	for idx, sourceSandbox := range sourceSandboxes {
		if sourceSandbox == nil || strings.TrimSpace(sourceSandbox.WorkspaceHeadRef) == "" {
			return fmt.Errorf("source sandbox missing git head ref")
		}
		message := fmt.Sprintf("multiagent: merge task %s", tasks[idx].ID)
		if _, err := runGit(ctx, sharedSandbox.WorkspacePath, "merge", "--no-ff", sourceSandbox.WorkspaceHeadRef, "-m", message); err != nil {
			status, _ := runGit(ctx, sharedSandbox.WorkspacePath, "status", "--porcelain")
			_ = writeFile(filepath.Join(sharedSandbox.RootPath, "merge-conflicts.log"), []byte(strings.TrimSpace(status)+"\n"))
			_, _ = runGit(ctx, sharedSandbox.WorkspacePath, "merge", "--abort")
			return err
		}
	}
	return nil
}

func (p gitWorkspaceProvider) addWorktree(ctx context.Context, path, branch, baseRef string) error {
	if strings.TrimSpace(p.repoPath) == "" {
		return errors.New("git workspace repo path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := runGitInRepo(ctx, p.repoPath, "worktree", "add", "-b", branch, path, baseRef)
	return err
}

func commitGitWorkspaceChanges(ctx context.Context, worktreePath, message string) error {
	dirty, err := gitWorktreeDirty(ctx, worktreePath)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if _, err := runGit(ctx, worktreePath, "add", "-A"); err != nil {
		return err
	}
	_, err = runGit(ctx, worktreePath, "commit", "-m", message)
	return err
}

func gitWorktreeDirty(ctx context.Context, worktreePath string) (bool, error) {
	status, err := runGit(ctx, worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(status) != "", nil
}

func gitRevParse(ctx context.Context, worktreePath, ref string) (string, error) {
	out, err := runGit(ctx, worktreePath, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func runGit(ctx context.Context, worktreePath string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", worktreePath}, args...)
	return runGitCommand(ctx, fullArgs...)
}

func runGitInRepo(ctx context.Context, repoPath string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", repoPath}, args...)
	return runGitCommand(ctx, fullArgs...)
}

func runGitCommand(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", args...)
	output, err := cmd.CombinedOutput()
	if cmdCtx.Err() != nil {
		return string(output), cmdCtx.Err()
	}
	if err != nil {
		return string(output), fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func gitBranchName(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z':
				return r
			case r >= 'A' && r <= 'Z':
				return r
			case r >= '0' && r <= '9':
				return r
			case r == '-' || r == '_' || r == '.':
				return r
			default:
				return '-'
			}
		}, part)
		part = strings.Trim(part, "-./")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	if len(cleaned) == 0 {
		return "multiagent/workspace"
	}
	return strings.Join(cleaned, "/")
}
