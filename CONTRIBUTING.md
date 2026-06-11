# Contributing to MultiAgentCom

感谢你对 MultiAgentCom 的关注！以下是参与贡献的指南。

## 开发环境

### 前置要求

- Go 1.22+
- Node.js 18+（前端开发）
- PostgreSQL 15+（可选，用于集成测试）
- Docker（可选，用于容器 runtime 测试）

### 本地设置

```bash
# 克隆仓库
git clone https://github.com/user/multiagentcom.git
cd multiagentcom

# 安装前端依赖
cd web && npm install && cd ..

# 运行测试
go test ./...

# 启动开发服务器
go run ./cmd/server
```

## 代码规范

### Go

- 使用 `gofmt` 格式化代码
- 使用 `go vet` 检查代码
- 遵循 [Effective Go](https://go.dev/doc/effective_go) 指南
- 所有公开函数必须有文档注释
- 错误处理不能忽略

### TypeScript/Vue

- 使用 TypeScript strict 模式
- 使用 Composition API（`<script setup>`）
- 组件名使用 PascalCase

## 提交规范

使用 [Conventional Commits](https://www.conventionalcommits.org/) 格式：

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

类型：
- `feat`: 新功能
- `fix`: 修复
- `docs`: 文档
- `test`: 测试
- `refactor`: 重构
- `perf`: 性能优化
- `chore`: 构建/工具变更

示例：
```
feat(runtime): add Gemini API support
fix(auth): correct OIDC JWT signature verification
test(httpapi): add benchmark for health endpoint
```

## Pull Request 流程

1. Fork 仓库
2. 创建特性分支：`git checkout -b feature/my-feature`
3. 提交更改：`git commit -m "feat: add my feature"`
4. 推送分支：`git push origin feature/my-feature`
5. 创建 Pull Request

### PR 要求

- 所有测试通过：`go test ./...`
- 无 vet 警告：`go vet ./...`
- 前端构建成功：`cd web && npm run build`
- 新功能必须有测试覆盖
- 更新相关文档

## 报告问题

使用 GitHub Issues 报告 bug 或请求功能。

### Bug 报告模板

```markdown
**描述**
简要描述问题。

**复现步骤**
1. 执行 '...'
2. 点击 '...'
3. 看到错误

**期望行为**
描述期望的行为。

**实际行为**
描述实际的行为。

**环境**
- OS: [e.g. macOS 14]
- Go: [e.g. 1.22]
- Browser: [e.g. Chrome 120]
```

## 安全问题

如发现安全漏洞，请通过邮件私下报告，不要创建公开 Issue。

## 许可证

贡献代码将使用与项目相同的 [MIT License](LICENSE)。
