# ── Stage 1: Frontend ──────────────────────────────────────
FROM node:22-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json* ./
RUN npm install --ignore-scripts
COPY web/ ./
RUN npx vite build

# ── Stage 2: Go backend ────────────────────────────────────
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /app/web/dist /app/web/dist
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/multiagentcom ./cmd/server

# ── Stage 3: Runtime ────────────────────────────────────────
FROM alpine:3.21
RUN apk add --no-cache ca-certificates git && \
    addgroup -S appgroup && adduser -S appuser -G appgroup && \
    mkdir -p /data/artifacts /data/sandboxes /data/state && \
    chown -R appuser:appgroup /data
COPY --from=backend-builder /bin/multiagentcom /usr/local/bin/multiagentcom
COPY --from=frontend-builder /app/web/dist /usr/local/share/multiagentcom/web

ENV MULTI_AGENT_WEB_ROOT=/usr/local/share/multiagentcom/web
USER appuser
EXPOSE 8080
ENTRYPOINT ["multiagentcom"]
