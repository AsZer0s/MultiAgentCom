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
| AC-10 | 人类锁定代码不可覆盖 | `TestCodeLockPreservesHumanContent`、`TestHTTPApplyCodeLock` |
| AC-11 | 语义快照与平行分支 | `TestRollbackToSnapshotCreatesParallelBranchTimeline`、`TestHTTPRollbackSnapshotCreatesParallelBranchTimeline` |

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
  - 后续自动 bundle 生成会保留人工代码内容，并在 `metadata/lock-conflicts.log` 记录“跳过覆盖”行为。

### AC-11 Snapshot Timeline Branching

- Result: `PASS`
- Notes:
  - `GET /projects/{id}/snapshots` 可查看稳定快照和回滚后产生的新时间线分支。
  - `POST /projects/{id}/snapshots/rollback` 不会覆写旧历史，而是创建新的 rollback branch，并保留原 `main` 分支快照。

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

- 当前 HITL 与代码锁定仍是 MVP 级内存模型，尚未接入真实代码仓或 AST 级锁区解析。
- 时间线快照当前为语义内存快照，尚未覆盖外部依赖、真实文件系统和多分支工作树。
- Sprint 4 之前，预览环境与标准化交付链路仍未纳入回滚后的联动验收。
