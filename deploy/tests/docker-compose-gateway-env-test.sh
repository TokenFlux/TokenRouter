#!/bin/sh
set -eu

# docker-compose-gateway-env-test.sh checks the version 2 environment boundary
# across Compose variants. Application settings must come from persisted YAML,
# while setup credentials and reference-only secrets remain available to files
# that explicitly name their environment sources.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.standalone.yml \
  deploy/docker-compose.dev.yml
do
  if grep -Eq '^[[:space:]]*-[[:space:]]*(GATEWAY_|SECURITY_|DATABASE_MAX_|DATABASE_CONN_|REDIS_POOL_SIZE=|REDIS_MIN_IDLE_CONNS=|ENABLE_SERVER_TIMING=|PLUGINS_ENABLED=)' "$compose_file"; then
    printf '%s still passes implicit application configuration\n' "$compose_file" >&2
    exit 1
  fi
  for key in DATABASE_PASSWORD REDIS_PASSWORD JWT_SECRET TOTP_ENCRYPTION_KEY UPDATE_GITHUB_TOKEN PAYMENT_RESUME_SIGNING_KEY; do
    count=$(grep -Ec "^[[:space:]]*-[[:space:]]*${key}=" "$compose_file" || true)
    if [ "$count" -ne 1 ]; then
      printf '%s must pass reference source %s exactly once\n' "$compose_file" "$key" >&2
      exit 1
    fi
  done
done

if grep -Eq '^(GATEWAY_|SECURITY_|DATABASE_MAX_|DATABASE_CONN_|REDIS_POOL_SIZE=|REDIS_MIN_IDLE_CONNS=|ENABLE_SERVER_TIMING=|PLUGINS_ENABLED=)' deploy/.env.example; then
  printf '.env.example still documents implicit application overrides\n' >&2
  exit 1
fi

printf 'docker compose configuration environment test passed\n'
