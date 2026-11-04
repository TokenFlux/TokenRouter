#!/usr/bin/env bash
# build_image.sh builds the complete application image from the repository
# root. It supplies the configured Go module proxy and checksum database to
# the shared Dockerfile, then tags the result for local deployment or testing.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

docker build -t ghcr.io/tokenflux/tokenrouter:latest \
    --build-arg GOPROXY=https://goproxy.cn,direct \
    --build-arg GOSUMDB=sum.golang.google.cn \
    -f "${REPO_ROOT}/Dockerfile" \
    "${REPO_ROOT}"
