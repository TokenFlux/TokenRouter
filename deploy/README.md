# TokenRouter deployment files

Choose an entry point for your deployment. See the [deployment guide](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/index.md) for installation, upgrades, and backups, and [DOCKER.md](DOCKER.md) for container configuration.

| Deployment | Entry point | Description |
| --- | --- | --- |
| Linux binary | [install.sh](install.sh) | Downloads a release archive and configures systemd; requires external PostgreSQL and Redis. Prompts and messages are in English. |
| Docker setup | [docker-deploy.sh](docker-deploy.sh) | Downloads the bind-mount Compose template and environment example, then generates secrets. Start the services after reviewing the configuration. |
| Docker named volumes | [docker-compose.yml](docker-compose.yml) | Runs the application, PostgreSQL, and Redis with named volumes. |
| Docker bind mounts | [docker-compose.local.yml](docker-compose.local.yml) | Persists application, PostgreSQL, and Redis data in local directories. |
| External PostgreSQL and Redis | [docker-compose.standalone.yml](docker-compose.standalone.yml) | Runs only the application container. |
| Apple Container | [apple-container.sh](apple-container.sh) | Manages the application and its dependencies; see the [Apple Container guide](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/apple_container.md). |

## Configuration and templates

- [.env.example](.env.example): container environment variables. Copy it to `.env` and configure the values.
- [config.example.yaml](config.example.yaml): the full application configuration example.
- [Caddyfile](Caddyfile): a reverse proxy baseline. Replace the example domain before use.
- [tokenrouter.service](tokenrouter.service): a systemd template for manual installation. The installer generates a service definition using the actual installation paths.
- [codex-instructions.md.tmpl](codex-instructions.md.tmpl): an optional Codex request instructions template, enabled by setting its path in the application configuration.

## Files requiring a source checkout

`docker-compose.dev.yml` and `build_image.sh` build the complete application with the repository-root `Dockerfile` and require a full source checkout. Both source and release image Dockerfiles copy `docker-entrypoint.sh` to handle volume permissions and container startup. Deployment tests live in `tests/`; run `make test-scripts` from the repository root.

Binary release archives include the deployment files explicitly listed in `.goreleaser.yaml`. Development Compose, build helpers, the container entrypoint, and test fixtures are excluded. The old data management daemon is retired; see the [datamanagementd retirement notes](https://github.com/TokenFlux/TokenRouter/blob/main/docs/guides/deployment/datamanagementd.md) to identify legacy deployments.
