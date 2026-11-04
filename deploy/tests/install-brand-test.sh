#!/usr/bin/env bash
set -euo pipefail

# install-brand-test.sh checks installer output and compatibility using
# temporary installation roots and local archives. The fixture verifies
# English help, legacy identity selection, archive-name compatibility, and
# preservation of operator-owned pricing files without modifying host services
# or real installations.

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${TEST_DIR}/../install.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "$TEST_ROOT"' EXIT

# Help and shared messages must use English without a language selection step.
# Piped help must finish without terminal input or access to release metadata.
[[ "$(msg 'install_title')" == 'TokenRouter Installation Script' ]]
[[ "$(msg 'wizard_admin')" == 'Admin account creation' ]]
help_output=$(bash "${TEST_DIR}/../install.sh" --help </dev/null)
grep -Fq 'Usage:' <<<"$help_output"
grep -Fq 'Install TokenRouter' <<<"$help_output"
if grep -Fq 'Select language' <<<"$help_output"; then
    echo 'Installer help still asks for a language selection' >&2
    exit 1
fi

# Installation detection runs against an isolated temporary root. The fixture
# checks identity selection without invoking systemctl or changing an
# installation on the host.
detect_existing_installation "$TEST_ROOT"
[[ "$SERVICE_NAME" == tokenrouter && "$INSTALL_DIR" == "$TEST_ROOT/opt/tokenrouter" ]]
mkdir -p "$TEST_ROOT/opt/sub2api" "$TEST_ROOT/etc/systemd/system"
printf 'User=legacy-service-user\n' > "$TEST_ROOT/etc/systemd/system/sub2api.service"
detect_existing_installation "$TEST_ROOT"
[[ "$SERVICE_NAME" == sub2api && "$BINARY_NAME" == sub2api ]]
[[ "$SERVICE_USER" == legacy-service-user && "$CONFIG_DIR" == "$TEST_ROOT/etc/sub2api" ]]
mkdir -p "$TEST_ROOT/opt/tokenrouter"
if detect_existing_installation "$TEST_ROOT" >/dev/null 2>&1; then
    echo 'Installer accepted conflicting installations' >&2
    exit 1
fi
# Local archives and a download substitute cover both archive names. An
# existing legacy installation must accept current binaries without changing
# its executable path.
rmdir "$TEST_ROOT/opt/tokenrouter"
detect_existing_installation "$TEST_ROOT"
FIXTURE_DIR="$TEST_ROOT/releases"
mkdir -p "$FIXTURE_DIR/old" "$FIXTURE_DIR/new"
printf 'legacy-binary' > "$FIXTURE_DIR/old/sub2api"
printf 'new-binary' > "$FIXTURE_DIR/new/tokenrouter"
printf 'new-binary' > "$FIXTURE_DIR/new/sub2api"
OS=linux
ARCH=amd64
LATEST_VERSION=v1.2.3
tar -czf "$FIXTURE_DIR/sub2api_1.2.3_linux_amd64.tar.gz" -C "$FIXTURE_DIR/old" sub2api
tar -czf "$FIXTURE_DIR/tokenrouter_1.2.3_linux_amd64.tar.gz" -C "$FIXTURE_DIR/new" tokenrouter sub2api
(cd "$FIXTURE_DIR" && sha256sum ./*.tar.gz | sed 's|  ./|  |' > checksums.txt)
LEGACY_ONLY=true
curl() {
    local url="" output=""
    while [[ $# -gt 0 ]]; do
        case "$1" in
            https://*) url="$1" ;;
            -o) shift; output="$1" ;;
        esac
        shift
    done
    local name="${url##*/}"
    if [[ "$LEGACY_ONLY" == true && "$name" == tokenrouter_* ]]; then return 1; fi
    cp "$FIXTURE_DIR/$name" "$output"
}
download_and_extract
[[ "$(cat "$INSTALL_DIR/$BINARY_NAME")" == legacy-binary ]]
[[ "$INSTALL_DIR" == "$TEST_ROOT/opt/sub2api" ]]
rm -rf "$TEMP_DIR"
LEGACY_ONLY=false
download_and_extract
[[ "$(cat "$INSTALL_DIR/$BINARY_NAME")" == new-binary ]]
[[ "$BINARY_NAME" == sub2api ]]
# Default model supplements are embedded in the binary. Installing an archive
# must neither create external supplements nor replace custom files left by
# the operator.
PRICING_RESOURCE="resources/model-pricing/model_pricing_supplements.json"
[[ ! -e "$INSTALL_DIR/$PRICING_RESOURCE" ]]
mkdir -p "$INSTALL_DIR/resources/model-pricing"
printf 'custom-pricing' > "$INSTALL_DIR/$PRICING_RESOURCE"
rm -rf "$TEMP_DIR"
download_and_extract
[[ "$(cat "$INSTALL_DIR/$PRICING_RESOURCE")" == custom-pricing ]]
# The download function installs its own exit cleanup, so restore fixture
# cleanup after assertions to remove both temporary roots.
trap 'rm -rf "$TEST_ROOT" "$TEMP_DIR"' EXIT
echo 'Installer brand compatibility tests passed.'
