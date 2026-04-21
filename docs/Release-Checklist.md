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
- `bash scripts/auth-smoke.sh` 验证单租户 token 鉴权通过
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
- `GET /projects/{id}/alerts` 返回关键失败告警流
- 配置 `MULTI_AGENT_ALERT_WEBHOOK_URL` 后，失败告警可推送到外部 webhook sink
- `GET /projects/{id}/communications?taskId=...` 返回 `checksum`
- `GET /projects/{id}/audit-logs` 返回关键操作审计流
- `GET /projects/{id}/token-costs?taskId=...` 返回 `totalTokens`
- 配置 `MULTI_AGENT_API_TOKEN` 后，未带 token 的 API 请求返回 `401`
- 配置 `MULTI_AGENT_API_TOKEN` 后，带 token 的 API 请求可正常创建项目并写入审计
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
