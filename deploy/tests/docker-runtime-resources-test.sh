#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

# 统一输出失败原因，方便从 CI 日志定位缺失资源。
fail() {
  printf 'docker runtime resources test failed: %s\n' "$1" >&2
  exit 1
}

# 去掉行首缩进后按完整行匹配，避免相似配置掩盖路径或参数错误。
# go:embed 指令写在 var 块里时带缩进。
assert_line() {
  file=$1
  line=$2
  sed 's/^[[:space:]]*//' "$file" | grep -Fqx "$line" || fail "$file is missing: $line"
}

test -s backend/internal/modelcatalog/model_supplements.json || \
  fail 'model supplements are missing or empty'
test -s backend/internal/modelcatalog/catalog.json.gz || \
  fail 'embedded models.dev catalog is missing or empty'
test -s backend/internal/modelcatalog/LICENSE.models.dev || \
  fail 'models.dev license is missing or empty'

# 官方模型补充与离线目录都由 Go 编译器嵌入。
assert_line backend/internal/modelcatalog/catalog.go '//go:embed model_supplements.json'
assert_line backend/internal/modelcatalog/catalog.go '//go:embed catalog.json.gz'

printf 'docker runtime resources test passed\n'
