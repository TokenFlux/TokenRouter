# syntax=docker/dockerfile:1.7
# Dockerfile builds the TokenRouter runtime image from source. The Node stage
# produces frontend assets for the Go stage to embed in the server binary. The
# runtime stage copies that binary and PostgreSQL client tools for database
# backup and restore.

ARG NODE_IMAGE=node:26.12.1-alpine
ARG GOLANG_IMAGE=golang:1.27.4-alpine
ARG ALPINE_IMAGE=alpine:3.21
ARG POSTGRES_IMAGE=postgres:18-alpine
ARG GOPROXY=https://goproxy.cn,direct
ARG GOSUMDB=sum.golang.google.cn
ARG NPM_CONFIG_REGISTRY=

# -----------------------------------------------------------------------------
# Stage 1: Frontend Builder
# -----------------------------------------------------------------------------
# 前端产物是与架构无关的 JS，固定在构建平台执行以避免目标架构的 QEMU 模拟。
FROM --platform=${BUILDPLATFORM} ${NODE_IMAGE} AS frontend-builder
ARG NPM_CONFIG_REGISTRY

WORKDIR /app/frontend

# Node 版本需要与 .node-version 一致，镜像覆盖值同样检查；pnpm 版本取 package.json 的 packageManager。
COPY .node-version /app/
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
# The linked SDK supplies shared source contracts and runtime values to the host build.
COPY plugin-sdk/web/package.json /app/plugin-sdk/web/
COPY plugin-sdk/web/src/ /app/plugin-sdk/web/src/
RUN test "$(node -p 'process.versions.node')" = "$(cat /app/.node-version)" && \
    npm install --global "$(node -p 'require("./package.json").packageManager')"

# 源码变化时复用已安装的依赖层。
RUN --mount=type=cache,id=tokenrouter-pnpm-store,target=/root/.local/share/pnpm/store \
    if [ -n "${NPM_CONFIG_REGISTRY}" ]; then pnpm config set registry "${NPM_CONFIG_REGISTRY}"; fi && \
    pnpm install --frozen-lockfile --prefer-offline

# 复制前端源码、法律文档和共享语言资源后构建。
COPY frontend/ ./
COPY docs/legal/ /app/docs/legal/
COPY backend/internal/pkg/locale/*.json /app/backend/internal/pkg/locale/
RUN pnpm run build

# -----------------------------------------------------------------------------
# Stage 2: Backend Builder
# -----------------------------------------------------------------------------
# Go 工具链固定在构建平台运行，并在下方交叉编译到目标架构。
# 由于关闭了 CGO，下载模块与编译过程都无需通过 QEMU 执行。
FROM --platform=${BUILDPLATFORM} ${GOLANG_IMAGE} AS backend-builder

# Build arguments for version info (set by CI)
ARG VERSION=
ARG COMMIT=docker
ARG DATE
ARG GOPROXY
ARG GOSUMDB
# buildx 根据 --platform 指定的目标自动注入，例如 linux/amd64。
ARG TARGETOS
ARG TARGETARCH

ENV GOPROXY=${GOPROXY}
ENV GOSUMDB=${GOSUMDB}

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /app/backend

# Copy go mod files first (better caching)
COPY backend/go.mod backend/go.sum ./
# The backend resolves the MIT SDK through a local module replacement.
COPY plugin-sdk/go /app/plugin-sdk/go
# 跨构建保留模块缓存，重试临时下载故障时无需重新获取全部依赖。
RUN --mount=type=cache,id=tokenrouter-gomod,target=/go/pkg/mod \
    go mod download

# 先复制后端源码
COPY backend/ ./

# 再复制前端构建产物，避免被后端源码覆盖
COPY --from=frontend-builder /app/backend/internal/web/dist ./internal/web/dist

# 构建 release 二进制，并嵌入前端资源
# 版本优先级：构建参数 VERSION > 精确 Git tag > cmd/server/VERSION
RUN --mount=type=cache,id=tokenrouter-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=tokenrouter-gobuild,target=/root/.cache/go-build \
    VERSION_VALUE="${VERSION}" && \
    if [ -z "${VERSION_VALUE}" ]; then VERSION_VALUE="$(./scripts/resolve-version.sh)"; fi && \
    DATE_VALUE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build \
    -tags embed \
    -ldflags="-s -w -X main.Version=${VERSION_VALUE} -X main.Commit=${COMMIT} -X main.Date=${DATE_VALUE} -X main.BuildType=release" \
    -trimpath \
    -o /app/tokenrouter \
    ./cmd/server

# -----------------------------------------------------------------------------
# Stage 3: PostgreSQL Client (version-matched with docker-compose)
# -----------------------------------------------------------------------------
FROM ${POSTGRES_IMAGE} AS pg-client

# -----------------------------------------------------------------------------
# Stage 4: Final Runtime Image
# -----------------------------------------------------------------------------
FROM ${ALPINE_IMAGE}

# Labels
LABEL maintainer="Wei-Shaw <github.com/Wei-Shaw>"
LABEL description="TokenRouter - AI API Gateway Platform"
LABEL org.opencontainers.image.source="https://github.com/TokenFlux/TokenRouter"

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    su-exec \
    libpq \
    zstd-libs \
    lz4-libs \
    krb5-libs \
    libldap \
    libedit \
    && rm -rf /var/cache/apk/*

# Copy pg_dump and psql from the same postgres image used in docker-compose
# This ensures version consistency between backup tools and the database server
COPY --from=pg-client /usr/local/bin/pg_dump /usr/local/bin/pg_dump
COPY --from=pg-client /usr/local/bin/psql /usr/local/bin/psql
COPY --from=pg-client /usr/local/lib/libpq.so.5* /usr/local/lib/

# Create non-root user
RUN addgroup -g 1000 tokenrouter && \
    adduser -u 1000 -G tokenrouter -s /bin/sh -D tokenrouter

# Set working directory
WORKDIR /app

# 复制已内嵌默认价格补充的二进制，并设置运行用户。
COPY --from=backend-builder --chown=tokenrouter:tokenrouter /app/tokenrouter /app/tokenrouter

# Create data directory
RUN mkdir -p /app/data && chown tokenrouter:tokenrouter /app/data

# Copy entrypoint script (fixes volume permissions then drops to tokenrouter)
COPY deploy/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# Expose port (can be overridden by SERVER_PORT env var)
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD wget -q -T 5 -O /dev/null http://localhost:${SERVER_PORT:-8080}/health || exit 1

# Run the application (entrypoint fixes /app/data ownership then execs as tokenrouter)
ENTRYPOINT ["/app/docker-entrypoint.sh"]
# 旧 Compose 可继续调用原可执行路径。
RUN ln -s /app/tokenrouter /app/sub2api
CMD ["/app/tokenrouter"]
