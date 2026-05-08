# Sprint 4 Acceptance Report

本报告对应 `docs/Implementation-Backlog.md` 中的 `BL-306 Sprint 4 验收与回归`，覆盖 AC-12 / AC-13 / AC-14 / AC-15。

## Scope

- 验收版本：`main` 分支，包含 BL-301 / BL-302 / BL-303 / BL-304 / BL-305 的当前工作区状态
- 验收环境：Go 标准库 HTTP 服务，默认内存存储；文件存储、scoped auth、readiness 与 token budget 已有回归覆盖，`go test ./...`
- 示例场景：Todo 全栈（状态面板、通信日志、Token 成本趋势、预览、标准交付包）

## Coverage

| Acceptance Case | Goal | Coverage |
| --- | --- | --- |
| AC-12 | 状态矩阵与拓扑可视化 | `TestHTTPStatusMatrixAndPanel`、`TestHTTPSprintTwoAcceptanceFlow` |
| AC-13 | 内部通信日志可追踪 | `TestListCommunicationLogsWithTaskFilter`、`TestHTTPListCommunicationLogs` |
| AC-14 | 预览环境可验收 | `TestStartPreviewFromSharedSandbox`、`TestHTTPStartPreview` |
| AC-15 | 标准化交付打包 | `TestSprintOneFlow`、`TestHTTPFlow` |

## Results

### AC-12 Status Matrix And Topology

- Result: `PASS`
- Notes:
  - `GET /status/panel` 提供项目过滤、任务拓扑、通信日志区块和 Token 成本趋势区块。
  - 当前拓扑以 SVG 展示任务依赖边、agent lane、运行状态和通信 badge；点击任务节点会按 `taskId` 过滤通信日志和成本条目，满足 MVP 级可视化要求。

### AC-13 Communication Traceability

- Result: `PASS`
- Notes:
  - `GET /projects/{id}/communications` 支持按 `taskId` 过滤，并支持 `limit`、`offset`、`since`、`until` 分页/时间过滤。
  - 日志包含 `version`、`from`、`to`、`type`、`taskId`、`checksum`、`timestamp`。
  - 状态面板可按 `taskId` 高亮对应任务和通信条目。

### AC-14 Preview Readiness

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/preview/start` 可从最新共享沙盒启动预览。
  - 预览页提供 Todo 交互示例与 revision 检查，满足 MVP 级演示与验收入口需求。

### AC-15 Standard Delivery Bundle

- Result: `PASS`
- Notes:
  - `POST /projects/{id}/delivery/export` 下载的 ZIP 包含后端、前端、Compose 和 README。
  - 交付包结构已覆盖 `go.mod`、`package.json`、`docker-compose.yml`、`README.md` 等标准工程化内容。

## Regression Summary

- 执行命令：`go test ./...`
- 结果：通过
- 补充检查：
  - `bash scripts/security-check.sh`
  - `bash scripts/demo.sh`
  - `bash scripts/alert-smoke.sh`
  - `bash scripts/release-check.sh`

## Demo Entry

- 状态面板：`GET /status/panel`
- 通信日志：`GET /projects/{id}/communications`
- 审计日志：`GET /projects/{id}/audit-logs`
- 告警流：`GET /projects/{id}/alerts`
- Token 成本：`GET /projects/{id}/token-costs`
- 预览启动：`POST /projects/{id}/preview/start`
- 交付导出：`POST /projects/{id}/delivery/export`
- 一键回归：`bash scripts/release-check.sh`

## Residual Risk

- Token 与成本已支持 runtime provider 返回 usage 优先、估算 fallback、可配置单价和 warn/block budget；HTTP runtime provider 已有 `runtime.http.v1` 协议、Bearer outbound auth、有界 retry、结构化错误和 response size/timeout/malformed 归一化，真实账单仍需由外部模型供应商账务系统对账。
- 状态面板已接入内嵌 SVG 任务拓扑，但仍是零构建的 vanilla MVP，不是完整图编辑器或复杂图分析工作台。
- 交付包虽然已可本地启动，但仍属于示例工程结构，未覆盖真实业务依赖与外部基础设施。
- 当前鉴权已支持本地多 token、roles、project scope、disabled 和 expiry；仍未接入企业级 OIDC/OAuth、租户目录或集中权限系统。
