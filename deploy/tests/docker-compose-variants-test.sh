#!/bin/sh
set -eu

# docker-compose-variants-test.sh compares shared application settings across
# deployment topologies. It checks environment propagation, startup health
# policy, and file descriptor limits so switching storage or building from
# source does not silently lose the common deployment configuration.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

# 统一输出失败原因，方便从 CI 日志定位配置漂移。
fail() {
  printf 'docker compose variants test failed: %s\n' "$1" >&2
  exit 1
}

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/tokenrouter-compose-variants.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

# 只提取应用服务的 KEY=value 环境变量，忽略注释和其他服务。
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

# 保存环境变量和值，并单独保存键集合用于拓扑变体比较。
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

# 两个生产内置服务变体除持久化方式外，应用环境必须完全一致。
cmp -s "$tmp_dir/standard.env" "$tmp_dir/local.env" || \
  fail 'standard and local application environments differ'

# standalone 只允许连接地址表达式不同，不能遗漏公共配置键。
cmp -s "$tmp_dir/standard.keys" "$tmp_dir/standalone.keys" || \
  fail 'standalone application environment keys differ from standard'

# 开发版可增加开发专用变量，但必须包含全部公共配置键。
while IFS= read -r key
do
  grep -Fqx "$key" "$tmp_dir/dev.keys" || \
    fail "dev application environment is missing $key"
done < "$tmp_dir/standard.keys"

# 除部署拓扑和调试模式外，公共环境变量的表达式也必须一致。
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

# 内置三服务变体都应为应用、PostgreSQL 和 Redis 设置文件描述符上限。
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

# standalone 只有应用容器，因此只需要一组文件描述符上限。
soft_count=$(grep -Fxc '        soft: 100000' deploy/docker-compose.standalone.yml || true)
hard_count=$(grep -Fxc '        hard: 100000' deploy/docker-compose.standalone.yml || true)
[ "$soft_count" -eq 1 ] || fail 'standalone must contain one soft nofile limit'
[ "$hard_count" -eq 1 ] || fail 'standalone must contain one hard nofile limit'

printf 'docker compose variants test passed\n'
