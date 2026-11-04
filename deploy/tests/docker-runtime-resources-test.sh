#!/bin/sh
set -eu

# docker-runtime-resources-test.sh checks the resources embedded in the
# application binary for model metadata. The model supplements, offline
# catalog, and license must exist, and the source must retain the expected
# embedding directives before image packaging.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

# fail reports a missing runtime resource and exits with a diagnostic for CI
# logs.
fail() {
  printf 'docker runtime resources test failed: %s\n' "$1" >&2
  exit 1
}

# assert_line checks a complete source line after removing leading
# indentation. Indented go:embed directives are accepted, while different
# paths or arguments still fail with a diagnostic.
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

# The binary embeds official model supplements and the offline catalog. Both
# source resources and their embedding directives must remain available at
# build time.
assert_line backend/internal/modelcatalog/catalog.go '//go:embed model_supplements.json'
assert_line backend/internal/modelcatalog/catalog.go '//go:embed catalog.json.gz'

printf 'docker runtime resources test passed\n'
