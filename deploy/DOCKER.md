# TokenRouter Docker images

TokenRouter publishes multi-architecture images to GitHub Container Registry:

```text
ghcr.io/tokenflux/tokenrouter:latest
```

The application requires PostgreSQL and Redis. Docker Compose provides runtime configuration, persistent storage, health checks, and dependency startup ordering.

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

When using `docker run` directly, provide the same settings as the `tokenrouter` service in `docker-compose.standalone.yml`. At minimum, configure:

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
| `TOTP_ENCRYPTION_KEY` | Stable encryption key for two-factor authentication. |

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
