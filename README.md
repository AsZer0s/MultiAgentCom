# MultiAgentCom

基于 `docs/` 中的产品与架构文档，当前仓库已启动 **Sprint 1 / M1 最小闭环** 的首轮开发。

## 已实现范围

- `GET /health`：健康检查
- `GET /status/matrix`：查看全局或按项目过滤的状态矩阵数据
- `GET /status/panel`：打开最小状态矩阵面板
- `POST /projects`：创建项目
- `POST /projects/{id}/requirements`：提交需求
- `GET /projects/{id}/requirements`：查看需求
- `POST /projects/{id}/plan`：生成结构化 PRD + 首个任务
- `POST /projects/{id}/contracts/generate`：基于最新 PRD 生成契约
- `POST /projects/{id}/contracts/validate`：校验候选实现是否符合契约
- `GET /projects/{id}/contracts`：查看项目下契约版本列表
- `GET /projects/{id}/contracts/{contractId}`：读取单个契约
- `POST /projects/{id}/tasks/dispatch`：基于最新契约生成前后端并行任务
- `GET /projects/{id}/tasks`：查看项目任务列表与依赖
- `POST /projects/{id}/tasks/{taskId}/context/generate`：为指定任务生成上下文切片
- `GET /projects/{id}/tasks/{taskId}/context`：查看该任务最新上下文注入结果
- `POST /projects/{id}/tasks/{taskId}/sandbox/fail`：为指定任务注入一次性私有沙盒失败
- `POST /projects/{id}/overrides`：为运行中的任务注入人工高优指令，在安全检查点进入并恢复 `HUMAN_OVERRIDE`
- `POST /projects/{id}/locks`：注册带 `LOCKED BY HUMAN` 标记的人工代码片段，后续自动产物生成时保留为真值
- `POST /projects/{id}/shared-sandbox/merge`：将多个已完成任务的产物送入共享沙盒并执行合并闸门
- `GET /projects/{id}/snapshots`：查看项目时间线快照与分支
- `POST /projects/{id}/snapshots/rollback`：回滚到指定快照并创建新的平行时间线分支
- `POST /projects/{id}/tasks/run`：触发单 Agent 串行执行
- `POST /projects/{id}/tasks/{taskId}/retry`：为失败任务创建独立重试任务
- `POST /projects/{id}/runs/parallel`：并行触发多个已就绪任务
- `GET /projects/{id}/runs/{runId}/status`：查询执行状态
- `GET /projects/{id}/runs/{runId}/sandbox`：查看指定 run 的私有沙盒信息
- `GET /projects/{id}/sandboxes`：查看项目下全部私有沙盒
- `POST /projects/{id}/delivery/export`：获取最新交付包
- `GET /projects/{id}/artifacts/{artifactId}/download`：下载交付包

## 设计说明

- 后端使用 Go 标准库先跑通最小闭环，领域层与 HTTP 层已解耦，后续可按 `docs/Tech-Stack-Decision.md` 平滑替换为 Gin。
- 当前使用**内存存储**，用于验证 Sprint 1 核心链路。
- 单 Agent 执行器当前为**规则驱动的占位实现**：会基于 PRD 生成最小交付包（README、占位源码、元数据）。
- Contract Hub 当前为**最小可演示实现**：支持基于最新 PRD 规则生成 CRUD 风格 API/Schema 契约，并按版本保存在内存中。
- 契约校验当前支持**合并前最小闸门**：可检查候选 endpoints/schemas 与契约的缺失、类型不一致、额外字段；若存在冲突，会拒绝校验并自动创建修复任务。
- 并行调度当前支持**双 Agent + 简单 DAG**：可生成后端/前端实现任务，并在依赖满足后触发集成任务；失败任务可单独创建 retry 任务，不影响其他任务继续推进。
- Context Engine 当前支持**按任务角色切片注入**：后端任务会拿到 API/Schema 重点，前端任务会拿到 UX/验收重点；每次生成都会记录 `version` 和 `sources`，可回查最新注入结果。
- 状态矩阵面板当前支持**最小可视化监控**：可查看项目级任务矩阵、Agent 状态汇总，并在 `/status/panel` 中按项目过滤与自动刷新。
- 私有沙盒运行时当前支持**每个 run 独立工作目录**：系统会为每次执行分配显式 `sandboxId` 和独立 `rootPath`；单个沙盒失败会标记为 `FAILED`，不会影响其他并行任务继续产生产物。
- HITL 当前支持**最小人工接管**：运行中的任务可通过 `POST /projects/{id}/overrides` 进入 `HUMAN_OVERRIDE`，执行器会在安全检查点应用指令并恢复执行，任务审计与运行摘要会记录这次接管。
- 代码锁定当前支持**最小人工真值保护**：人类可通过 `POST /projects/{id}/locks` 注册包含 `LOCKED BY HUMAN` 标记的文件内容；后续自动生成 bundle 时会保留这段人工内容，并在 `metadata/lock-conflicts.log` 记录跳过覆盖行为。
- 共享沙盒当前支持**最小合并闸门**：只有 `DONE` 任务的成功产物才可进入 `SHARED` 沙盒；可在合并前执行契约校验并生成修复任务，或在模拟集成失败时阻断进入主交付链路。
- Timeline 当前支持**最小快照与回滚**：共享沙盒成功合并后会自动生成稳定快照；共享沙盒集成失败时会自动回滚到最近稳定快照，并创建新的 branch 保留原时间线；手动回滚也会清理旧的上下文注入记录。

## 本地运行

```bash
go run ./cmd/server
```

默认监听：`http://127.0.0.1:8080`

## 测试

```bash
go test ./...
```

## 快速演示

另附 `scripts/demo.sh`：

```bash
go run ./cmd/server
# 新开终端
bash scripts/demo.sh
```
