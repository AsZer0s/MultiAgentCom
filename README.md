# MultiAgentCom

基于 `docs/` 中的产品与架构文档，当前仓库已完成 **Sprint 4 / M4 MVP 验收**。

## 已实现范围

- `GET /health`：健康检查
- `GET /status/matrix`：查看全局或按项目过滤的状态矩阵数据
- `GET /status/panel`：打开 WebUI 运维 dashboard，集中查看 readiness、任务拓扑、状态矩阵、告警、审计、通信日志、Token 成本、沙盒和快照
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
- 单 Agent 执行器当前为**规则驱动的本地交付实现**，同时支持通过 `MULTI_AGENT_RUNTIME_PROVIDER=http` 接入外部 runtime provider；HTTP runtime 使用 `runtime.http.v1` 协议发送 `protocolVersion` 与 `X-MultiAgentCom-Runtime-Protocol`，支持嵌套 `usage`、兼容旧版 flat token 字段，并把非 2xx、超时、网络、malformed、超大响应和协议版本不匹配归一化为结构化 provider error；系统会基于 PRD 生成标准交付包（README、Go 后端、Node 预览前端、Dockerfile、Compose、元数据），并在 `metadata/manifest.json` 输出 `delivery.bundle.v1` 交付契约、在 `metadata/release-gate.json` 输出本地 release gate 报告。
- Contract Hub 当前为**最小可演示实现**：支持基于最新 PRD 规则生成 CRUD 风格 API/Schema 契约，并按版本保存在服务状态中。
- 契约校验当前支持**合并前最小闸门**：可检查候选 endpoints/schemas 与契约的缺失、类型不一致、额外字段；若存在冲突，会拒绝校验并自动创建修复任务。
- 并行调度当前支持**双 Agent + 简单 DAG**：可生成后端/前端实现任务，并在依赖满足后触发集成任务；失败任务可单独创建 retry 任务，不影响其他任务继续推进。
- Context Engine 当前支持**按任务角色切片注入**：后端任务会拿到 API/Schema 重点，前端任务会拿到 UX/验收重点；每次生成都会记录 `version` 和 `sources`，可回查最新注入结果。
- WebUI 运维 dashboard 当前支持**集中可视化监控**：可在 `/status/panel` 查看 readiness、项目级任务拓扑 SVG、任务矩阵、Agent 状态汇总、KPI、失败告警、审计轨迹、通信日志、Token 成本趋势、私有沙盒和时间线快照；拓扑按依赖深度和 assignee agent 分 lane 展示，点击任务节点会按 `taskId` 过滤通信日志与成本条目，状态更新采用 SSE 实时推送并提供轮询兜底。
- 通信日志当前支持**链路可视化与分页读取**：会记录任务派发、上下文注入、运行启动、人工接管、代码锁等内部消息，并可在 `/projects/{id}/communications` 或 `/status/panel` 中按 `taskId` 过滤和高亮查看；HTTP 列表支持 `limit`、`offset`、`since`、`until`。
- 告警基线当前支持**失败通知与分页读取**：run 失败和共享沙盒关键失败会沉淀为 `/projects/{id}/alerts` 中的告警流，并在 `/status/panel` 里直接展示；告警与审计列表均支持分页和时间过滤。
- Token 成本监控当前支持**真实 usage 优先与预算状态**：runtime provider 返回 token usage 时优先采用真实值，否则使用估算 fallback；单价和 warn/block 预算阈值可通过环境变量配置，可通过 `/projects/{id}/token-costs` 或 `/status/panel` 查看按任务的趋势条目。
- 私有沙盒运行时当前支持**每个 run 独立工作区**：默认 `workspaceProvider=directory` 会为每次执行分配显式 `sandboxId`、独立 `rootPath`、`workspacePath` 并写入 `.multiagent/workspace-manifest.json`；也可通过 `MULTI_AGENT_WORKSPACE_PROVIDER=git`、`MULTI_AGENT_WORKSPACE_GIT_REPO_PATH` 和 `MULTI_AGENT_WORKSPACE_GIT_BASE_REF` 启用 Git worktree provider，在本地已有 repo 或通过 `MULTI_AGENT_WORKSPACE_GIT_REMOTE_URL` clone 的 repo 上创建任务分支 worktree、提交任务产物并记录 `workspaceBranch` / `workspaceHeadRef`；`MULTI_AGENT_WORKSPACE_GIT_FETCH_BEFORE_USE=true` 会在创建 worktree 前 fetch，`MULTI_AGENT_WORKSPACE_GIT_PUSH_ENABLED=true` 会非 force push task/shared 分支。单个沙盒失败会标记为 `FAILED`，不会影响其他并行任务继续产生产物。
- HITL 当前支持**最小人工接管**：运行中的任务可通过 `POST /projects/{id}/overrides` 进入 `HUMAN_OVERRIDE`，执行器会在安全检查点应用指令并恢复执行，任务审计与运行摘要会记录这次接管。
- 代码锁定当前支持**人工真值保护与 Go AST 级锁**：人类可通过 `POST /projects/{id}/locks` 注册包含 `LOCKED BY HUMAN` 标记的文件内容；默认 `file` 模式会在 bundle 生成末尾覆盖整文件，`go_symbol` 模式可锁定 Go `func`、`method`、`type`、`var`、`const` 并只替换对应声明，method 可用 `symbolName=Receiver.Method` 精确区分同名接收者，要求 marker 位于被锁定 symbol 或其 doc comment 内；同时支持用 locked content 的 package/import 上下文创建缺失 Go 文件、合并锁内容所需 imports、移除替换后不再使用的普通 imports，冲突会写入 `metadata/lock-conflicts.log`。
- 共享沙盒当前支持**最小合并闸门**：只有 `DONE` 任务的成功产物才可进入 `SHARED` 沙盒；可在合并前执行契约校验并生成修复任务。directory provider 成功合并会把交付 ZIP materialize 到 `workspace/artifacts/<artifactId>/` 并写入共享 `.multiagent/workspace-manifest.json`；Git provider 会创建共享 worktree、对每个任务 head 执行 `git merge --no-ff`，并在成功后记录共享 `workspaceHeadRef`，随后可安全清理已合并的私有 worktree。
- Timeline 当前支持**快照与回滚**：共享沙盒成功合并后会自动生成稳定快照；文件存储模式下快照会落盘 `state.json` 与 `manifest.json` 并记录 checksum，回滚可从 `file://` StateRef 解析状态并校验 checksum；Git provider 合并成功后会额外在快照中记录 `workspaceStateRef=repo://local/<branch>@<commit>` 与 `workspaceChecksum`，回滚到 Git workspace snapshot 时会校验 ref/checksum 并创建新的受管 shared rollback worktree/branch 指向 snapshot commit，不 reset、不覆盖原 shared worktree；共享沙盒集成失败时会自动回滚到最近稳定快照，并创建新的 branch 保留原时间线。
- Preview Service 当前支持**最小可验收预览**：共享沙盒合并完成后可启动带 revision 检查的 Todo 预览页，便于验收演示。
- 安全基线当前支持**多 token scoped auth 与审计**：除 `MULTI_AGENT_API_TOKEN` 兼容模式外，可通过 `MULTI_AGENT_AUTH_TOKENS` 或 `MULTI_AGENT_AUTH_TOKENS_FILE` 配置带 actor、roles、project scope、disabled、expiry 的 token 记录；关键操作会写入 `/projects/{id}/audit-logs` 审计流。
- 告警通知当前支持**最小 webhook 主动推送**：设置 `MULTI_AGENT_ALERT_WEBHOOK_URL` 后，run 失败和回滚事件会异步推送结构化 alert 到外部接收端。
- 状态面板当前支持**WebUI 运维总览**：同一页面可查看 readiness、任务拓扑、任务矩阵、失败告警、审计轨迹、通信日志、Token 成本趋势、沙盒和快照，更新机制为 SSE + 轮询兜底，并使用 DOM/SVG API 渲染接口数据以降低 XSS 风险。
- 运维 readiness 当前支持 `GET /ready`：会检查配置有效性、auth token 配置、存储/数据目录、artifact/sandbox 根目录可写性、Git workspace provider 的 repo/base ref 可用性以及 runtime provider 配置；配置 remote Git 后，readiness 可 clone 缺失 repo 并按需 fetch。
- Git workspace v5 当前支持 pure Git snapshot restore MVP：在 v4 的 manual private rebase 基础上，`POST /projects/{id}/snapshots/rollback` 会同时恢复 service state（`memory://` / `file://` StateRef）与 Git workspace state（`workspaceStateRef` / `workspaceChecksum`）；恢复会创建新的受管 shared rollback worktree/branch，HEAD 等于原 snapshot checksum，并作为后续 shared merge 的 base。`POST /projects/{id}/workspaces/rebase` 仍只作用于受管且已 release 的 private Git task workspace，dirty content worktree 会被拒绝，冲突会自动 `git rebase --abort` 并保留原 head；shared branch rebase、force push、remote branch 删除、repo-only service-state restore 仍是后续工作。

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
- 同时会执行 `DEMO_RUNS=1 bash scripts/release-check.sh` 作为 release smoke，保证预览、交付包、`delivery.bundle.v1` 契约和关键脚本链路可用
- 本地发布前仍建议执行默认三轮的 `bash scripts/release-check.sh`
