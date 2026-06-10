# Changelog

All notable changes to MultiAgentCom will be documented in this file.

## [1.0.0] - 2026-06-08

### Added

#### Phase 1 - AI + Docker + Postgres
- Claude API runtime provider (Anthropic Messages API)
- OpenAI-compatible runtime provider (Chat Completions + legacy Codex Completions)
- Google Gemini runtime provider (generateContent API)
- Dockerfile multi-stage build (Node frontend + Go backend + Alpine runtime)
- docker-compose.yml with app + Postgres services
- Postgres auto-migration on first startup (4 versions, 20 projection tables)
- Extended repository for 10 entities (context, sandbox, snapshot, audit, alert, override, lock, conflict, preview, communication)

#### Phase 2 - Frontend + Concurrency + Auth
- Vue 3 SPA with 7 views (Dashboard, Projects, Detail, Task Board, HITL, Settings, Login)
- Static file serving with SPA fallback
- Postgres advisory locks wired into 13 core write methods
- Idempotency key protection on 7 POST handlers
- OIDC callback with authorization code exchange
- RS256 JWT signature verification with JWKS key lookup
- RBAC policy with project-level role management
- Admin configuration UI (Runtime, Token Cost, S3, Alerts, OIDC)

#### Phase 3 - Production
- CORS middleware with configurable allowed origins
- Rate limiting middleware (100 req/min per IP)
- Security headers (HSTS, X-Frame-Options, X-XSS-Protection)
- CSRF protection for state-changing endpoints
- S3/MinIO artifact storage with AWS SigV4 signing
- External migration manager (up/down/status)
- Prometheus /metrics endpoint (HTTP, business, LLM metrics)
- Postgres connection pool configuration
- Input length validation on all domain objects
- Sandbox cleanup on graceful shutdown
- OpenAPI 3.0 specification
- Auth test coverage (15 tests)

### Security
- OIDC ID token RS256 signature verification
- CORS restricted to configured origins
- Error messages sanitized (no internal details leaked)
- Gemini API key moved from URL to header
- Token query string marked as deprecated
- Input length limits on all user-facing fields

## [0.1.0] - 2026-05-01

### Added
- Initial MVP with Sprint 1-4 features
- Project lifecycle: requirements → PRD → contracts → tasks → delivery
- 43 HTTP API endpoints
- Three storage backends (memory, file, Postgres)
- Three runtime providers (mock, HTTP, container)
- Git workspace provider with worktrees
- HITL override and code locking
- Observability dashboard with SSE streaming
