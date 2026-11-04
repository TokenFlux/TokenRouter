#!/bin/sh
set -eu

# docker-compose-security-test.sh checks the application security option in
# every supported Compose topology. Each configuration must declare no-new-
# privileges exactly once for the application service so changing storage or
# build mode does not remove the shared baseline.

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

# check_application_security_opt verifies exactly one application
# no-new-privileges setting in the supplied Compose file. It exits with an
# error if the application security baseline is absent or duplicated.
check_application_security_opt() {
  file=$1
  count=$(
    awk '
      $0 == "  tokenrouter:" {
        in_application = 1
        next
      }
      in_application && $0 ~ /^  [A-Za-z0-9_-]+:$/ {
        in_application = 0
      }
      in_application && $0 == "    security_opt:" {
        in_security_opt = 1
        next
      }
      in_application && in_security_opt && $0 == "      - no-new-privileges:true" {
        count++
      }
      END { print count + 0 }
    ' "$file"
  )

  if [ "$count" -ne 1 ]; then
    printf '%s must enable no-new-privileges exactly once for the tokenrouter service\n' "$file" >&2
    exit 1
  fi
}

# Each supported deployment topology must retain the application security
# baseline. Check production volumes, bind mounts, external dependencies, and
# source builds together.
for compose_file in \
  deploy/docker-compose.yml \
  deploy/docker-compose.local.yml \
  deploy/docker-compose.standalone.yml \
  deploy/docker-compose.dev.yml
do
  check_application_security_opt "$compose_file"
done

printf 'docker compose security test passed\n'
