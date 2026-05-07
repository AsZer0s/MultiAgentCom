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
- 导出 ZIP 交付包结构符合 AC-15
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

- `GET /status/panel` 页面包含 `Agent Message Log`
- `GET /status/panel` 页面包含 `Failure Alerts`
- `GET /status/panel` 页面包含 `Audit Trail`
- `GET /status/panel` 页面包含 `Token Cost Trend`
- `GET /status/stream` 可持续接收 `status` SSE 事件（断开 SSE 后可由轮询兜底刷新）
- `GET /ready` 返回 readiness 检查结果；配置无效时应返回 `503`
- `GET /projects/{id}/alerts?limit=1&since=...` 返回分页后的关键失败告警流
- 配置 `MULTI_AGENT_ALERT_WEBHOOK_URL` 后，失败告警可推送到外部 webhook sink
- `GET /projects/{id}/communications?taskId=...&limit=...&offset=...` 返回 `checksum` 和分页元数据
- `GET /projects/{id}/audit-logs?limit=1&since=...` 返回分页后的关键操作审计流
- `GET /projects/{id}/token-costs?taskId=...` 返回 `totalTokens` 与 `budgetStatus`
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
