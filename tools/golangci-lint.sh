#!/usr/bin/env bash
set -euo pipefail

# 本地入口与 CI 共用版本文件，避免内置格式化规则随机器漂移。
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
expected_version="$(cat "$repo_root/.golangci-version")"
if ! command -v golangci-lint >/dev/null 2>&1; then
  printf '未安装 golangci-lint，请安装 %s 并加入 PATH。\n' "$expected_version" >&2
  exit 1
fi
actual_version="$(golangci-lint version --short)"
if [[ "${actual_version#v}" != "${expected_version#v}" ]]; then
  printf 'golangci-lint 版本不符：需要 %s，当前为 %s。\n' "$expected_version" "$actual_version" >&2
  exit 1
fi

exec golangci-lint "$@"
