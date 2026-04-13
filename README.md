# MultiAgentCom

基于 `docs/` 中的产品与架构文档，当前仓库已启动 **Sprint 1 / M1 最小闭环** 的首轮开发。

## 已实现范围

- `GET /health`：健康检查
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
- `POST /projects/{id}/tasks/run`：触发单 Agent 串行执行
- `POST /projects/{id}/tasks/{taskId}/retry`：为失败任务创建独立重试任务
- `POST /projects/{id}/runs/parallel`：并行触发多个已就绪任务
- `GET /projects/{id}/runs/{runId}/status`：查询执行状态
- `POST /projects/{id}/delivery/export`：获取最新交付包
- `GET /projects/{id}/artifacts/{artifactId}/download`：下载交付包

## 设计说明

- 后端使用 Go 标准库先跑通最小闭环，领域层与 HTTP 层已解耦，后续可按 `docs/Tech-Stack-Decision.md` 平滑替换为 Gin。
- 当前使用**内存存储**，用于验证 Sprint 1 核心链路。
- 单 Agent 执行器当前为**规则驱动的占位实现**：会基于 PRD 生成最小交付包（README、占位源码、元数据）。
- Contract Hub 当前为**最小可演示实现**：支持基于最新 PRD 规则生成 CRUD 风格 API/Schema 契约，并按版本保存在内存中。
- 契约校验当前支持**合并前最小闸门**：可检查候选 endpoints/schemas 与契约的缺失、类型不一致、额外字段；若存在冲突，会拒绝校验并自动创建修复任务。
- 并行调度当前支持**双 Agent + 简单 DAG**：可生成后端/前端实现任务，并在依赖满足后触发集成任务；失败任务可单独创建 retry 任务，不影响其他任务继续推进。

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
