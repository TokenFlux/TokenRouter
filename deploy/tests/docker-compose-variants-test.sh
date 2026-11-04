#!/bin/sh
set -eu

# docker-compose-variants-test.sh compares shared application settings across
# deployment topologies. It checks environment propagation, startup health
# policy, and file descriptor limits so switching storage or building from
# source does not silently lose the common deployment configuration.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

# fail reports configuration drift and exits with a diagnostic for CI logs.
fail() {
  printf 'docker compose variants test failed: %s\n' "$1" >&2
  exit 1
}

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/tokenrouter-compose-variants.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

# extract_application_environment prints sorted KEY=value entries from the
# application service in the supplied Compose file. Comments and other
# services are excluded so topology-specific dependency configuration does not
# affect this comparison.
extract_application_environment() {
  file=$1
  awk '
    $0 == "  tokenrouter:" {
      in_application = 1
      next
    }
    in_application && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
      exit
    }
    in_application && $0 == "    environment:" {
      in_environment = 1
      next
    }
    in_environment && $0 ~ /^    [A-Za-z0-9_-]+:/ {
      exit
    }
    in_environment && $0 ~ /^      - [A-Z][A-Z0-9_]*=/ {
      sub(/^      - /, "")
      print
    }
  ' "$file" | LC_ALL=C sort
}

# save_environment writes sorted application settings and a separate key list
# for a named Compose variant. It uses the shared temporary directory so later
# checks can compare values or key coverage.
save_environment() {
  name=$1
  file=$2
  extract_application_environment "$file" > "$tmp_dir/$name.env"
  cut -d= -f1 "$tmp_dir/$name.env" > "$tmp_dir/$name.keys"
}

# extract_service_healthcheck prints a service's canonical health check block.
# It accepts a Compose path and service name, omits comment lines and
# indentation, and returns empty output when that service has no health check.
extract_service_healthcheck() {
  awk -v service="$2" '
    $0 == "  " service ":" {
      in_service = 1
      next
    }
    in_service && $0 ~ /^  [A-Za-z0-9_-]+:$/ { exit }
    in_service && $0 == "    healthcheck:" {
      in_healthcheck = 1
      next
    }
    in_healthcheck && $0 ~ /^[^ #]|^    [A-Za-z0-9_-]+:/ { exit }
    in_healthcheck && $0 !~ /^[[:space:]]*(#|$)/ {
      sub(/^[[:space:]]+/, "")
      sub(/[[:space:]]+$/, "")
      print
    }
  ' "$1"
}

# Each deployment runs the same application bootstrap against PostgreSQL.
# Compare health policies so alternate storage and source builds retain the
# SQL readiness check and the startup recovery grace periods.
extract_service_healthcheck deploy/docker-compose.yml tokenrouter > "$tmp_dir/app.health"
extract_service_healthcheck deploy/docker-compose.yml postgres > "$tmp_dir/postgres.health"
grep -Fxq 'start_period: 120s' "$tmp_dir/app.health" || \
  fail 'application bootstrap grace period differs'
grep -Fq -- "-Atqc 'SELECT 1'" "$tmp_dir/postgres.health" || \
  fail 'PostgreSQL health check must execute SQL'
grep -Fxq 'retries: 18' "$tmp_dir/postgres.health" || \
  fail 'PostgreSQL recovery retry budget differs'
grep -Fxq 'start_period: 60s' "$tmp_dir/postgres.health" || \
  fail 'PostgreSQL recovery grace period differs'

for variant in local standalone dev
do
  compose_file="deploy/docker-compose.$variant.yml"
  extract_service_healthcheck "$compose_file" tokenrouter > "$tmp_dir/$variant.app.health"
  cmp -s "$tmp_dir/app.health" "$tmp_dir/$variant.app.health" || \
    fail "$variant application health check differs from standard"
  if [ "$variant" != standalone ]; then
    extract_service_healthcheck "$compose_file" postgres > "$tmp_dir/$variant.postgres.health"
    cmp -s "$tmp_dir/postgres.health" "$tmp_dir/$variant.postgres.health" || \
      fail "$variant PostgreSQL health check differs from standard"
  fi
done

save_environment standard deploy/docker-compose.yml
save_environment local deploy/docker-compose.local.yml
save_environment standalone deploy/docker-compose.standalone.yml
save_environment dev deploy/docker-compose.dev.yml

# The production variants differ in persistence, while their application
# environments must match. Compare complete entries to catch both missing
# variables and changed expressions.
cmp -s "$tmp_dir/standard.env" "$tmp_dir/local.env" || \
  fail 'standard and local application environments differ'

# Standalone connections may use different address expressions. Their
# application environment must still include every shared configuration key
# supplied by the standard deployment.
cmp -s "$tmp_dir/standard.keys" "$tmp_dir/standalone.keys" || \
  fail 'standalone application environment keys differ from standard'

# Development may add its own variables. It must still include all shared
# application keys so source builds exercise the same configurable runtime
# behavior.
while IFS= read -r key
do
  grep -Fqx "$key" "$tmp_dir/dev.keys" || \
    fail "dev application environment is missing $key"
done < "$tmp_dir/standard.keys"

# Deployment topology and debug mode account for the intentional environment
# differences. All remaining shared expressions must match across the variants
# checked here.
for variant in standalone dev
do
  while IFS= read -r entry
  do
    key=${entry%%=*}
    case "$key" in
      SERVER_MODE|DATABASE_HOST|DATABASE_PORT|DATABASE_USER|DATABASE_PASSWORD|DATABASE_DBNAME|DATABASE_SSLMODE|REDIS_HOST|REDIS_PORT)
        continue
        ;;
    esac
    grep -Fqx "$entry" "$tmp_dir/$variant.env" || \
      fail "$variant has a different value expression for $key"
  done < "$tmp_dir/standard.env"
done

# Each bundled deployment runs three services. All three need the expected
# file descriptor limits so the application and dependencies retain their
# connection capacity.
for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.dev.yml
do
  soft_count=$(grep -Fxc '        soft: 100000' "$compose_file" || true)
  hard_count=$(grep -Fxc '        hard: 100000' "$compose_file" || true)
  [ "$soft_count" -eq 3 ] || fail "$compose_file must contain three soft nofile limits"
  [ "$hard_count" -eq 3 ] || fail "$compose_file must contain three hard nofile limits"
done

# Standalone runs only the application container. Its template therefore needs
# exactly one pair of file descriptor limits at the same values as other
# variants.
soft_count=$(grep -Fxc '        soft: 100000' deploy/docker-compose.standalone.yml || true)
hard_count=$(grep -Fxc '        hard: 100000' deploy/docker-compose.standalone.yml || true)
[ "$soft_count" -eq 1 ] || fail 'standalone must contain one soft nofile limit'
[ "$hard_count" -eq 1 ] || fail 'standalone must contain one hard nofile limit'

printf 'docker compose variants test passed\n'
