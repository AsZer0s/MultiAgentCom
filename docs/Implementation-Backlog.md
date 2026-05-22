# MultiAgentCom 实施任务清单（Implementation Backlog v0.1）

> 本文将 `docs/MVP-Plan.md` 的里程碑进一步拆为可执行任务，便于分配、跟踪与验收。

## 1. 使用说明

- 任务 ID：`BL-XXX`
- 优先级：`P0`（必须）/`P1`（重要）/`P2`（优化）
- 估时单位：人日（PD）
- 每项任务包含：目标、依赖、交付标准（DoD）

## 2. 总体节奏

- Sprint 1（周 1-2）：最小闭环
- Sprint 2（周 3-4）：协同能力
- Sprint 3（周 5-6）：可控与恢复
- Sprint 4（周 7-8）：预览与交付

## 3. Sprint 1 Backlog（M1）

### BL-001 项目脚手架与基础服务
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 后端
- **目标：** 建立后端服务骨架、配置、日志、健康检查。
- **依赖：** 无
- **DoD：**
  - 可启动 API 服务。
  - 提供 `/health` 接口。
  - 统一日志格式与 requestId。

### BL-002 需求输入接口
- **优先级：** P0
- **估时：** 1 PD
- **负责人建议：** 后端
- **目标：** 实现需求提交与存储。
- **依赖：** BL-001
- **DoD：**
  - 提供 `POST /projects/{id}/requirements`。
  - 需求可持久化并可查询。

### BL-003 PRD 生成工作流（FR-01）
- **优先级：** P0
- **估时：** 2 PD
- **负责人建议：** 后端 + Agent
- **目标：** 基于输入需求生成结构化 PRD。
- **依赖：** BL-002
- **DoD：**
  - 生成内容包含目标/范围/约束/验收标准。
  - 结果可回写并版本化。

### BL-004 Task 状态机最小实现
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 后端
- **目标：** 支持 `CREATED -> IN_PROGRESS -> DONE/FAILED`。
- **依赖：** BL-001
- **DoD：**
  - 状态流转可审计。
  - 非法状态变更被拒绝。

### BL-005 单 Agent 执行器
- **优先级：** P0
- **估时：** 2 PD
- **负责人建议：** 后端 + Agent
- **目标：** 完成串行任务执行与结果回传。
- **依赖：** BL-003, BL-004
- **DoD：**
  - 可触发一次任务执行并输出结果。
  - 错误可回传任务系统。

### BL-006 Artifact 导出最小能力（FR-09）
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 后端
- **目标：** 产物打包与下载。
- **依赖：** BL-005
- **DoD：**
  - 可导出源码包和 README 模板。
  - 产物记录可追踪到 taskId。

### BL-007 Sprint 1 验收与回归
- **优先级：** P0
- **估时：** 1 PD
- **负责人建议：** QA
- **目标：** 覆盖 AC-01/02/14/15 的子集验证。
- **依赖：** Sprint 1 全部核心任务
- **DoD：**
  - 输出测试报告。
  - P0 缺陷清零或有明确豁免记录。

## 4. Sprint 2 Backlog（M2）

### BL-101 Contract Hub 最小版（FR-02）
- **优先级：** P0
- **估时：** 2 PD
- **负责人建议：** 后端
- **目标：** 契约创建、查询、版本管理。
- **依赖：** BL-003
- **DoD：**
  - 支持 `v1/v2` 版本化存储。
  - 提供契约读取 API。

### BL-102 契约一致性校验
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 后端
- **目标：** 合并前校验实现与契约冲突。
- **依赖：** BL-101
- **DoD：**
  - 冲突可阻止合并。
  - 返回结构化冲突详情。

### BL-103 并行调度（双 Agent）（FR-03）
- **优先级：** P0
- **估时：** 2.5 PD
- **负责人建议：** 后端
- **目标：** 任务 DAG + 并行执行。
- **依赖：** BL-004, BL-005
- **DoD：**
  - 支持至少 2 个 Agent 并发执行。
  - 失败任务可独立重试。

### BL-104 Context Engine 切片注入（FR-04）
- **优先级：** P1
- **估时：** 2 PD
- **负责人建议：** 后端 + Agent
- **目标：** 按角色精确注入上下文。
- **依赖：** BL-101, BL-103
- **DoD：**
  - 前后端 Agent 收到不同上下文。
  - 注入记录可查询版本与来源。

### BL-105 状态矩阵面板 v1（FR-08）
- **优先级：** P1
- **估时：** 2 PD
- **负责人建议：** 前端
- **目标：** 展示任务与 Agent 状态。
- **依赖：** BL-103
- **DoD：**
  - 可实时查看运行/阻塞/完成状态。
  - 支持按项目过滤。

### BL-106 Sprint 2 验收与回归
- **优先级：** P0
- **估时：** 1 PD
- **负责人建议：** QA
- **目标：** 覆盖 AC-03/04/05/06/12。
- **依赖：** Sprint 2 核心任务
- **DoD：**
  - 输出冲突校验测试与并行调度报告。

## 5. Sprint 3 Backlog（M3）

### BL-201 私有沙盒运行时（FR-05）
- **优先级：** P0
- **估时：** 2.5 PD
- **负责人建议：** 后端/平台
- **目标：** 每 Agent 独立容器执行环境。
- **依赖：** BL-103
- **DoD：**
  - Agent 崩溃隔离不影响主流程。
  - 沙盒资源可回收。

### BL-202 共享沙盒与合并闸门（FR-05）
- **优先级：** P0
- **估时：** 2 PD
- **负责人建议：** 后端/平台
- **目标：** 统一集成与测试入口。
- **依赖：** BL-201, BL-102
- **DoD：**
  - 合并前执行契约和测试校验。
  - 失败能阻断进入主交付链路。

### BL-203 Timeline 快照与回滚（FR-07）
- **优先级：** P0
- **估时：** 2.5 PD
- **负责人建议：** 后端
- **目标：** 自动快照、失败回滚、新分支生成。
- **依赖：** BL-202
- **DoD：**
  - 可回滚到最近稳定快照。
  - 回滚后保留原时间线。

### BL-204 HITL 指令注入（FR-06）
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 后端 + 前端
- **目标：** 人工高优指令接管。
- **依赖：** BL-103
- **DoD：**
  - 任意进行中任务可进入 `HUMAN_OVERRIDE`。
  - 指令在安全检查点生效。

### BL-205 代码锁定机制（FR-06）
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 后端
- **目标：** `LOCKED BY HUMAN` 不可覆盖策略。
- **依赖：** BL-204
- **DoD：**
  - 自动流程检测锁定标记并跳过覆盖。
  - 记录锁定冲突日志。

### BL-206 Sprint 3 验收与回归
- **优先级：** P0
- **估时：** 1 PD
- **负责人建议：** QA
- **目标：** 覆盖 AC-07/08/09/10/11。
- **依赖：** Sprint 3 核心任务
- **DoD：**
  - 输出回滚与接管专项报告。

## 6. Sprint 4 Backlog（M4）

### BL-301 预览服务（FR-09）
- **优先级：** P0
- **估时：** 2 PD
- **负责人建议：** 前端 + 后端
- **目标：** 一键拉起可验收预览环境。
- **依赖：** BL-202
- **DoD：**
  - 可访问预览 URL。
  - 基础热更新可用（MVP 级）。

### BL-302 交付引擎（FR-09）
- **优先级：** P0
- **估时：** 2 PD
- **负责人建议：** 后端
- **目标：** 标准化生成依赖与部署文件。
- **依赖：** BL-301
- **DoD：**
  - 产物包含 `go.mod`/`package.json`、`docker-compose.yml`、`README.md`。

### BL-303 拓扑与通信日志可视化（FR-08）
- **优先级：** P1
- **估时：** 2 PD
- **负责人建议：** 前端
- **目标：** 展示依赖链路与通信流。
- **依赖：** BL-105
- **DoD：**
  - 支持按 taskId 查询通信日志。
  - 支持链路节点高亮。

### BL-304 Token 与成本监控基础版（FR-08）
- **优先级：** P1
- **估时：** 1.5 PD
- **负责人建议：** 后端 + 前端
- **目标：** 输出基础成本趋势图。
- **依赖：** BL-303
- **DoD：**
  - 可展示按任务的 Token 消耗与趋势。

### BL-305 E2E 演示脚本与发布检查
- **优先级：** P0
- **估时：** 1.5 PD
- **负责人建议：** 产品 + QA
- **目标：** 形成稳定可重复演示流程。
- **依赖：** BL-301, BL-302
- **DoD：**
  - 10 分钟演示脚本可重复执行 3 次。
  - 发布清单完整并通过检查。

### BL-306 Sprint 4 验收与回归
- **优先级：** P0
- **估时：** 1 PD
- **负责人建议：** QA
- **目标：** 覆盖 AC-12/13/14/15 全量。
- **依赖：** Sprint 4 核心任务
- **DoD：**
  - P0 用例全通过，P1 通过率 >= 80%。

## 7. 跨 Sprint 公共任务

### BL-901 CI 基线
- **优先级：** P0
- **估时：** 1 PD
- **目标：** 建立 lint/test/build 自动化流水线。
- **DoD：** PR 合并前自动校验可用。

### BL-902 观测与告警基线
- **优先级：** P1
- **估时：** 1 PD
- **目标：** 错误率、失败率、回滚事件告警。
- **DoD：** 关键失败场景可主动通知。

### BL-903 安全基线检查
- **优先级：** P0
- **估时：** 1 PD
- **目标：** 密钥扫描、最小权限、审计日志。
- **DoD：** 无明文密钥；关键操作可追踪。

### BL-904 文档同步维护
- **优先级：** P0
- **估时：** 每 Sprint 0.5 PD
- **目标：** 保持文档与实现一致。
- **DoD：** 每次里程碑后更新 Spec/Plan/Case。

### BL-905 Git workspace 清理生命周期
- **状态：** Done
- **优先级：** P1
- **估时：** 1 PD
- **目标：** 减少 local Git provider 长期运行后的 worktree/branch 污染。
- **DoD：** 已合并私有 worktree 可安全清理，cleanup API 支持 dry-run；分支删除保持显式 opt-in 且不使用 force。
- **完成范围：** 已提供 `POST /projects/{id}/workspaces/cleanup`，支持 dry-run、PRIVATE/SHARED scope、已合并私有 worktree 清理、分支删除显式 opt-in，HTTP 覆盖 dry-run 与安全保留行为。

### BL-906 Remote Git 最小闭环
- **状态：** Done
- **优先级：** P1
- **估时：** 1.5 PD
- **目标：** 将 Git workspace 从 local-only 扩展到 remote-backed MVP。
- **DoD：** 支持 remote clone、fetch-before-use、private/shared branch 非 force push；token 不写入 remote URL 且错误脱敏；force push/remote 删除保持 out of scope。
- **完成范围：** Git provider 已支持 remote URL clone、fetch-before-use、origin/main base、private/shared branch 非 force push、token file/auth username 配置与 remote URL credential 校验。

### BL-907 Git rebase 安全最小闭环
- **状态：** Done
- **优先级：** P1
- **估时：** 1.5 PD
- **目标：** 为已生成的受管 private task workspace 提供显式、可审计的 rebase 能力。
- **DoD：** rebase API 要求显式 `targetRef` 与 sandbox 选择；支持 dry-run ahead/behind；dirty content worktree 被拒绝；冲突自动 abort 且保留原 head；可选 publish 仅使用非 force push。
- **完成范围：** 已提供 `POST /projects/{id}/workspaces/rebase`，要求显式 `targetRef` 与 `sandboxIds` 或 `all=true`，支持 dry-run、dirty worktree 拒绝、冲突 abort、受管 private workspace 限定与非 force publish。

### BL-908 Pure Git snapshot restore
- **状态：** Done
- **优先级：** P1
- **估时：** 1.5 PD
- **目标：** 将 Git workspace snapshot 从“仅记录 ref”扩展为可安全恢复的 rollback worktree。
- **DoD：** rollback 到 Git snapshot 会创建新的受管 shared rollback worktree；checksum/ref mismatch 被拒绝；不覆盖已有 worktree；后续 shared merge 以 restored head 为 base；service/HTTP 测试覆盖成功与安全失败。
- **完成范围：** Git workspace snapshot rollback 已创建受管 shared rollback worktree/branch，校验 `workspaceStateRef` 与 `workspaceChecksum`，不 reset 原 shared worktree，rollback 后续 shared merge 使用 restored head 作为 base。

### BL-909 Go AST lock hardening
- **状态：** Done
- **优先级：** P1
- **估时：** 1 PD
- **目标：** 补强 Go symbol lock 的 marker 归属校验与缺失目标文件创建能力。
- **DoD：** `LOCKED BY HUMAN` 必须位于被锁定 symbol/doc comment 内；method 支持 `Receiver.Method` 精确区分同名 receiver；grouped `type/var/const` 只替换命中的目标 spec；缺失 Go 文件可由 locked content 的 package/import 上下文创建；service/HTTP 测试覆盖 marker 拒绝、doc comment 保留、receiver 区分、grouped spec 精确替换和缺失文件创建。
- **完成范围：** Go symbol lock 已支持 marker 归属校验、receiver-qualified method、grouped declaration spec 精确替换、缺失 Go 文件创建、import reconciliation 与 HTTP/service 覆盖。

### BL-910 HITL lock lease and conflict queue
- **状态：** Done
- **优先级：** P1
- **估时：** 1.5 PD
- **目标：** 将 HITL override / code lock 升级为带 owner lease 的本地治理流程。
- **DoD：** override/lock 支持 `owner` 与 `ttlSeconds`；同 scope 未过期 lease owner 不同时进入 `OPEN` conflict queue；conflict 可查询与 resolve；file-store 持久化与 audit 覆盖通过。
- **完成范围：** override/code lock 已支持 owner/TTL lease、冲突入队、`GET /projects/{id}/conflicts`、resolve API、file-store 持久化与 `HITL_CONFLICT_RESOLVED` audit。

### BL-911 Container sandbox isolation MVP
- **状态：** Done
- **优先级：** P1
- **估时：** 1.5 PD
- **目标：** 为 runtime provider 增加容器级隔离的最小闭环。
- **DoD：** `MULTI_AGENT_RUNTIME_PROVIDER=container` 可注册并执行 container runtime；支持 binary/image/network/user/read-only/workdir 配置；只挂载 workspace，默认 `network=none` 与 `read-only=true`；通过 stdin 传入结构化 sandbox/workspace 元数据；readiness 校验 binary 可执行但不 pull image、不启动容器；service/HTTP/unit 测试覆盖成功、timeout、stderr failure 与缺失 binary。
- **完成范围：** container runtime 已支持注册执行、workspace-only mount、stdin payload、binary/image/network/user/read-only/workdir 配置、资源限制、tmpfs writable paths、entrypoint/command、轻量 readiness 与 opt-in 真实 smoke。

### BL-912 HITL conflict dashboard
- **状态：** Done
- **优先级：** P1
- **估时：** 1 PD
- **目标：** 将 HITL conflict queue 暴露到状态面板。
- **DoD：** `/status/panel` 增加 HITL Conflicts 面板；通过 `/projects/{id}/conflicts` 获取数据；OPEN/RESOLVED 条目以不同样式渲染；空态与 project-less 状态有明确提示；HTTP panel smoke 覆盖通过。
- **完成范围：** 状态面板已展示 HITL Conflicts、区分 OPEN/RESOLVED、支持空态/project-less 提示，并可直接 resolve open conflict 后刷新队列。

### BL-913 File-store durability and typed state seam
- **状态：** Done
- **优先级：** P1
- **估时：** 1 PD
- **目标：** 将 file-store 从 raw blob 调用深化为 typed service-state 边界，并补强重启恢复与状态完整性检查。
- **DoD：** Service 通过 typed state-store seam 读写持久化状态；FileStore 保存采用 temp-file/fsync/rename 并同步目录；`/ready` 校验 file-store `service-state.json` JSON 与版本；release check 覆盖 file-store restart smoke；Postgres 保持后续目标而不暴露未完成 provider。
- **完成范围：** 已新增 service typed state-store 适配层、file-store state path helper 与目录 fsync；`/ready` 增加 `fileStoreState` 检查并拒绝损坏/不支持版本；release check 新增 file-store 重启烟测；README/Release Checklist 已同步 Postgres 前置边界说明。

### BL-914 Postgres store provider MVP
- **状态：** Done
- **优先级：** P1
- **估时：** 1.5 PD
- **目标：** 将 typed service-state 持久化扩展到真实 Postgres provider。
- **DoD：** `MULTI_AGENT_STORE_PROVIDER=postgres` 与 `MULTI_AGENT_POSTGRES_DSN` 可启用 JSONB-backed store；`/ready` 校验 Postgres 连接、schema 与状态版本；service restart 可恢复项目/需求/任务状态；release check 提供 opt-in Postgres smoke；默认 CI 不依赖 Postgres。
- **完成范围：** 已新增 Postgres raw store、`service_state` JSONB schema ensure、provider 配置校验、service wiring、`postgresStore` readiness、guarded integration tests 与 opt-in release smoke；关系化 schema / migrations / 多实例并发治理保持后续目标。

## 8. 依赖总览（关键路径）

- 关键路径建议：
  - BL-001 -> BL-002 -> BL-003 -> BL-004 -> BL-005 -> BL-101 -> BL-102 -> BL-103 -> BL-201 -> BL-202 -> BL-203 -> BL-301 -> BL-302 -> BL-305

## 9. 资源与产能估算（建议）

- 后端：约 26-30 PD
- 前端：约 10-12 PD
- QA：约 6-8 PD
- 产品/架构：约 4-6 PD
- 总计：约 46-56 PD（含公共任务）

## 10. 风险缓冲建议

- 每个 Sprint 预留 15% 缓冲用于缺陷和返工。
- P0 任务不得在 Sprint 末 2 天才开始联调。
- 任一关键链路任务延期超过 1 天时，触发范围收敛机制（降级 P1/P2）。

