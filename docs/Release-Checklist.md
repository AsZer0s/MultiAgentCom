# Release Checklist

本清单对应 `docs/Implementation-Backlog.md` 中的 `BL-305 E2E 演示脚本与发布检查`。

## Preflight

- 本地环境可执行 `go test ./...`
- 本地可执行 `/usr/bin/python3`
- 本地可执行 `curl`、`unzip`
- 端口 `8080` 未被其他进程占用

## One-Command Check

```bash
bash scripts/release-check.sh
```

预期结果：

- `go test ./...` 通过
- `bash scripts/security-check.sh` 通过
- `bash scripts/demo.sh` 端到端执行通过
- `bash scripts/alert-smoke.sh` 验证失败告警与 webhook 推送通过
- `bash scripts/auth-smoke.sh` 验证 token 鉴权兼容路径通过
- 预览环境可访问
- 导出 ZIP 交付包结构符合 AC-15，并通过 `delivery.bundle.v1` / `delivery.release_gate.v1` 契约校验
- Sprint 4 验收文档存在

## Demo Walkthrough

启动服务：

```bash
go run ./cmd/server
```

新开终端执行单轮演示：

```bash
bash scripts/demo.sh
```

执行三轮重复演示：

```bash
RUNS=3 bash scripts/demo.sh
```

验证主动告警通知：

```bash
bash scripts/alert-smoke.sh
```

验证鉴权基线：

```bash
API_TOKEN=your-token BASE_URL=http://127.0.0.1:18082 bash scripts/auth-smoke.sh
```

## Manual Verification Points

- `GET /status/panel` 页面包含 `Operations Dashboard`
- `GET /status/panel` 页面包含 `Readiness`
- `GET /status/panel` 页面包含 `Status Matrix`
- `GET /status/panel` 页面包含 `Task Topology`
- `GET /status/panel` 任务拓扑展示依赖边、agent lane、通信 badge，并可点击任务节点按 `taskId` 过滤日志
- `GET /status/panel` 页面包含 `Agent Message Log`
- `GET /status/panel` 页面包含 `Failure Alerts`
- `GET /status/panel` 页面包含 `Audit Trail`
- `GET /status/panel` 页面包含 `Token Cost Trend`
- `GET /status/panel` 页面包含 `Sandboxes`
- `GET /status/panel` 页面包含 `Snapshots`
- `GET /status/stream` 可持续接收 `status` SSE 事件（断开 SSE 后可由轮询兜底刷新）
- `GET /ready` 返回 readiness 检查结果；配置无效时应返回 `503`
- `GET /projects/{id}/alerts?limit=1&since=...` 返回分页后的关键失败告警流
- 配置 `MULTI_AGENT_ALERT_WEBHOOK_URL` 后，失败告警可推送到外部 webhook sink
- `GET /projects/{id}/communications?taskId=...&limit=...&offset=...` 返回 `checksum` 和分页元数据
- `GET /projects/{id}/audit-logs?limit=1&since=...` 返回分页后的关键操作审计流
- `GET /projects/{id}/token-costs?taskId=...` 返回 `totalTokens` 与 `budgetStatus`
- 私有 run sandbox 返回 `workspacePath`、默认 `workspaceProvider=directory` 和 `workspaceManifestRef`，且工作区包含 `.multiagent/workspace-manifest.json`
- 配置 `MULTI_AGENT_WORKSPACE_PROVIDER=git`、`MULTI_AGENT_WORKSPACE_GIT_REPO_PATH` 和 `MULTI_AGENT_WORKSPACE_GIT_BASE_REF` 后，私有 run sandbox 会创建真实 Git worktree、任务分支和 `workspaceHeadRef`
- 配置 `MULTI_AGENT_WORKSPACE_GIT_REMOTE_URL` 后，`GET /ready` 可从 remote clone 缺失 repo；`MULTI_AGENT_WORKSPACE_GIT_FETCH_BEFORE_USE=true` 与 `WorkspaceGitBaseRef=origin/main` 可在创建 worktree 前更新 remote-tracking ref
- `MULTI_AGENT_WORKSPACE_GIT_PUSH_ENABLED=true` 后，private task branch 与 shared branch 会非 force push 到 remote；remote URL 中嵌入凭据应被配置校验拒绝，token 不应出现在错误消息中
- Git provider 完成 run 后，任务分支 commit 中包含 `tasks/<taskId>/bundle/metadata/manifest.json`
- 共享沙盒成功合并后，directory provider 的 `workspace/artifacts/<artifactId>/` 包含 materialized delivery bundle 内容，且共享工作区包含 `.multiagent/workspace-manifest.json`
- Git provider 的共享沙盒成功合并后，私有任务 head 是共享 `workspaceHeadRef` 的 ancestor，快照包含 `workspaceStateRef` 与 `workspaceChecksum`
- Git provider 成功合并后，已合并私有 worktree 会被安全清理或记录跳过原因；released shared worktree/branch 保留
- `POST /projects/{id}/workspaces/cleanup` dry-run 返回计划清理结果且不移除 worktree；`deleteBranches=true` 只尝试非 force 删除已合并私有分支
- `POST /projects/{id}/workspaces/rebase` 必须显式传入 `targetRef` 且指定 `sandboxIds` 或 `all=true`；只处理受管 Git private task workspace，dirty content worktree 被拒绝，冲突会 abort 并保持原 head；`publish=true` 只执行非 force push，远端 non-fast-forward 拒绝时远端分支保持不变
- 文件存储模式下，快照 rollback 可从 `file://` StateRef 恢复；checksum 被篡改时应拒绝回滚
- `POST /projects/{id}/locks` 可提交 `lockMode=go_symbol`、`language=go`、`symbolKind=func|method|type|var|const` 和 `symbolName`，并只替换对应 Go 声明，同时合并锁内容所需 imports、移除替换后不再使用的普通 imports
- `MULTI_AGENT_RUNTIME_PROVIDER=http` runtime provider 返回 `runtime.http.v1` success 时可执行成功并采用嵌套 `usage`
- `MULTI_AGENT_RUNTIME_HTTP_BEARER_TOKEN` 配置后，runtime provider 请求携带 `Authorization: Bearer ...`
- `MULTI_AGENT_RUNTIME_HTTP_MAX_ATTEMPTS` 配置后，runtime provider 对 retryable 失败执行有界重试；`release-check.sh` 会验证一次 `503` 后成功恢复
- `MULTI_AGENT_RUNTIME_PROVIDER=http` runtime provider 返回结构化非 2xx error 时，run 失败原因包含稳定 code/status/retryable/requestId，且项目告警记录该失败
- 配置 `MULTI_AGENT_API_TOKEN` 后，未带 token 的 API 请求返回 `401`
- 配置 `MULTI_AGENT_API_TOKEN` 后，带 token 的 API 请求可正常创建项目并写入审计
- 配置 `MULTI_AGENT_AUTH_TOKENS` 或 `MULTI_AGENT_AUTH_TOKENS_FILE` 后，带 roles/project scope 的 token 可授权或拒绝对应关键操作
- `POST /projects/{id}/preview/start` 后预览页可访问
- 下载交付包后包含：
  - `README.md`
  - `docker-compose.yml`
  - `generated-app/go.mod`
  - `generated-app/main.go`
  - `generated-app/Dockerfile`
  - `web-app/package.json`
  - `web-app/server.js`
  - `web-app/index.html`
  - `web-app/Dockerfile`
  - `metadata/manifest.json`（`schemaVersion: delivery.bundle.v1`，包含必需文件 checksum/size 与本地入口）
  - `metadata/release-gate.json`（`schemaVersion: delivery.release_gate.v1`，`status: PASS`）

## Exit Criteria

- AC-14 通过
- AC-15 通过
- P1 中 AC-12 / AC-13 可演示
- 安全扫描通过，关键操作可通过审计日志追踪
- `RUNS=3 bash scripts/demo.sh` 可连续通过

## Status Stream Smoke

```bash
bash scripts/status-stream-smoke.sh
```

预期结果：

- `GET /status/stream` 返回 `200`
- 响应头 `Content-Type` 包含 `event-stream`
- SSE 输出包含 `event: status`
- SSE 输出包含 `data:`
