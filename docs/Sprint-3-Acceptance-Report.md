# Sprint 3 Acceptance Report

本报告对应 `docs/Implementation-Backlog.md` 中的 `BL-206 Sprint 3 验收与回归`，覆盖 AC-07 / AC-08 / AC-09 / AC-10 / AC-11。

## Scope

- 验收版本：`main` 分支，Checkpoint `3740b72` 之后的工作区状态
- 验收环境：Go 标准库 HTTP 服务，内存存储，`go test ./...`
- 示例场景：Todo 全栈（私有沙盒、共享沙盒、时间线回滚、HITL 接管、代码锁定）

## Coverage

| Acceptance Case | Goal | Coverage |
| --- | --- | --- |
| AC-07 | 私有沙盒隔离 | `TestPrivateSandboxIsolation`、`TestHTTPSandboxIsolationFlow` |
| AC-08 | 共享沙盒失败回滚 | `TestMergeSharedSandboxBlocksOnIntegrationFailure`、`TestSharedSandboxFailureAutoRollbackToLatestStableSnapshot`、`TestHTTPSharedSandboxMergeIntegrationFailure`、`TestHTTPSharedSandboxFailureAutoRollback` |
| AC-09 | HITL 强制接管 | `TestApplyHumanOverrideAtSafetyCheckpoint`、`TestHTTPApplyHumanOverride` |
| AC-10 | 人类锁定代码不可覆盖 | `TestCodeLockPreservesHumanContent`、`TestCodeLockPreservesLateGeneratedHumanContent`、`TestGoSymbolCodeLockReplacesOnlyFunction`、`TestHTTPApplyCodeLock` |
| AC-11 | 语义快照与平行分支 | `TestRollbackToSnapshotCreatesParallelBranchTimeline`、`TestRollbackToSnapshotResolvesFileStateRef`、`TestRollbackToSnapshotRejectsFileChecksumMismatch`、`TestHTTPRollbackSnapshotCreatesParallelBranchTimeline` |

## Results

### AC-07 Private Sandbox Isolation

- Result: `PASS`
- Notes:
  - 每个 run 都会分配独立 `sandboxId` 和独立 `rootPath`。
  - 单个私有沙盒失败时，仅对应 run / task 标记失败，其它并行任务仍可继续完成。

### AC-08 Shared Sandbox Failure Rollback

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/shared-sandbox/merge` 在模拟集成失败时返回失败结果，并触发自动回滚到最近稳定快照。
  - 回滚结果包含新的 branch、来源快照和清理掉的上下文计数，符合“失败后可追溯恢复”的 MVP 要求。

### AC-09 HITL Override

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/overrides` 可让运行中任务进入 `HUMAN_OVERRIDE`。
  - 执行器会在安全检查点应用 override，并恢复到正常执行；任务审计与 run summary 都会记录这次接管。

### AC-10 Locked Human Code

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/locks` 要求提交内容中包含 `LOCKED BY HUMAN` 标记。
  - 后续自动 bundle 生成末尾会保留人工内容，避免 `web-app/index.html` / `docker-compose.yml` 等晚生成文件覆盖锁。
  - `go_symbol` 模式初始支持锁定 Go 顶层 `func`；后续已扩展到 Go `func`、`method`、`type`、`var`、`const` 声明级替换，并保留同文件中的 generated imports/其它声明。

### AC-11 Snapshot Timeline Branching

- Result: `PASS`
- Notes:
  - `GET /projects/{id}/snapshots` 可查看稳定快照和回滚后产生的新时间线分支。
  - `POST /projects/{id}/snapshots/rollback` 不会覆写旧历史，而是创建新的 rollback branch，并保留原 `main` 分支快照。
  - 文件存储模式下 rollback 可从 `file://` StateRef 读取落盘 `state.json`，并在 checksum 不匹配时拒绝恢复。

## Regression Summary

- 执行命令：`go test ./...`
- 结果：通过
- 涉及模块：
  - `internal/domain`
  - `internal/service`
  - `internal/httpapi`

## Demo Entry

- 私有沙盒：`POST /projects/{id}/tasks/{taskId}/sandbox/fail`
- 人工接管：`POST /projects/{id}/overrides`
- 代码锁定：`POST /projects/{id}/locks`
- 共享沙盒合并：`POST /projects/{id}/shared-sandbox/merge`
- 快照查询：`GET /projects/{id}/snapshots`
- 手动回滚：`POST /projects/{id}/snapshots/rollback`

## Residual Risk

- 当前 HITL 仍是 MVP 级服务状态模型，尚未接入真实代码仓权限流或分布式锁。
- 代码锁已支持 Go `func`、`method`、`type`、`var`、`const` 声明级替换；尚未覆盖 import reconciliation 或跨语言 AST 锁。
- 时间线快照已支持 `file://` StateRef 恢复与 checksum 校验；Sprint 4 后已补齐 local-only Git workspace v1（本地已有 repo 的 worktree、任务分支 commit、shared sandbox merge、snapshot workspace ref），但尚未覆盖远程 clone/fetch/push、rebase 执行、外部依赖或纯 Git restore。
- Sprint 4 之前，预览环境与标准化交付链路仍未纳入回滚后的联动验收。
