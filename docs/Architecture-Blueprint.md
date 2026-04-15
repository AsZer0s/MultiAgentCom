# MultiAgentCom 架构蓝图（Architecture Blueprint v0.1）

> 本文与 `docs/Spec-Refined.md`、`docs/MVP-Plan.md`、`docs/Acceptance-Cases.md` 对齐，目标是给出可直接进入开发的最小架构方案。

## 1. 架构目标

- 支持“需求到交付”的端到端自动化主链路。
- 支持多 Agent 并行协同与契约先行开发。
- 支持 HITL 接管、双层沙盒隔离、回滚恢复。
- 支持 MVP 快速落地，并可平滑演进到生产级。

## 2. 总体架构（逻辑分层）

- **体验层（UI/CLI）**
  - 项目创建、需求输入、状态面板、HITL 控制台、预览入口。
- **编排层（Orchestrator）**
  - 任务分解、依赖编排、调度路由、重试与超时控制。
- **智能执行层（Agent Runtime）**
  - Manager Agent 与 Worker Agents 的执行上下文与会话管理。
- **契约与上下文层（Contract + Context）**
  - 契约版本管理、上下文切片、注入策略与冲突校验。
- **运行时基础设施层（Sandbox + Timeline + Storage）**
  - 私有沙盒、共享沙盒、快照分支、日志、对象存储、元数据存储。
- **可观测与交付层（Observability + Delivery）**
  - 状态可视化、通信日志、Token 监控、预览部署与打包导出。

## 3. 核心组件与职责

### 3.1 API Gateway
- 对外统一入口（HTTP/WebSocket）。
- 鉴权、限流、请求追踪 ID 注入。

### 3.2 Project Service
- 管理项目、环境、配置。
- 维护系统级默认模板（PRD/契约/交付）。

### 3.3 Orchestrator Service
- 负责主流程状态机推进。
- 执行任务 DAG 编排（并行/串行/依赖）。
- 触发 Agent 运行、回调处理、失败补偿。

### 3.4 Agent Runtime Service
- 管理 Agent 实例生命周期。
- 适配不同模型提供商（抽象统一调用接口）。
- 执行结构化消息协议（含版本与 checksum）。

### 3.5 Contract Hub Service
- 存储并版本化 API/Schema 契约。
- 合并前执行契约一致性检查。
- 提供差异比对与冲突解释。

### 3.6 Context Engine Service
- 对 PRD/契约/历史任务进行切片。
- 按 Agent 角色注入最小必要上下文。
- 记录上下文来源与版本追踪信息。

### 3.7 Sandbox Runtime Service
- 管理私有沙盒（每 Agent 独立执行）。
- 管理共享沙盒（合并与集成验证）。
- 负责集成失败回滚。

### 3.8 Timeline Engine Service
- 维护语义快照。
- 管理平行分支与时间线切换。
- 回滚时广播失效上下文清理事件。

### 3.9 HITL Service
- 高优先级指令注入。
- 代码锁定策略管理（`LOCKED BY HUMAN`）。
- 人工干预审计日志记录。

### 3.10 Observability Service
- Agent 状态矩阵。
- 拓扑链路与任务流水。
- 通信日志、Token 成本、失败率监控。

### 3.11 Delivery Service
- 拉起预览环境。
- 生成标准交付包（依赖、部署、README）。

## 4. 核心数据流（主链路）

1. 用户提交需求 -> `Project Service`
2. `Orchestrator` 触发 PRD 生成任务 -> `Agent Runtime`
3. PRD 输出后触发契约生成 -> `Contract Hub`
4. `Orchestrator` 拆解任务 DAG -> 分发至多个 Worker Agent
5. Worker 在私有沙盒执行并提交 `Artifact`
6. 产物进入共享沙盒做集成测试
7. 成功则进入预览与交付；失败则回滚并创建修复任务
8. 全链路写入日志、快照、可观测指标

## 5. 时序（关键场景）

### 5.1 并行协同场景
- `Orchestrator` 根据契约将后端任务派发给 Go-Agent，前端任务派发给 Vue-Agent。
- 两个 Agent 并行运行，产物回传后统一合并。
- 若任一产物契约校验失败，仅退回对应子任务，不阻断其他可继续任务。

### 5.2 HITL 接管场景
- 人类发起高优先级指令。
- `HITL Service` 写入 override 事件并通知 `Orchestrator`。
- `Orchestrator` 在安全检查点暂停/改写任务上下文后继续执行。

### 5.3 回滚场景
- 共享沙盒集成测试失败。
- `Timeline Engine` 选择最近稳定快照创建新分支恢复。
- `Context Engine` 执行失效记忆清理并重新注入上下文。

## 6. 技术选型建议（MVP）

## 6.1 后端
- 语言：Go（主控编排与服务性能优先）
- Web 框架：Gin/Fiber（二选一）
- 任务队列：Redis Stream 或 NATS JetStream
- 工作流引擎：先用轻量自研状态机，后续可升级 Temporal

## 6.2 前端
- 框架：Vue 3 + Vite
- UI：Element Plus 或 Naive UI
- 实时状态：WebSocket 或 SSE

## 6.3 存储
- 元数据：PostgreSQL
- 缓存/队列：Redis
- 产物：对象存储（本地 MinIO，云上 S3 兼容）
- 日志检索：MVP 可先落地 PostgreSQL + 文件，后续接入 ELK/Loki

## 6.4 沙盒
- MVP：容器级隔离（Docker）+ 命名空间隔离
- 演进：gVisor/Firecracker（更强安全隔离）

## 6.5 部署
- MVP：`docker-compose`
- 演进：Kubernetes（按服务水平扩展）

## 7. 数据模型最小字段建议

### 7.1 `task`
- `id`, `project_id`, `type`, `status`, `priority`, `assignee_agent`, `input_ref`, `output_ref`, `created_at`, `updated_at`

### 7.2 `agent_run`
- `id`, `task_id`, `agent_type`, `model`, `status`, `sandbox_id`, `started_at`, `ended_at`, `error_code`

### 7.3 `contract`
- `id`, `project_id`, `name`, `version`, `schema_ref`, `checksum`, `created_by`, `created_at`

### 7.4 `artifact`
- `id`, `task_id`, `kind`, `uri`, `checksum`, `size_bytes`, `created_at`

### 7.5 `snapshot`
- `id`, `project_id`, `branch`, `source_snapshot_id`, `reason`, `state_ref`, `created_at`

### 7.6 `human_override`
- `id`, `project_id`, `task_id`, `operator`, `instruction`, `lock_scope`, `created_at`

## 8. API 草案（MVP）

- `POST /projects`
- `POST /projects/{id}/requirements`
- `POST /projects/{id}/plan`
- `POST /projects/{id}/contracts/generate`
- `POST /projects/{id}/tasks/run`
- `GET /projects/{id}/runs/{runId}/status`
- `GET /projects/{id}/communications`
- `GET /projects/{id}/audit-logs`
- `GET /projects/{id}/alerts`
- `GET /projects/{id}/token-costs`
- `POST /projects/{id}/overrides`
- `POST /projects/{id}/snapshots/rollback`
- `POST /projects/{id}/preview/start`
- `POST /projects/{id}/delivery/export`

## 9. 安全与合规基线（MVP）

- 所有 API 需带鉴权令牌。
- 敏感配置通过环境变量注入，禁止硬编码密钥。
- 沙盒网络默认最小权限，按需放开。
- 人工锁定区域写保护，需管理员权限解除。
- 关键操作（回滚、覆盖、导出）写审计日志。
- 关键失败事件应写入告警流，并可按需通过 webhook 推送到外部接收端。

## 10. 可观测性指标（建议初始仪表盘）

- 任务成功率、失败率、平均恢复时长。
- Agent 运行时长分布、并行度、重试次数。
- 契约冲突次数与冲突类型。
- 回滚触发次数与恢复成功率。
- Token 消耗与单位任务成本趋势。
- 告警数量、失败类型、webhook 推送成功率。

## 11. 演进路线

- **Phase A（MVP）**
  - 单集群、基础并发、手工运维。
- **Phase B（可扩展）**
  - 多租户、插件化 Agent、策略路由。
- **Phase C（生产级）**
  - 高可用编排、细粒度权限、全链路审计合规。

## 12. 开发落地建议（本周可执行）

- 先实现 `Orchestrator + Task 状态机 + 单 Agent 执行`。
- 同步实现 `Contract Hub` 最小版本（创建/查询/校验）。
- 预留 `HITL` 与 `Timeline` 的事件接口，不阻塞主链路。
- 用 `docker-compose` 统一本地开发环境（Postgres/Redis/MinIO）。
