# Sprint 2 Acceptance Report

本报告对应 `docs/Implementation-Backlog.md` 中的 `BL-106 Sprint 2 验收与回归`，覆盖 AC-03 / AC-04 / AC-05 / AC-06 / AC-12。

## Scope

- 验收版本：`main` 分支，Checkpoint `e56af97` 之后的工作区状态
- 验收环境：Go 标准库 HTTP 服务，内存存储，`go test ./...`
- 示例场景：Todo 全栈（后端 API + 前端页面 + 状态矩阵）

## Coverage

| Acceptance Case | Goal | Coverage |
| --- | --- | --- |
| AC-03 | 契约先行生成 | `TestHTTPContractFlow`、`TestHTTPSprintTwoAcceptanceFlow` |
| AC-04 | 契约冲突阻止合并 | `TestHTTPContractValidationConflict`、`TestHTTPSprintTwoAcceptanceFlow` |
| AC-05 | 动态路由并行执行 | `TestDispatchTasksAndParallelRun`、`TestHTTPParallelDispatchFlow`、`TestHTTPSprintTwoAcceptanceFlow` |
| AC-06 | 精准上下文注入 | `TestGenerateTaskContextSlicesByRole`、`TestHTTPTaskContextFlow`、`TestHTTPSprintTwoAcceptanceFlow` |
| AC-12 | 状态矩阵与拓扑可视化 | `TestStatusMatrixAggregatesTasksAndAgents`、`TestHTTPStatusMatrixAndPanel`、`TestHTTPSprintTwoAcceptanceFlow` |

## Results

### AC-03 Contract First Generation

- Result: `PASS`
- Notes:
  - 可通过 `POST /projects/{id}/contracts/generate` 生成 `v1` 契约。
  - 契约包含 CRUD 风格 endpoints 和至少一组 schema 定义。

### AC-04 Contract Conflict Blocks Merge

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/contracts/validate` 在冲突场景下返回 `409 Conflict`。
  - 返回结构化冲突详情，并自动生成 `CONTRACT_REWORK` 修复任务。

### AC-05 Dynamic Parallel Routing

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/tasks/dispatch` 会生成后端、前端、集成三个任务。
  - `POST /projects/{id}/runs/parallel` 可并行启动前后端任务。
  - 集成任务保留依赖，等待前置任务完成后再执行。

### AC-06 Precise Context Injection

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/tasks/{taskId}/context/generate` 会按任务角色切片。
  - 后端上下文包含 API / Schema 重点，前端上下文包含 UX Scope / Acceptance 重点。
  - 每次注入都记录 `version` 和 `sources`，支持通过 `GET /projects/{id}/tasks/{taskId}/context` 查询最新结果。

### AC-12 Status Matrix And Topology

- Result: `PASS`
- Notes:
  - `GET /status/matrix` 返回项目级任务矩阵、Agent 状态汇总和聚合计数。
  - `GET /status/panel` 提供最小 HTML 面板，可按项目过滤并自动刷新。
  - 当前“拓扑”展示以任务依赖 `dependsOn` 为主，已满足 MVP 级依赖流向查看需求。

## Regression Summary

- 执行命令：`go test ./...`
- 结果：通过
- 涉及模块：
  - `internal/service`
  - `internal/httpapi`
  - `internal/domain`

## Demo Entry

- 本地演示脚本：`bash scripts/demo.sh`
- 状态面板入口：`GET /status/panel`
- 状态矩阵 JSON：`GET /status/matrix`

## Residual Risk

- 当前状态矩阵面板仍是服务端内嵌 HTML 的最小版，不包含更细的拓扑图形可视化。
- 任务执行仍是规则驱动占位逻辑，尚未接入真实多 Agent 运行时或 WebSocket 实时推送。
- Sprint 3 之前，沙盒隔离、回滚、HITL 仍未进入验收范围。
