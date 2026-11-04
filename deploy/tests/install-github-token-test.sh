#!/bin/bash

set -euo pipefail

# install-github-token-test.sh checks how the installer sends authenticated
# GitHub API requests using a local curl substitute. It verifies token
# isolation, ignored curl configuration, rejected extra destinations, and
# anonymous release asset downloads without contacting GitHub.

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
TEMP_DIR=$(mktemp -d)
trap 'rm -rf "$TEMP_DIR"' EXIT

cat > "$TEMP_DIR/curl" <<'EOF'
#!/bin/bash
printf '%s\n' "$@" > "$CURL_ARGS_LOG"
env > "${CURL_ARGS_LOG}.env"
cat > "${CURL_ARGS_LOG}.stdin"
EOF
chmod +x "$TEMP_DIR/curl"

mkdir "$TEMP_DIR/home"
cat > "$TEMP_DIR/home/.curlrc" <<'EOF'
url = "https://example.com/collect"
header = "X-Leaked-From-Curlrc: yes"
EOF

# Extract only the API helper so the fixture never runs installer main or
# loads unrelated Bash 4-only declarations.
sed -n '/^github_api_curl() {$/,/^}$/p' "$ROOT_DIR/deploy/install.sh" > "$TEMP_DIR/install-functions.sh"

run_api_curl() {
    CURL_ARGS_LOG="$1" HOME="$TEMP_DIR/home" PATH="$TEMP_DIR:$PATH" UPDATE_GITHUB_TOKEN="${2:-}" \
        GITHUB_TOKEN="github-fallback" GH_TOKEN="gh-fallback" \
        bash -c 'source "$1"; github_api_curl -s "$2"' bash \
        "$TEMP_DIR/install-functions.sh" "https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest"
}

run_api_curl "$TEMP_DIR/authenticated" "update-secret"
test "$(head -n 1 "$TEMP_DIR/authenticated")" = '-q'
grep -Fxq -- '--config' "$TEMP_DIR/authenticated"
grep -Fxq -- '-' "$TEMP_DIR/authenticated"
grep -Fxq -- '--globoff' "$TEMP_DIR/authenticated"
grep -Fxq 'header = "Authorization: Bearer update-secret"' "$TEMP_DIR/authenticated.stdin"
if grep -Fq 'update-secret' "$TEMP_DIR/authenticated"; then
    echo "installer exposed the update token in curl argv" >&2
    exit 1
fi
if grep -Eq 'update-secret|github-fallback|gh-fallback' "$TEMP_DIR/authenticated.env"; then
    echo "installer exposed a token in curl environment" >&2
    exit 1
fi
test "$(grep -Fxc 'https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest' "$TEMP_DIR/authenticated")" -eq 1
if grep -Fq 'example.com/collect' "$TEMP_DIR/authenticated" || grep -Fq 'X-Leaked-From-Curlrc' "$TEMP_DIR/authenticated" ||
    grep -Fq 'example.com/collect' "$TEMP_DIR/authenticated.stdin" || grep -Fq 'X-Leaked-From-Curlrc' "$TEMP_DIR/authenticated.stdin"; then
    echo "installer allowed hostile curl config into authenticated invocation" >&2
    exit 1
fi

run_api_curl "$TEMP_DIR/anonymous"
test "$(head -n 1 "$TEMP_DIR/anonymous")" = '-q'
if grep -Eq 'github-fallback|gh-fallback' "$TEMP_DIR/anonymous.env"; then
    echo "installer exposed a fallback token in anonymous curl environment" >&2
    exit 1
fi
if grep -Fq 'Authorization:' "$TEMP_DIR/anonymous"; then
    echo "installer unexpectedly used a fallback token" >&2
    exit 1
fi
test ! -s "$TEMP_DIR/anonymous.stdin"
test "$(grep -Fxc 'https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest' "$TEMP_DIR/anonymous")" -eq 1
if grep -Fq 'example.com/collect' "$TEMP_DIR/anonymous" || grep -Fq 'X-Leaked-From-Curlrc' "$TEMP_DIR/anonymous"; then
    echo "installer allowed hostile curl config into anonymous invocation" >&2
    exit 1
fi

assert_unsafe_invocation_rejected() {
    local name=$1
    shift
    rm -f "$TEMP_DIR/$name" "$TEMP_DIR/$name.stdin"
    if CURL_ARGS_LOG="$TEMP_DIR/$name" PATH="$TEMP_DIR:$PATH" UPDATE_GITHUB_TOKEN="update-secret" \
        bash -c 'source "$1"; shift; github_api_curl "$@"' bash \
        "$TEMP_DIR/install-functions.sh" "$@" 2>/dev/null; then
        echo "installer accepted unsafe curl invocation: $name" >&2
        exit 1
    fi
    if [ -e "$TEMP_DIR/$name" ]; then
        echo "installer invoked curl for unsafe request: $name" >&2
        exit 1
    fi
}

assert_unsafe_invocation_rejected non-api -s \
    "https://github.com/TokenFlux/TokenRouter/releases/download/v1/asset"
assert_unsafe_invocation_rejected mixed-host -s \
    "https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest" \
    "https://example.com/collect"
assert_unsafe_invocation_rejected multiple-api -s \
    "https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest" \
    "https://api.github.com/repos/TokenFlux/TokenRouter/releases"
assert_unsafe_invocation_rejected url-option -s --url \
    "https://example.com/collect" \
    "https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest"

# Every installer Releases API request must use the scoped helper. Count its
# call sites to catch new requests that bypass credential restrictions.
test "$(grep -c 'github_api_curl .*https://api.github.com/' "$ROOT_DIR/deploy/install.sh")" -eq 3

# Release assets and checksums use anonymous downloads. Keep those requests on
# plain curl rather than the authenticated API helper used for metadata.
grep -Fq 'curl -fsSL "$download_url"' "$ROOT_DIR/deploy/install.sh"
grep -Fq 'curl -fsSL "$checksum_url"' "$ROOT_DIR/deploy/install.sh"

echo "install GitHub token checks passed"
