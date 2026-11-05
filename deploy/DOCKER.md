# TokenRouter Docker images

TokenRouter publishes multi-architecture images to GitHub Container Registry:

```text
ghcr.io/tokenflux/tokenrouter:latest
```

The application requires PostgreSQL and Redis. Docker Compose provides installation inputs, persistent configuration storage, health checks, and dependency startup ordering.

## Docker Compose

Choose a configuration for your deployment topology:

| File | Services | Storage | Use case |
| --- | --- | --- | --- |
| [docker-compose.local.yml](docker-compose.local.yml) | TokenRouter, PostgreSQL, Redis | Local directories | Production deployments using bind mounts for backups and migration. |
| [docker-compose.yml](docker-compose.yml) | TokenRouter, PostgreSQL, Redis | Docker named volumes | Production deployments using Docker volume management. |
| [docker-compose.standalone.yml](docker-compose.standalone.yml) | TokenRouter only | Docker named volumes | Environments with external PostgreSQL and Redis. |
| [docker-compose.dev.yml](https://github.com/TokenFlux/TokenRouter/blob/main/deploy/docker-compose.dev.yml) | Locally built TokenRouter, PostgreSQL, Redis | Local directories | Development and source testing; requires a full source checkout. |

See the [deployment guide](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/index.md) for complete steps and [.env.example](.env.example) for environment variables.

## Standalone containers

When using `docker run` directly, provide the same settings as the `tokenrouter` service in `docker-compose.standalone.yml`. For the first installation, configure:

| Variable | Purpose |
| --- | --- |
| `AUTO_SETUP=true` | Enables unattended container initialization. |
| `SERVER_HOST=0.0.0.0` | Listens on the container network interfaces. |
| `DATABASE_HOST`, `DATABASE_PORT` | PostgreSQL address. |
| `DATABASE_USER`, `DATABASE_PASSWORD`, `DATABASE_DBNAME` | PostgreSQL credentials and database name. |
| `DATABASE_SSLMODE` | PostgreSQL TLS mode. |
| `REDIS_HOST`, `REDIS_PORT` | Redis address. |
| `REDIS_USERNAME`, `REDIS_PASSWORD`, `REDIS_DB` | Redis credentials and database number. |
| `JWT_SECRET` | Stable signing key for login sessions. |
| `TOTP_ENCRYPTION_KEY` | Stable encryption key for stored credentials and two-factor authentication. |

## Database startup recovery

During startup migrations, the application retries temporary PostgreSQL availability and connection failures with bounded exponential backoff. Invalid credentials, migration checksum failures, and other permanent errors return immediately. Compose checks PostgreSQL readiness with both `pg_isready` and a SQL query; application retries also cover database recovery after a host restart.

Persist `/app/data`, bind published ports to the intended host interface, and apply the security and resource limits from the standalone Compose template. The application does not use the old `DATABASE_URL` or `REDIS_URL` variables.

## Supported architectures

- `linux/amd64`
- `linux/arm64`

## Image tags

- `latest`: the latest stable release.
- `vX.Y.Z`: an immutable version tag.

Pin a version tag or image digest in production, and verify database backups before upgrading.

## Related links

- [GitHub repository](https://github.com/TokenFlux/TokenRouter)
- [Deployment guide](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/index.md)

## Versioned application configuration

Environment inputs initialize a new installation. Subsequent starts read
`/app/data/config.yaml`, which contains `config_version: 2`. Edit the persisted file
and restart the service:

```bash
docker compose exec tokenrouter vi /app/data/config.yaml
docker compose restart tokenrouter
```

You can also mount `./config.yaml:/app/data/config.yaml`. Writable single-file
mounts support automatic upgrades. Passwords and other secrets can use explicit
references such as `${DATABASE_PASSWORD}`; `${NAME:-default}` supplies a fallback,
and `$${` represents a literal `${`. The templates keep setup inputs and secret
names that migrated files may reference. Other application variables no longer
override configuration. For a custom reference, explicitly pass that variable in
the service's `environment` section.

Automatic installation and v1 migration also preserve nonempty `DATABASE_HOST`
and `REDIS_HOST` as explicit references. Keep passing these variables on later
starts, or replace the references with fixed hostnames in the configuration.

A version 1 file is backed up to `config.yaml.v1.bak` before its keys and legacy
environment inputs are migrated. Existing backups receive a UTC timestamp suffix.
The backup has mode `0600`; the upgraded file retains its original permissions.
Current-version files are not rewritten. Remaining ineffective variables produce
one warning listing their names, without values.

Read-only legacy mounts require an offline copy. This command inherits the
service's environment and writes the upgraded file on the host without changing
the mounted source:

```bash
umask 077
docker compose run --rm --no-deps tokenrouter /app/tokenrouter \
  -migrate-config -config /app/data/config.yaml -out - > config.v2.yaml
```

Replace the mounted file with the output, then restart. To roll back the binary,
stop the new process and restore the original v1 configuration and deployment
environment first; old binaries do not understand v2 names or references. See the
[configuration upgrade notes](https://github.com/TokenFlux/TokenRouter/blob/main/docs/operations/upgrade_notes.md#config_version_2).

New installations use the code defaults for database pools, Redis idle connections,
and the OpenAI compact-model override. Existing values are imported during the
first upgrade. Changing a listener port also requires matching container port
mappings and health-check settings.
