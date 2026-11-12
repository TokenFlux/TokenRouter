FROM golang:1.27.4-alpine

# Dockerfile builds the standalone backend using the repository root as its context.
# The backend imports the local plugin SDK module, so dependency resolution requires
# both source trees before compilation can resolve the module replacement.
WORKDIR /app/backend

# Git is required by version resolution in source builds.
RUN apk add --no-cache git

# Resolve dependencies after making the local SDK replacement available.
COPY backend/go.mod backend/go.sum ./
COPY plugin-sdk/go /app/plugin-sdk/go
RUN go mod download

COPY backend/ ./

# Preserve the repository's version resolution for the standalone executable.
RUN VERSION_VALUE="$(./scripts/resolve-version.sh)" && \
    go build -ldflags="-s -w -X main.Version=${VERSION_VALUE}" -o main ./cmd/server/

EXPOSE 8080
CMD ["./main"]
