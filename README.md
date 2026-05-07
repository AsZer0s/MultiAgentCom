# MultiAgentCom

基于 `docs/` 中的产品与架构文档，当前仓库已完成 **Sprint 4 / M4 MVP 验收**。

## 已实现范围

- `GET /health`：健康检查
- `GET /status/matrix`：查看全局或按项目过滤的状态矩阵数据
- `GET /status/panel`：打开最小状态矩阵面板
- `GET /status/stream`：通过 SSE 实时推送状态事件（状态面板使用，支持轮询兜底）
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
- `GET /projects/{id}/communications`：查看项目内部通信日志，支持 `taskId`、`limit`、`offset`、`since`、`until` 过滤
- `GET /projects/{id}/audit-logs`：查看项目关键操作审计日志，支持分页与时间过滤
- `GET /projects/{id}/alerts`：查看项目关键失败告警流，支持分页与时间过滤
- `GET /projects/{id}/token-costs`：查看按任务聚合的 Token、成本和预算状态趋势，支持 `taskId` 过滤
- `POST /projects/{id}/tasks/{taskId}/sandbox/fail`：为指定任务注入一次性私有沙盒失败
- `POST /projects/{id}/overrides`：为运行中的任务注入人工高优指令，在安全检查点进入并恢复 `HUMAN_OVERRIDE`
- `POST /projects/{id}/locks`：注册带 `LOCKED BY HUMAN` 标记的人工代码片段，后续自动产物生成时保留为真值
- `POST /projects/{id}/shared-sandbox/merge`：将多个已完成任务的产物送入共享沙盒并执行合并闸门
- `GET /projects/{id}/snapshots`：查看项目时间线快照与分支
- `POST /projects/{id}/snapshots/rollback`：回滚到指定快照并创建新的平行时间线分支
- `POST /projects/{id}/preview/start`：基于最新共享沙盒启动预览服务
- `GET /projects/{id}/preview/{previewId}`：打开预览页面
- `GET /projects/{id}/preview/{previewId}/status`：查询预览运行状态与 revision
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
- 存储支持默认内存模式，也可通过 `MULTI_AGENT_STORE_PROVIDER=file` 与 `MULTI_AGENT_DATA_ROOT` 启用文件持久化；项目、任务、运行、审计/告警/通信流、artifact 元数据与快照状态可在服务重启后恢复。
- 单 Agent 执行器当前为**规则驱动的本地交付实现**，同时支持通过 `MULTI_AGENT_RUNTIME_PROVIDER=http` 接入外部 runtime provider；系统会基于 PRD 生成标准交付包（README、Go 后端、Node 预览前端、Dockerfile、Compose、元数据）。
- Contract Hub 当前为**最小可演示实现**：支持基于最新 PRD 规则生成 CRUD 风格 API/Schema 契约，并按版本保存在服务状态中。
- 契约校验当前支持**合并前最小闸门**：可检查候选 endpoints/schemas 与契约的缺失、类型不一致、额外字段；若存在冲突，会拒绝校验并自动创建修复任务。
- 并行调度当前支持**双 Agent + 简单 DAG**：可生成后端/前端实现任务，并在依赖满足后触发集成任务；失败任务可单独创建 retry 任务，不影响其他任务继续推进。
- Context Engine 当前支持**按任务角色切片注入**：后端任务会拿到 API/Schema 重点，前端任务会拿到 UX/验收重点；每次生成都会记录 `version` 和 `sources`，可回查最新注入结果。
- 状态矩阵面板当前支持**最小可视化监控**：可查看项目级任务矩阵、Agent 状态汇总，并在 `/status/panel` 中按项目过滤；状态更新采用 SSE 实时推送并提供轮询兜底。
- 通信日志当前支持**链路可视化与分页读取**：会记录任务派发、上下文注入、运行启动、人工接管、代码锁等内部消息，并可在 `/projects/{id}/communications` 或 `/status/panel` 中按 `taskId` 过滤和高亮查看；HTTP 列表支持 `limit`、`offset`、`since`、`until`。
- 告警基线当前支持**失败通知与分页读取**：run 失败和共享沙盒关键失败会沉淀为 `/projects/{id}/alerts` 中的告警流，并在 `/status/panel` 里直接展示；告警与审计列表均支持分页和时间过滤。
- Token 成本监控当前支持**真实 usage 优先与预算状态**：runtime provider 返回 token usage 时优先采用真实值，否则使用估算 fallback；单价和 warn/block 预算阈值可通过环境变量配置，可通过 `/projects/{id}/token-costs` 或 `/status/panel` 查看按任务的趋势条目。
- 私有沙盒运行时当前支持**每个 run 独立工作目录**：系统会为每次执行分配显式 `sandboxId` 和独立 `rootPath`；单个沙盒失败会标记为 `FAILED`，不会影响其他并行任务继续产生产物。
- HITL 当前支持**最小人工接管**：运行中的任务可通过 `POST /projects/{id}/overrides` 进入 `HUMAN_OVERRIDE`，执行器会在安全检查点应用指令并恢复执行，任务审计与运行摘要会记录这次接管。
- 代码锁定当前支持**最小人工真值保护**：人类可通过 `POST /projects/{id}/locks` 注册包含 `LOCKED BY HUMAN` 标记的文件内容；后续自动生成 bundle 时会保留这段人工内容，并在 `metadata/lock-conflicts.log` 记录跳过覆盖行为。
- 共享沙盒当前支持**最小合并闸门**：只有 `DONE` 任务的成功产物才可进入 `SHARED` 沙盒；可在合并前执行契约校验并生成修复任务，或在模拟集成失败时阻断进入主交付链路。
- Timeline 当前支持**快照与回滚**：共享沙盒成功合并后会自动生成稳定快照；文件存储模式下快照会落盘 `state.json` 与 `manifest.json` 并记录 checksum；共享沙盒集成失败时会自动回滚到最近稳定快照，并创建新的 branch 保留原时间线。
- Preview Service 当前支持**最小可验收预览**：共享沙盒合并完成后可启动带 revision 检查的 Todo 预览页，便于验收演示。
- 安全基线当前支持**多 token scoped auth 与审计**：除 `MULTI_AGENT_API_TOKEN` 兼容模式外，可通过 `MULTI_AGENT_AUTH_TOKENS` 或 `MULTI_AGENT_AUTH_TOKENS_FILE` 配置带 actor、roles、project scope、disabled、expiry 的 token 记录；关键操作会写入 `/projects/{id}/audit-logs` 审计流。
- 告警通知当前支持**最小 webhook 主动推送**：设置 `MULTI_AGENT_ALERT_WEBHOOK_URL` 后，run 失败和回滚事件会异步推送结构化 alert 到外部接收端。
- 状态面板当前支持**最小运维总览**：同一页面可查看任务矩阵、失败告警、审计轨迹、通信日志和 Token 成本趋势，更新机制为 SSE + 轮询兜底。
- 运维 readiness 当前支持 `GET /ready`：会检查配置有效性、auth token 配置、存储/数据目录、artifact/sandbox 根目录可写性以及 runtime provider 配置；配置无效时服务启动会 fail fast。

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

如需连续回归三轮演示：

```bash
RUNS=3 bash scripts/demo.sh
```

如需执行完整发布检查：

```bash
bash scripts/release-check.sh
```

如需执行安全扫描：

```bash
bash scripts/security-check.sh
```

如需执行鉴权 smoke：

```bash
API_TOKEN=your-token BASE_URL=http://127.0.0.1:18082 bash scripts/auth-smoke.sh
```

CI 基线：

- GitHub Actions 会在 `push` / `pull_request` 上自动执行 `go test ./...`
- 同时会执行 `DEMO_RUNS=1 bash scripts/release-check.sh` 作为 release smoke，保证预览、交付包和关键脚本链路可用
- 本地发布前仍建议执行默认三轮的 `bash scripts/release-check.sh`
