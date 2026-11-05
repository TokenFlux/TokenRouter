#!/bin/bash

set -euo pipefail

# apple-container-test.sh checks Apple Container lifecycle commands with
# isolated command fixtures. It verifies environment-file permissions, startup
# and teardown, ownership rejection, and legacy volume reuse without creating
# real containers or changing a running deployment.

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
SCRIPT="${DEPLOY_DIR}/apple-container.sh"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/tokenrouter-apple-test.XXXXXX")"
STATE_DIR="${TEST_ROOT}/state"
ENV_FILE="${TEST_ROOT}/tokenrouter.env"

cleanup() {
    rm -rf "${TEST_ROOT}"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

assert_exists() {
    [[ -e "$1" ]] || fail "Expected path to exist: $1"
}

assert_missing() {
    [[ ! -e "$1" ]] || fail "Expected path to be absent: $1"
}

export FAKE_CONTAINER_STATE="${STATE_DIR}"
export PATH="${TEST_DIR}/fixtures/bin:${PATH}"
export TOKENROUTER_ENV_FILE="${ENV_FILE}"

mkdir -p "${STATE_DIR}"

"${SCRIPT}" init
[[ "$(stat -f '%Lp' "${ENV_FILE}")" == "600" ]] || fail "init did not create a mode-600 env file"
grep -q '^POSTGRES_PASSWORD=change_this_secure_password$' "${ENV_FILE}" && fail "init retained the placeholder password"

chmod 644 "${ENV_FILE}"
if "${SCRIPT}" up >/dev/null 2>&1; then
    fail "up accepted an insecure env file"
fi
chmod 600 "${ENV_FILE}"

# Deliberately retain one retired scalar and one reference-only secret in the
# deployment file; application environment construction must distinguish them.
printf 'GATEWAY_MAX_BODY_SIZE=1234\nUPDATE_GITHUB_TOKEN=fixture-reference-token\n' >>"${ENV_FILE}"
"${SCRIPT}" up
assert_exists "${STATE_DIR}/containers/tokenrouter-apple"
assert_exists "${STATE_DIR}/containers/tokenrouter-apple-postgres"
assert_exists "${STATE_DIR}/containers/tokenrouter-apple-redis"
assert_exists "${STATE_DIR}/running/tokenrouter-apple"
grep -q '^GATEWAY_MAX_BODY_SIZE=' "${STATE_DIR}/tokenrouter-apple.env" && fail "retired scalar reached application environment"
grep -q '^UPDATE_GITHUB_TOKEN=fixture-reference-token$' "${STATE_DIR}/tokenrouter-apple.env" || fail "explicit-reference secret was dropped"
grep -q '^DATABASE_PASSWORD=' "${STATE_DIR}/tokenrouter-apple.env" || fail "setup password was dropped"
grep -q '^POSTGRES_PASSWORD=' "${STATE_DIR}/tokenrouter-apple.env" && fail "database service variable reached application environment"
grep -Fq 'ghcr.io/tokenflux/tokenrouter:latest' "${STATE_DIR}/commands.log" || fail "up did not use the TokenRouter application image"
"${SCRIPT}" status >/dev/null

"${SCRIPT}" up --recreate
assert_exists "${STATE_DIR}/running/tokenrouter-apple"
"${SCRIPT}" down
assert_missing "${STATE_DIR}/running/tokenrouter-apple"
assert_missing "${STATE_DIR}/running/tokenrouter-apple-postgres"
assert_missing "${STATE_DIR}/running/tokenrouter-apple-redis"

"${SCRIPT}" destroy --yes
assert_missing "${STATE_DIR}/containers/tokenrouter-apple"
assert_missing "${STATE_DIR}/networks/tokenrouter-apple"
assert_exists "${STATE_DIR}/volumes/tokenrouter-apple-data"

"${SCRIPT}" up
"${SCRIPT}" destroy --volumes --yes
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-data"
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-postgres-data"
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-redis-data"

touch "${STATE_DIR}/system-running"
touch "${STATE_DIR}/containers/tokenrouter-apple"
touch "${STATE_DIR}/unowned/container/tokenrouter-apple"
if "${SCRIPT}" status >/dev/null 2>&1; then
    fail "status accepted an unowned same-name container"
fi

# Legacy stacks retain their original volumes and ownership labels. The
# previous environment variable must also continue selecting the deployment
# environment file.
rm -f "${STATE_DIR}/containers/tokenrouter-apple" "${STATE_DIR}/unowned/container/tokenrouter-apple"
touch "${STATE_DIR}/volumes/sub2api-apple-data" "${STATE_DIR}/volumes/sub2api-apple-postgres-data" "${STATE_DIR}/volumes/sub2api-apple-redis-data"
TOKENROUTER_ENV_FILE= SUB2API_ENV_FILE="${ENV_FILE}" "${SCRIPT}" up
assert_exists "${STATE_DIR}/containers/sub2api-apple"
"${SCRIPT}" logs app >/dev/null
assert_exists "${STATE_DIR}/volumes/sub2api-apple-data"
assert_missing "${STATE_DIR}/volumes/tokenrouter-apple-data"
touch "${STATE_DIR}/volumes/tokenrouter-apple-data"
if "${SCRIPT}" up >/dev/null 2>&1; then fail "up accepted mixed old and new resources"; fi
rm -f "${STATE_DIR}/volumes/tokenrouter-apple-data"
"${SCRIPT}" destroy --volumes --yes

printf 'Apple container lifecycle tests passed.\n'
