package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	Publish(ctx context.Context, sandbox *domain.Sandbox) error
	Cleanup(ctx context.Context, sandbox *domain.Sandbox, policy workspaceCleanupPolicy) (*workspaceCleanupResult, error)
	Rebase(ctx context.Context, sandbox *domain.Sandbox, opts workspaceRebaseOptions) (*workspaceRebaseResult, error)
	RestoreSharedSnapshot(ctx context.Context, sandbox *domain.Sandbox, branch string, commit string) error
}

type workspaceCleanupPolicy struct {
	DryRun        bool
	DeleteBranch  bool
	RetainedHeads []string
	Reason        string
	Now           time.Time
}

type workspaceCleanupResult struct {
	SandboxID       string
	Provider        string
	Skipped         bool
	SkipReason      string
	WorktreeRemoved bool
	BranchDeleted   bool
	RetainedRef     string
	Error           string
}

type workspaceRebaseOptions struct {
	DryRun    bool
	TargetRef string
	Fetch     bool
	Publish   bool
	Now       time.Time
}

type workspaceRebaseResult struct {
	SandboxID      string
	Provider       string
	Status         string
	Reason         string
	Branch         string
	TargetRef      string
	OldHeadRef     string
	NewHeadRef     string
	Ahead          int
	Behind         int
	Fetched        bool
	RebaseAborted  bool
	Published      bool
	ConflictLog    string
	ConflictLogRef string
	Error          string
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

func (p directoryWorkspaceProvider) Publish(_ context.Context, _ *domain.Sandbox) error {
	return nil
}

func (p directoryWorkspaceProvider) Cleanup(_ context.Context, sandbox *domain.Sandbox, _ workspaceCleanupPolicy) (*workspaceCleanupResult, error) {
	return &workspaceCleanupResult{SandboxID: sandbox.ID, Provider: p.Name(), Skipped: true, SkipReason: "directory workspace cleanup is not supported"}, nil
}

func (p directoryWorkspaceProvider) Rebase(_ context.Context, sandbox *domain.Sandbox, opts workspaceRebaseOptions) (*workspaceRebaseResult, error) {
	return &workspaceRebaseResult{SandboxID: sandbox.ID, Provider: p.Name(), Status: "SKIPPED", Reason: "directory workspace rebase is not supported", TargetRef: strings.TrimSpace(opts.TargetRef)}, nil
}

func (p directoryWorkspaceProvider) RestoreSharedSnapshot(_ context.Context, _ *domain.Sandbox, _ string, _ string) error {
	return errors.New("directory workspace snapshot restore is not supported")
}

type gitWorkspaceProvider struct {
	repoPath       string
	baseRef        string
	remoteURL      string
	remoteName     string
	fetchBeforeUse bool
	pushEnabled    bool
	authToken      string
	authTokenFile  string
	authUsername   string
	mu             sync.Mutex
}

func newWorkspaceProvider(cfg config.Config) workspaceProvider {
	if strings.EqualFold(strings.TrimSpace(cfg.WorkspaceProvider), "git") {
		return newGitWorkspaceProvider(cfg)
	}
	return directoryWorkspaceProvider{}
}

func CheckGitWorkspace(ctx context.Context, cfg config.Config) error {
	provider := newGitWorkspaceProvider(config.WithDefaults(cfg))
	return provider.ensureRepoReady(ctx)
}

func newGitWorkspaceProvider(cfg config.Config) *gitWorkspaceProvider {
	baseRef := strings.TrimSpace(cfg.WorkspaceGitBaseRef)
	if baseRef == "" {
		baseRef = "HEAD"
	}
	remoteName := strings.TrimSpace(cfg.WorkspaceGitRemoteName)
	if remoteName == "" {
		remoteName = "origin"
	}
	authUsername := strings.TrimSpace(cfg.WorkspaceGitAuthUsername)
	if authUsername == "" {
		authUsername = "x-access-token"
	}
	return &gitWorkspaceProvider{
		repoPath:       strings.TrimSpace(cfg.WorkspaceGitRepoPath),
		baseRef:        baseRef,
		remoteURL:      strings.TrimSpace(cfg.WorkspaceGitRemoteURL),
		remoteName:     remoteName,
		fetchBeforeUse: cfg.WorkspaceGitFetchBeforeUse,
		pushEnabled:    cfg.WorkspaceGitPushEnabled,
		authToken:      strings.TrimSpace(cfg.WorkspaceGitAuthToken),
		authTokenFile:  strings.TrimSpace(cfg.WorkspaceGitAuthTokenFile),
		authUsername:   authUsername,
	}
}

func (p *gitWorkspaceProvider) Name() string {
	return "git"
}

func (p *gitWorkspaceProvider) CreatePrivate(ctx context.Context, sandbox *domain.Sandbox, task *domain.Task) error {
	if err := p.ensureRepoReady(ctx); err != nil {
		return err
	}
	sandbox.WorkspaceBranch = gitBranchName("multiagent", sandbox.ProjectID, "task", task.ID, sandbox.ID)
	sandbox.WorkspaceBaseRef = p.baseRef
	return p.addWorktree(ctx, sandbox.WorkspacePath, sandbox.WorkspaceBranch, sandbox.WorkspaceBaseRef)
}

func (p *gitWorkspaceProvider) CreateShared(ctx context.Context, sandbox *domain.Sandbox, _ []*domain.Task, baseRef string) error {
	if err := p.ensureRepoReady(ctx); err != nil {
		return err
	}
	sandbox.WorkspaceBranch = gitBranchName("multiagent", sandbox.ProjectID, "shared", sandbox.ID)
	if strings.TrimSpace(baseRef) == "" {
		baseRef = p.baseRef
	}
	sandbox.WorkspaceBaseRef = baseRef
	return p.addWorktree(ctx, sandbox.WorkspacePath, sandbox.WorkspaceBranch, sandbox.WorkspaceBaseRef)
}

func (p *gitWorkspaceProvider) BundleDir(sandbox *domain.Sandbox, task *domain.Task) string {
	return filepath.Join(sandbox.WorkspacePath, "tasks", task.ID, "bundle")
}

func (p *gitWorkspaceProvider) FinalizePrivateRun(ctx context.Context, sandbox *domain.Sandbox, task *domain.Task, run *domain.AgentRun) error {
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

func (p *gitWorkspaceProvider) MergeShared(ctx context.Context, sharedSandbox *domain.Sandbox, tasks []*domain.Task, _ []*domain.Artifact, sourceSandboxes []*domain.Sandbox) error {
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

func (p *gitWorkspaceProvider) Publish(ctx context.Context, sandbox *domain.Sandbox) error {
	if !p.pushEnabled || sandbox == nil || !strings.EqualFold(sandbox.WorkspaceProvider, "git") {
		return nil
	}
	if strings.TrimSpace(sandbox.WorkspacePath) == "" || strings.TrimSpace(sandbox.WorkspaceBranch) == "" || strings.TrimSpace(sandbox.WorkspaceHeadRef) == "" {
		return errors.New("git workspace metadata is incomplete for publish")
	}
	_, err := p.runGit(ctx, sandbox.WorkspacePath, "push", p.remoteName, sandbox.WorkspaceBranch+":refs/heads/"+sandbox.WorkspaceBranch)
	return err
}

func (p *gitWorkspaceProvider) RestoreSharedSnapshot(ctx context.Context, sandbox *domain.Sandbox, branch string, commit string) error {
	if err := p.ensureRepoReady(ctx); err != nil {
		return err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("git restore branch is required")
	}
	resolvedCommit, err := gitRevParse(ctx, p.repoPath, strings.TrimSpace(commit)+"^{commit}")
	if err != nil {
		return err
	}
	worktrees, err := gitWorktreeList(ctx, p.repoPath)
	if err != nil {
		return err
	}
	if _, registered := worktrees[normalizeGitWorktreePath(sandbox.WorkspacePath)]; registered {
		return errors.New("restore workspace path is already a registered git worktree")
	}
	if hasContent, err := pathHasContent(sandbox.WorkspacePath); err != nil {
		return err
	} else if hasContent {
		return errors.New("restore workspace path already contains files")
	}
	if _, exists, err := gitBranchHead(ctx, p.repoPath, branch); err != nil {
		return err
	} else if exists {
		return errors.New("restore branch already exists")
	}
	sandbox.WorkspaceBranch = branch
	sandbox.WorkspaceBaseRef = resolvedCommit
	if err := p.addWorktree(ctx, sandbox.WorkspacePath, sandbox.WorkspaceBranch, resolvedCommit); err != nil {
		return err
	}
	head, err := gitRevParse(ctx, sandbox.WorkspacePath, "HEAD")
	if err != nil {
		return err
	}
	if head != resolvedCommit {
		return fmt.Errorf("restored worktree head %s does not match snapshot commit %s", head, resolvedCommit)
	}
	sandbox.WorkspaceHeadRef = head
	return nil
}

func (p *gitWorkspaceProvider) Rebase(ctx context.Context, sandbox *domain.Sandbox, opts workspaceRebaseOptions) (*workspaceRebaseResult, error) {
	result := &workspaceRebaseResult{SandboxID: sandbox.ID, Provider: p.Name(), Branch: sandbox.WorkspaceBranch, TargetRef: strings.TrimSpace(opts.TargetRef), OldHeadRef: strings.TrimSpace(sandbox.WorkspaceHeadRef)}
	if err := p.ensureRepoReady(ctx); err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	if opts.Fetch {
		if strings.TrimSpace(p.remoteURL) == "" {
			result.Status = "SKIPPED"
			result.Reason = "git remote is not configured"
			return result, nil
		}
		if err := p.fetch(ctx); err != nil {
			result.Status = "FAILED"
			result.Error = err.Error()
			return result, nil
		}
		result.Fetched = true
	}
	if !strings.EqualFold(sandbox.WorkspaceProvider, "git") {
		result.Status = "SKIPPED"
		result.Reason = "sandbox is not a git workspace"
		return result, nil
	}
	if sandbox.Scope != "PRIVATE" {
		result.Status = "SKIPPED"
		result.Reason = "only private workspaces are rebased"
		return result, nil
	}
	if sandbox.Status != domain.SandboxStatusReleased {
		result.Status = "SKIPPED"
		result.Reason = "sandbox is not released"
		return result, nil
	}
	if strings.TrimSpace(sandbox.WorkspacePath) == "" || strings.TrimSpace(sandbox.WorkspaceBranch) == "" || strings.TrimSpace(sandbox.WorkspaceHeadRef) == "" {
		result.Status = "SKIPPED"
		result.Reason = "git workspace metadata is incomplete"
		return result, nil
	}
	if !strings.HasPrefix(sandbox.WorkspaceBranch, "multiagent/") || !strings.Contains(sandbox.WorkspaceBranch, "/task/") {
		result.Status = "SKIPPED"
		result.Reason = "workspace branch is not a managed private task branch"
		return result, nil
	}
	worktrees, err := gitWorktreeList(ctx, p.repoPath)
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	registeredBranch, registered := worktrees[normalizeGitWorktreePath(sandbox.WorkspacePath)]
	if !registered {
		result.Status = "SKIPPED"
		result.Reason = "workspace path is not a registered git worktree"
		return result, nil
	}
	if registeredBranch != "" && registeredBranch != sandbox.WorkspaceBranch {
		result.Status = "SKIPPED"
		result.Reason = "registered worktree branch does not match sandbox metadata"
		return result, nil
	}
	head, err := gitRevParse(ctx, sandbox.WorkspacePath, "HEAD")
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	if head != strings.TrimSpace(sandbox.WorkspaceHeadRef) {
		result.Status = "SKIPPED"
		result.Reason = "workspace head moved after sandbox metadata was recorded"
		return result, nil
	}
	if dirty, err := gitWorktreeDirty(ctx, sandbox.WorkspacePath); err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	} else if dirty {
		metadataOnly, err := gitWorktreeDirtyOnlyWorkspaceManifest(ctx, sandbox.WorkspacePath)
		if err != nil {
			result.Status = "FAILED"
			result.Error = err.Error()
			return result, nil
		}
		if !metadataOnly {
			result.Status = "SKIPPED"
			result.Reason = "workspace has uncommitted changes"
			return result, nil
		}
		if !opts.DryRun {
			if err := os.RemoveAll(filepath.Join(sandbox.WorkspacePath, ".multiagent")); err != nil && !os.IsNotExist(err) {
				result.Status = "FAILED"
				result.Error = err.Error()
				return result, nil
			}
		}
	}
	targetCommit, err := gitRevParse(ctx, sandbox.WorkspacePath, result.TargetRef+"^{commit}")
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	result.TargetRef = targetCommit
	behind, ahead, err := gitRevListLeftRightCount(ctx, sandbox.WorkspacePath, targetCommit, "HEAD")
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	result.Behind = behind
	result.Ahead = ahead
	upToDate, err := gitIsAncestor(ctx, sandbox.WorkspacePath, targetCommit, "HEAD")
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	if upToDate {
		result.Status = "UP_TO_DATE"
		result.NewHeadRef = head
		return result, nil
	}
	if opts.DryRun {
		result.Status = "DRY_RUN"
		result.NewHeadRef = head
		return result, nil
	}
	if _, err := p.runGit(ctx, sandbox.WorkspacePath, "rebase", targetCommit); err != nil {
		conflictLog, conflictLogRef := writeRebaseConflictLog(sandbox, err.Error())
		_, _ = p.runGit(ctx, sandbox.WorkspacePath, "rebase", "--abort")
		afterAbortHead, _ := gitRevParse(ctx, sandbox.WorkspacePath, "HEAD")
		result.Status = "FAILED"
		result.Error = err.Error()
		result.RebaseAborted = true
		result.ConflictLog = conflictLog
		result.ConflictLogRef = conflictLogRef
		result.NewHeadRef = afterAbortHead
		return result, nil
	}
	newHead, err := gitRevParse(ctx, sandbox.WorkspacePath, "HEAD")
	if err != nil {
		result.Status = "FAILED"
		result.Error = err.Error()
		return result, nil
	}
	result.Status = "REBASED"
	result.NewHeadRef = newHead
	if opts.Publish {
		publishSandbox := *sandbox
		publishSandbox.WorkspaceBaseRef = targetCommit
		publishSandbox.WorkspaceHeadRef = newHead
		if err := p.Publish(ctx, &publishSandbox); err != nil {
			result.Status = "REBASED_PUBLISH_FAILED"
			result.Error = err.Error()
			return result, nil
		}
		result.Published = true
	}
	return result, nil
}

func (p *gitWorkspaceProvider) Cleanup(ctx context.Context, sandbox *domain.Sandbox, policy workspaceCleanupPolicy) (*workspaceCleanupResult, error) {
	result := &workspaceCleanupResult{SandboxID: sandbox.ID, Provider: p.Name()}
	if !strings.EqualFold(sandbox.WorkspaceProvider, "git") {
		result.Skipped = true
		result.SkipReason = "sandbox is not a git workspace"
		return result, nil
	}
	if sandbox.Scope != "PRIVATE" {
		result.Skipped = true
		result.SkipReason = "only private workspaces are cleaned"
		return result, nil
	}
	if sandbox.Status != domain.SandboxStatusReleased {
		result.Skipped = true
		result.SkipReason = "sandbox is not released"
		return result, nil
	}
	if strings.TrimSpace(sandbox.WorkspacePath) == "" || strings.TrimSpace(sandbox.WorkspaceBranch) == "" || strings.TrimSpace(sandbox.WorkspaceHeadRef) == "" {
		result.Skipped = true
		result.SkipReason = "git workspace metadata is incomplete"
		return result, nil
	}
	if !strings.HasPrefix(sandbox.WorkspaceBranch, "multiagent/") {
		result.Skipped = true
		result.SkipReason = "workspace branch is not managed by multiagent"
		return result, nil
	}

	worktrees, err := gitWorktreeList(ctx, p.repoPath)
	if err != nil {
		return result, err
	}
	registeredBranch, registered := worktrees[normalizeGitWorktreePath(sandbox.WorkspacePath)]
	if !registered {
		if _, statErr := os.Stat(sandbox.WorkspacePath); os.IsNotExist(statErr) {
			result.WorktreeRemoved = true
		} else {
			result.Skipped = true
			result.SkipReason = "workspace path is not a registered git worktree"
			return result, nil
		}
	} else {
		if registeredBranch != "" && registeredBranch != sandbox.WorkspaceBranch {
			result.Skipped = true
			result.SkipReason = "registered worktree branch does not match sandbox metadata"
			return result, nil
		}
		if dirty, err := gitWorktreeDirty(ctx, sandbox.WorkspacePath); err != nil {
			return result, err
		} else if dirty {
			metadataOnly, err := gitWorktreeDirtyOnlyWorkspaceManifest(ctx, sandbox.WorkspacePath)
			if err != nil {
				return result, err
			}
			if !metadataOnly {
				result.Skipped = true
				result.SkipReason = "workspace has uncommitted changes"
				return result, nil
			}
			if !policy.DryRun {
				if err := os.RemoveAll(filepath.Join(sandbox.WorkspacePath, ".multiagent")); err != nil && !os.IsNotExist(err) {
					return result, err
				}
			}
		}
		if policy.DryRun {
			result.WorktreeRemoved = true
		} else if _, err := runGitInRepo(ctx, p.repoPath, "worktree", "remove", sandbox.WorkspacePath); err != nil {
			return result, err
		} else {
			result.WorktreeRemoved = true
		}
	}

	if !policy.DeleteBranch {
		return result, nil
	}
	if !strings.Contains(sandbox.WorkspaceBranch, "/task/") {
		result.Skipped = true
		result.SkipReason = "only private task branches are deleted"
		return result, nil
	}
	branchHead, exists, err := gitBranchHead(ctx, p.repoPath, sandbox.WorkspaceBranch)
	if err != nil {
		return result, err
	}
	if !exists {
		result.BranchDeleted = true
		return result, nil
	}
	if branchHead != strings.TrimSpace(sandbox.WorkspaceHeadRef) {
		result.Skipped = true
		result.SkipReason = "branch head moved after sandbox metadata was recorded"
		return result, nil
	}
	retainedRef, ok, err := gitHeadRetainedBy(ctx, p.repoPath, sandbox.WorkspaceHeadRef, policy.RetainedHeads)
	if err != nil {
		return result, err
	}
	if !ok {
		result.Skipped = true
		result.SkipReason = "branch head is not merged into a retained shared head"
		return result, nil
	}
	result.RetainedRef = retainedRef
	if policy.DryRun {
		result.BranchDeleted = true
		return result, nil
	}
	if _, err := runGitInRepo(ctx, p.repoPath, "branch", "-d", sandbox.WorkspaceBranch); err != nil {
		result.Skipped = true
		result.SkipReason = "git branch -d refused to delete branch"
		return result, nil
	}
	result.BranchDeleted = true
	return result, nil
}

func (p *gitWorkspaceProvider) addWorktree(ctx context.Context, path, branch, baseRef string) error {
	if strings.TrimSpace(p.repoPath) == "" {
		return errors.New("git workspace repo path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	_, err := p.runGitInRepo(ctx, "worktree", "add", "-b", branch, path, baseRef)
	return err
}

func (p *gitWorkspaceProvider) ensureRepoReady(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ensureRepoReadyLocked(ctx)
}

func (p *gitWorkspaceProvider) ensureRepoReadyLocked(ctx context.Context) error {
	if strings.TrimSpace(p.repoPath) == "" {
		return errors.New("git workspace repo path is required")
	}
	if p.isGitRepo(ctx) {
		if err := p.ensureRemote(ctx); err != nil {
			return err
		}
	} else {
		if strings.TrimSpace(p.remoteURL) == "" {
			return errors.New("git workspace repo path is not a git repository")
		}
		if err := p.cloneRepo(ctx); err != nil {
			return err
		}
	}
	if err := p.ensureCommitIdentity(ctx); err != nil {
		return err
	}
	if p.fetchBeforeUse {
		if err := p.fetch(ctx); err != nil {
			return err
		}
	}
	_, err := p.runGitInRepo(ctx, "rev-parse", "--verify", p.baseRef)
	return err
}

func (p *gitWorkspaceProvider) isGitRepo(ctx context.Context) bool {
	if strings.TrimSpace(p.repoPath) == "" {
		return false
	}
	info, err := os.Stat(p.repoPath)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = p.runGitInRepo(ctx, "rev-parse", "--git-dir")
	return err == nil
}

func (p *gitWorkspaceProvider) cloneRepo(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(p.repoPath), 0o755); err != nil {
		return err
	}
	_, err := p.runGitCommand(ctx, "clone", "--origin", p.remoteName, "--", p.remoteURL, p.repoPath)
	return err
}

func (p *gitWorkspaceProvider) ensureRemote(ctx context.Context) error {
	if strings.TrimSpace(p.remoteURL) == "" {
		return nil
	}
	out, err := p.runGitInRepo(ctx, "remote", "get-url", p.remoteName)
	if err != nil {
		if strings.Contains(err.Error(), "No such remote") || strings.Contains(err.Error(), "No such remote") || strings.Contains(err.Error(), "exit status 2") {
			_, addErr := p.runGitInRepo(ctx, "remote", "add", p.remoteName, p.remoteURL)
			return addErr
		}
		return err
	}
	if strings.TrimSpace(out) != p.remoteURL {
		return fmt.Errorf("git remote %s points to a different URL", p.remoteName)
	}
	return nil
}

func (p *gitWorkspaceProvider) fetch(ctx context.Context) error {
	_, err := p.runGitInRepo(ctx, "fetch", p.remoteName, "--prune")
	return err
}

func (p *gitWorkspaceProvider) ensureCommitIdentity(ctx context.Context) error {
	if _, err := p.runGitInRepo(ctx, "config", "user.name"); err != nil {
		if _, setErr := p.runGitInRepo(ctx, "config", "user.name", "MultiAgentCom"); setErr != nil {
			return setErr
		}
	}
	if _, err := p.runGitInRepo(ctx, "config", "user.email"); err != nil {
		if _, setErr := p.runGitInRepo(ctx, "config", "user.email", "multiagentcom@example.invalid"); setErr != nil {
			return setErr
		}
	}
	return nil
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

func gitWorktreeDirtyOnlyWorkspaceManifest(ctx context.Context, worktreePath string) (bool, error) {
	status, err := runGit(ctx, worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	lines := strings.FieldsFunc(strings.TrimSpace(status), func(r rune) bool { return r == '\n' || r == '\r' })
	if len(lines) == 0 {
		return false, nil
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, ".multiagent/workspace-manifest.json") && !strings.HasSuffix(line, ".multiagent/") {
			return false, nil
		}
	}
	return true, nil
}

func gitRevParse(ctx context.Context, worktreePath, ref string) (string, error) {
	out, err := runGit(ctx, worktreePath, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func gitWorktreeList(ctx context.Context, repoPath string) (map[string]string, error) {
	out, err := runGitInRepo(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	worktrees := map[string]string{}
	var currentPath string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentPath = normalizeGitWorktreePath(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))
			worktrees[currentPath] = ""
		case strings.HasPrefix(line, "branch ") && currentPath != "":
			branch := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			branch = strings.TrimPrefix(branch, "refs/heads/")
			worktrees[currentPath] = branch
		case line == "":
			currentPath = ""
		}
	}
	return worktrees, nil
}

func normalizeGitWorktreePath(path string) string {
	path = filepath.Clean(path)
	if evaluated, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(evaluated)
	}
	return path
}

func pathHasContent(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func gitBranchHead(ctx context.Context, repoPath, branch string) (string, bool, error) {
	out, err := runGitInRepo(ctx, repoPath, "show-ref", "--verify", "refs/heads/"+branch)
	if err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			return "", false, nil
		}
		return "", false, err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", false, nil
	}
	return fields[0], true, nil
}

func gitHeadRetainedBy(ctx context.Context, repoPath, head string, retainedHeads []string) (string, bool, error) {
	for _, retained := range retainedHeads {
		retained = strings.TrimSpace(retained)
		if retained == "" {
			continue
		}
		if _, err := runGitInRepo(ctx, repoPath, "merge-base", "--is-ancestor", head, retained); err == nil {
			return retained, true, nil
		} else if !strings.Contains(err.Error(), "exit status 1") {
			return "", false, err
		}
	}
	return "", false, nil
}

func gitRevListLeftRightCount(ctx context.Context, worktreePath, leftRef, rightRef string) (int, int, error) {
	out, err := runGit(ctx, worktreePath, "rev-list", "--left-right", "--count", leftRef+"..."+rightRef)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list count output: %s", strings.TrimSpace(out))
	}
	behind, err := parseGitCount(fields[0])
	if err != nil {
		return 0, 0, err
	}
	ahead, err := parseGitCount(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return behind, ahead, nil
}

func parseGitCount(value string) (int, error) {
	var count int
	if _, err := fmt.Sscanf(value, "%d", &count); err != nil {
		return 0, err
	}
	return count, nil
}

func gitIsAncestor(ctx context.Context, worktreePath, ancestor, descendant string) (bool, error) {
	_, err := runGit(ctx, worktreePath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "exit status 1") {
		return false, nil
	}
	return false, err
}

func writeRebaseConflictLog(sandbox *domain.Sandbox, rebaseError string) (string, string) {
	logPath := filepath.Join(sandbox.RootPath, "rebase-conflicts.log")
	parts := []string{"rebase error:", strings.TrimSpace(rebaseError), ""}
	if status, err := runGit(context.Background(), sandbox.WorkspacePath, "status", "--porcelain"); err == nil {
		parts = append(parts, "status --porcelain:", strings.TrimSpace(status), "")
	}
	if status, err := runGit(context.Background(), sandbox.WorkspacePath, "status", "--short", "--untracked-files=all"); err == nil {
		parts = append(parts, "status --short --untracked-files=all:", strings.TrimSpace(status), "")
	}
	if unmerged, err := runGit(context.Background(), sandbox.WorkspacePath, "diff", "--name-only", "--diff-filter=U"); err == nil {
		parts = append(parts, "unmerged paths:", strings.TrimSpace(unmerged), "")
	}
	_ = writeFile(logPath, []byte(strings.Join(parts, "\n")+"\n"))
	return logPath, "file://" + logPath
}

func (p *gitWorkspaceProvider) runGit(ctx context.Context, worktreePath string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", worktreePath}, args...)
	return p.runGitCommand(ctx, fullArgs...)
}

func (p *gitWorkspaceProvider) runGitInRepo(ctx context.Context, args ...string) (string, error) {
	fullArgs := append([]string{"-C", p.repoPath}, args...)
	return p.runGitCommand(ctx, fullArgs...)
}

func (p *gitWorkspaceProvider) runGitCommand(ctx context.Context, args ...string) (string, error) {
	env, secrets, err := p.remoteAuthEnv()
	if err != nil {
		return "", err
	}
	if len(secrets) > 0 {
		args = append([]string{"-c", gitCredentialHelperArg()}, args...)
	}
	return runGitCommandWithOptions(ctx, gitCommandOptions{env: env, redactValues: secrets}, args...)
}

func (p *gitWorkspaceProvider) remoteAuthEnv() ([]string, []string, error) {
	token := strings.TrimSpace(p.authToken)
	if token == "" && strings.TrimSpace(p.authTokenFile) != "" {
		payload, err := os.ReadFile(p.authTokenFile)
		if err != nil {
			return nil, nil, err
		}
		token = strings.TrimSpace(string(payload))
	}
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if token == "" {
		return env, nil, nil
	}
	env = append(env,
		"MULTI_AGENT_WORKSPACE_GIT_AUTH_USERNAME="+p.authUsername,
		"MULTI_AGENT_WORKSPACE_GIT_AUTH_TOKEN="+token,
	)
	return env, []string{token}, nil
}

func gitCredentialHelperArg() string {
	return "credential.helper=!f() { echo username=$MULTI_AGENT_WORKSPACE_GIT_AUTH_USERNAME; echo password=$MULTI_AGENT_WORKSPACE_GIT_AUTH_TOKEN; }; f"
}

func runGit(ctx context.Context, worktreePath string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", worktreePath}, args...)
	return runGitCommand(ctx, fullArgs...)
}

func runGitInRepo(ctx context.Context, repoPath string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", repoPath}, args...)
	return runGitCommand(ctx, fullArgs...)
}

type gitCommandOptions struct {
	env          []string
	redactValues []string
}

func runGitCommand(ctx context.Context, args ...string) (string, error) {
	return runGitCommandWithOptions(ctx, gitCommandOptions{}, args...)
}

func runGitCommandWithOptions(ctx context.Context, opts gitCommandOptions, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", args...)
	if len(opts.env) > 0 {
		cmd.Env = append(os.Environ(), opts.env...)
	}
	outputBytes, err := cmd.CombinedOutput()
	output := sanitizeGitError(string(outputBytes), opts.redactValues)
	joinedArgs := sanitizeGitError(strings.Join(args, " "), opts.redactValues)
	if cmdCtx.Err() != nil {
		return output, cmdCtx.Err()
	}
	if err != nil {
		return output, fmt.Errorf("git %s failed: %w: %s", joinedArgs, err, strings.TrimSpace(output))
	}
	return string(outputBytes), nil
}

func sanitizeGitError(text string, secrets []string) string {
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
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
