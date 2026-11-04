#!/bin/sh
set -eu

# docker-compose-gateway-env-test.sh checks gateway environment propagation
# across Compose variants. Variables documented in the environment example
# must appear once in each application service, while omitted connection-pool
# overrides retain backend defaults instead of adopting the example tuning
# values.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

gateway_variables=$(mktemp "${TMPDIR:-/tmp}/tokenrouter-gateway-env.XXXXXX")
cleanup() {
  rm -f "$gateway_variables"
}
trap cleanup EXIT HUP INT TERM

awk '
  /^GATEWAY_[A-Z0-9_]+=/ {
    separator = index($0, "=")
    print substr($0, 1, separator - 1) "\t" substr($0, separator + 1)
  }
' deploy/.env.example > "$gateway_variables"

for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.standalone.yml \
  deploy/docker-compose.dev.yml
do
  tab=$(printf '\t')
  while IFS="$tab" read -r key value; do
    # .env.example keeps high-capacity tuning examples for these connection
    # limits.
    # Deployments without explicit overrides must retain backend defaults
    # instead of silently adopting the larger capacities from the environment
    # example.
    case "$key" in
      GATEWAY_MAX_CONNS_PER_HOST) value=1024 ;;
      GATEWAY_MAX_IDLE_CONNS) value=2560 ;;
      GATEWAY_MAX_IDLE_CONNS_PER_HOST) value=120 ;;
    esac

    expected=$(printf '      - %s=${%s:-%s}' "$key" "$key" "$value")
    expected_count=$(grep -Fxc "$expected" "$compose_file" || true)
    key_count=$(grep -Ec "^[[:space:]]*-[[:space:]]*${key}([[:space:]]*=.*)?[[:space:]]*$" "$compose_file" || true)
    if [ "$expected_count" -ne 1 ] || [ "$key_count" -ne 1 ]; then
      printf '%s must pass %s with the expected fallback exactly once\n' "$compose_file" "$key" >&2
      exit 1
    fi
  done < "$gateway_variables"
done

printf 'docker compose Gateway environment test passed\n'
