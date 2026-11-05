#!/bin/sh
set -eu

# docker-compose-postgres-test.sh checks shared PostgreSQL tuning expressions
# and the deployment environment example. The templates must pass the same
# settings to the database, while the versioned YAML example retains application
# pool capacities independently of the database service environment.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

# fail reports configuration drift and exits with a diagnostic for CI logs.
fail() {
  printf 'docker compose postgres test failed: %s\n' "$1" >&2
  exit 1
}

# assert_line requires an exact configuration line in the supplied file. It
# exits with a diagnostic when the line is absent so similar parameters cannot
# hide a changed expression.
assert_line() {
  file=$1
  line=$2
  grep -Fqx "$line" "$file" || fail "$file is missing: $line"
}

# All three bundled PostgreSQL variants expose the same server tuning
# variables. Check their expressions together so switching persistence or
# build mode preserves the configured limits.
for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.dev.yml
do
  assert_line "$compose_file" '    shm_size: ${POSTGRES_SHM_SIZE:-1gb}'
  assert_line "$compose_file" '      -c max_connections=${POSTGRES_MAX_CONNECTIONS:-100}'
  assert_line "$compose_file" '      -c shared_buffers=${POSTGRES_SHARED_BUFFERS:-128MB}'
  assert_line "$compose_file" '      -c effective_cache_size=${POSTGRES_EFFECTIVE_CACHE_SIZE:-4GB}'
  assert_line "$compose_file" '      -c maintenance_work_mem=${POSTGRES_MAINTENANCE_WORK_MEM:-64MB}'
done

# Database service tuning stays in .env; application pools live in config.yaml.
# Check both examples so fresh installations retain the intended capacities.
assert_line deploy/.env.example 'POSTGRES_SHM_SIZE=1gb'
assert_line deploy/.env.example 'POSTGRES_MAX_CONNECTIONS=512'
assert_line deploy/config.example.yaml '  max_open_conns: 256'
assert_line deploy/config.example.yaml '  max_idle_conns: 128'

printf 'docker compose postgres test passed\n'
