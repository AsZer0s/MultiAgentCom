# MultiAgentCom

多 Agent 协作软件开发平台 — 从需求到交付的全自动化工作流。

[![Go Tests](https://github.com/AsZer0s/multiagentcom/actions/workflows/ci.yml/badge.svg)](https://github.com/AsZer0s/multiagentcom/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## 功能特性

### 核心工作流
- **需求 → PRD → 合同 → 任务 → 交付** 全生命周期自动化
- 并行任务调度（后端/前端/集成）与依赖 DAG
- 私有沙盒隔离 + 共享沙盒合并闸门
- 时间线快照与回滚（平行分支）
- 交付包导出（ZIP：README、Go 后端、Node 前端、Dockerfile、Compose）

### AI 模型集成
- **Claude**（Anthropic Messages API）
- **OpenAI**（Chat Completions + legacy Codex Completions）
- **Gemini**（Google generateContent API）
- 可插拔 runtime registry，支持 mock / HTTP / container

### 前端 UI
- Vue 3 + TypeScript + Vite SPA
- 7 个视图：Dashboard、Projects、Detail、Task Board、HITL、Settings、Login
- SSE 实时状态流 + 自动重连
- Admin 配置面板（Runtime / Token Cost / S3 / Alerts / OIDC）

### 安全
- OIDC + RBAC 认证授权（RS256 JWT 签名验证）
- CORS 可配置白名单
- Rate limiting（100 req/min per IP）
- 安全头（HSTS / X-Frame-Options / XSS-Protection）
- 输入长度校验 + 错误信息脱敏
- Postgres Advisory Lock 分布式并发控制
- Idempotency Key 幂等写入保护

### 生产级
- Docker 多阶段构建（非 root 用户）
- Kubernetes 部署清单（namespace / configmap / secret / deployment / service / ingress）
- Prometheus /metrics 端点（HTTP / 业务 / LLM 指标 + histogram）
- Postgres 自动迁移（21 张投影表）
- S3/MinIO artifact 存储（AWS SigV4 签名）
- 优雅关闭 + sandbox 清理
- OpenAPI 3.0 规范

## 快速开始

### 本地运行

```bash
# 克隆仓库
git clone https://github.com/AsZer0s/multiagentcom.git
cd multiagentcom

# 启动服务（默认内存存储）
go run ./cmd/server

# 访问
# API:  http://localhost:8080
# 面板: http://localhost:8080/status/panel
```

### Docker 部署

```bash
# 复制配置
cp .env.example .env
# 编辑 .env 设置 API token 和 LLM API key

# 启动
docker-compose up -d

# 查看日志
docker-compose logs -f app
```

### Kubernetes 部署

```bash
# 创建命名空间和资源
kubectl apply -f k8s/

# 修改 secret（生产环境必须）
kubectl edit secret multiagentcom-secrets -n multiagentcom
kubectl edit secret postgres-secrets -n multiagentcom

# 查看状态
kubectl get pods -n multiagentcom
```

## 配置

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MULTI_AGENT_ADDR` | `:8080` | 监听地址 |
| `MULTI_AGENT_API_TOKEN` | - | API Token |
| `MULTI_AGENT_STORE_PROVIDER` | `memory` | 存储后端（memory / file / postgres） |
| `MULTI_AGENT_POSTGRES_DSN` | - | Postgres 连接串 |
| `MULTI_AGENT_RUNTIME_PROVIDER` | `local` | Runtime（local / claude / openai / gemini / http / container） |
| `MULTI_AGENT_RUNTIME_CLAUDE_API_KEY` | - | Claude API Key |
| `MULTI_AGENT_RUNTIME_OPENAI_API_KEY` | - | OpenAI API Key |
| `MULTI_AGENT_RUNTIME_GEMINI_API_KEY` | - | Gemini API Key |
| `MULTI_AGENT_CORS_ALLOWED_ORIGINS` | - | CORS 允许的 origin（逗号分隔） |
| `MULTI_AGENT_WEB_ROOT` | - | Vue SPA 静态文件目录 |

完整配置参见 [.env.example](.env.example)。

### LLM Provider 选择

```bash
# Claude
MULTI_AGENT_RUNTIME_PROVIDER=claude
MULTI_AGENT_RUNTIME_CLAUDE_API_KEY=sk-ant-...
MULTI_AGENT_RUNTIME_CLAUDE_MODEL=claude-sonnet-4-20250514

# OpenAI
MULTI_AGENT_RUNTIME_PROVIDER=openai
MULTI_AGENT_RUNTIME_OPENAI_API_KEY=sk-...
MULTI_AGENT_RUNTIME_OPENAI_MODEL=gpt-4o

# OpenAI 兼容（Ollama / vLLM / LM Studio）
MULTI_AGENT_RUNTIME_PROVIDER=openai
MULTI_AGENT_RUNTIME_OPENAI_API_KEY=dummy
MULTI_AGENT_RUNTIME_OPENAI_BASE_URL=http://localhost:11434/v1
MULTI_AGENT_RUNTIME_OPENAI_MODEL=llama3

# Codex 旧格式
MULTI_AGENT_RUNTIME_PROVIDER=openai
MULTI_AGENT_RUNTIME_OPENAI_FORMAT=completions
MULTI_AGENT_RUNTIME_OPENAI_MODEL=code-davinci-002

# Gemini
MULTI_AGENT_RUNTIME_PROVIDER=gemini
MULTI_AGENT_RUNTIME_GEMINI_API_KEY=AIza...
MULTI_AGENT_RUNTIME_GEMINI_MODEL=gemini-2.0-flash
```

## API 端点

### 系统
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/health` | 健康检查 |
| GET | `/ready` | 就绪检查 |
| GET | `/metrics` | Prometheus 指标 |
| GET | `/llm/providers` | LLM Provider 列表 |
| GET | `/migrations/status` | 迁移状态 |
| GET | `/admin/config` | 管理配置（admin） |
| PUT | `/admin/config` | 更新配置（admin） |

### 项目
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/projects` | 项目列表 |
| POST | `/projects` | 创建项目 |
| GET | `/projects/{id}` | 项目详情 |
| POST | `/projects/{id}/requirements` | 添加需求 |
| GET | `/projects/{id}/requirements` | 需求列表 |
| POST | `/projects/{id}/plan` | 生成 PRD |
| POST | `/projects/{id}/contracts/generate` | 生成合同 |
| GET | `/projects/{id}/contracts` | 合同列表 |

### 任务与执行
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/projects/{id}/tasks` | 任务列表 |
| POST | `/projects/{id}/tasks/dispatch` | 派发任务 |
| POST | `/projects/{id}/tasks/run` | 启动执行 |
| POST | `/projects/{id}/runs/parallel` | 并行执行 |
| GET | `/projects/{id}/runs/{runId}/status` | 执行状态 |

### HITL
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/projects/{id}/overrides` | 人工接管 |
| POST | `/projects/{id}/locks` | 代码锁定 |
| GET | `/projects/{id}/conflicts` | 冲突列表 |
| POST | `/projects/{id}/conflicts/{id}/resolve` | 解决冲突 |

### 可观测性
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/status/matrix` | 状态矩阵 |
| GET | `/status/stream` | SSE 状态流 |
| GET | `/status/panel` | 运维面板 |
| GET | `/projects/{id}/alerts` | 告警 |
| GET | `/projects/{id}/audit-logs` | 审计 |
| GET | `/projects/{id}/token-costs` | Token 成本 |

### 交付
| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/projects/{id}/delivery/export` | 导出交付包 |
| GET | `/projects/{id}/artifacts/{id}/download` | 下载产物 |

完整 API 规范参见 [docs/openapi.yaml](docs/openapi.yaml)。

## 测试

```bash
# 运行所有测试
go test ./...

# 运行 benchmark
go test -bench=. -benchmem ./internal/...

# 运行特定包测试
go test -v ./internal/httpapi/

# 带 Postgres 的测试
MULTI_AGENT_TEST_POSTGRES_DSN='postgres://...' go test ./internal/store/
```

### Benchmark 结果

| 操作 | 延迟 | 吞吐量 |
|------|------|--------|
| HTTP /health | 101μs | ~10K req/s |
| CreateProject | 1.3ms | ~800 req/s |
| ListProjects (100) | 12μs | ~80K req/s |
| StatusMatrix (10 项目) | 7.5μs | ~130K req/s |
| OpenAI Runner | 53μs | ~19K req/s |
| Gemini Runner | 46μs | ~22K req/s |
| Metrics Record | 265ns | ~3.8M ops/s |

## 项目结构

```
multiagentcom/
├── cmd/server/              # 入口
├── internal/
│   ├── auth/                # 认证（Token + OIDC + RBAC）
│   ├── agentruntime/        # Agent 运行时（Mock/HTTP/Container/Claude/OpenAI/Gemini）
│   ├── config/              # 配置加载与验证
│   ├── domain/              # 领域模型（18 个实体）
│   ├── httpapi/             # HTTP API + 中间件 + 指标
│   ├── service/             # 核心业务逻辑
│   └── store/               # 存储（Memory/File/Postgres + 迁移管理）
├── web/                     # Vue 3 前端
├── migrations/              # SQL 迁移文件
├── k8s/                     # Kubernetes 部署清单
├── scripts/                 # 脚本（demo/smoke/security）
└── docs/                    # 文档
```

## 文档

- [产品规格](docs/Spec.md)
- [工程需求](docs/Spec-Refined.md)
- [架构蓝图](docs/Architecture-Blueprint.md)
- [技术栈决策](docs/Tech-Stack-Decision.md)
- [MVP 计划](docs/MVP-Plan.md)
- [验收用例](docs/Acceptance-Cases.md)
- [实施 Backlog](docs/Implementation-Backlog.md)
- [发布检查](docs/Release-Checklist.md)
- [OpenAPI 规范](docs/openapi.yaml)
- [CHANGELOG](CHANGELOG.md)

## 开源协议

[MIT License](LICENSE)
