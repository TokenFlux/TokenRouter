<div align="center">
  <p>
    <img src="assets/readme/banner.svg" width="100%" alt="TokenRouter, an open source LLM gateway. Messages, Responses, Chat Completions, and Gemini requests enter one endpoint and are routed to upstream platforms such as Anthropic, OpenAI, and Gemini, with failover when an upstream returns an error." />
  </p>

  <p>
    <a href="https://github.com/TokenFlux/TokenRouter/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/TokenFlux/TokenRouter/ci.yml?branch=main&style=flat-square&label=CI&logo=githubactions&logoColor=white&labelColor=27272A" alt="CI" /></a>
    <a href="https://github.com/TokenFlux/TokenRouter/releases"><img src="https://img.shields.io/github/v/release/TokenFlux/TokenRouter?display_name=tag&style=flat-square&label=release&labelColor=27272A&color=3F3F46" alt="Release" /></a>
    <a href="https://github.com/TokenFlux/TokenRouter/pkgs/container/tokenrouter"><img src="https://img.shields.io/badge/container-ghcr.io-3F3F46?style=flat-square&logo=docker&logoColor=white&labelColor=27272A" alt="Container: ghcr.io/tokenflux/tokenrouter" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-LGPL--3.0--or--later-3F3F46?style=flat-square&labelColor=27272A" alt="License: LGPL-3.0-or-later" /></a>
    <a href="https://discord.com/invite/7XZzukhX4E"><img src="https://img.shields.io/badge/Discord-join-5865F2?style=flat-square&logo=discord&logoColor=white&labelColor=27272A" alt="Discord" /></a>
  </p>

  <p><strong>English</strong> · <a href="README_ZH.md">简体中文</a> · <a href="#plugins">Plugins</a> · <a href="#quick-start">Quick Start</a> · <a href="#documentation">Documentation</a></p>
</div>

## Overview

TokenRouter is an open source unified LLM gateway. It puts model services from Anthropic, OpenAI, Gemini, and other vendors behind one endpoint, so a client can call models from different vendors with a single API key.

> [!WARNING]
> Commercial API gateway services are permitted under the applicable open-source licenses. You must not impersonate the official TokenFlux service or use names, logos, domains, interfaces, or promotional materials that misleadingly suggest official operation, authorization, partnership, or endorsement by TokenFlux. Commercial operators are encouraged to customize the frontend and clearly identify the actual operator. The relevant rights holders reserve the right to seek corrective action under applicable law for infringement or such confusion. See the [Commercial Use and Branding Notice](BRANDING_EN.md).

## Screenshots

### Dashboard

<img src="assets/screenshots/dashboard-dark.jpg" alt="TokenRouter Dashboard · Dark mode" width="1600" />

### Model Marketplace

<img src="assets/screenshots/models-dark.jpg" alt="TokenRouter Model Marketplace · Dark mode" width="1600" />

<details>
<summary>Light mode screenshots</summary>

### Dashboard

<img src="assets/screenshots/dashboard-light.jpg" alt="TokenRouter Dashboard · Light mode" width="1600" />

### Model Marketplace

<img src="assets/screenshots/models-light.jpg" alt="TokenRouter Model Marketplace · Light mode" width="1600" />

</details>

Visit the service officially operated by TokenFlux at [tokenflux.com](https://tokenflux.com).

## Capabilities

| Area | What you can do |
| --- | --- |
| Model access | Accept Anthropic Messages, OpenAI Responses, Chat Completions, and Gemini requests, and convert between the protocols a group allows |
| Upstream platforms | Connect Anthropic, OpenAI, Gemini, Antigravity, Grok, Qoder, Kimi, Zhipu, and DeepSeek through OAuth accounts, API keys, AWS Bedrock, or Vertex AI |
| Routing and retries | Pick an upstream by model, protocol support, rate limit state, and sticky sessions, and move to the next one when an upstream fails |
| Access control | Manage users, teams, groups, and API key quotas, concurrency, and RPM limits. Sign in with email and password, passkeys, two-factor authentication, GitHub, Google, LinuxDo, OIDC, WeChat, or DingTalk |
| Billing and payments | Charge by usage, with balance, subscription plans, quota packs, redeem codes, and referral rebates. Built-in payments support EasyPay, Alipay, WeChat Pay, Stripe, Airwallex, and EPUSDT (EPay protocol) |
| Content moderation | Block requests with keyword and hash rules, and ban offending users automatically |
| Admin dashboard | Browse usage logs and statistics, set up alerts and email reports, and upgrade or roll back online. The creative studio generates and edits images in the browser. The UI is available in Simplified Chinese and English |
| Plugins | Add user and admin pages, HTTP APIs, and scheduled tasks. Install, configure, update, and roll back plugins from the admin dashboard |

### Protocols and Endpoints

| Type | Common endpoints |
| --- | --- |
| Anthropic Messages | `POST /v1/messages` |
| OpenAI Responses / Chat | `POST /v1/responses`, `POST /v1/chat/completions` |
| Gemini | `POST /v1beta/models/{model}:generateContent`, `POST /v1beta/models/{model}:streamGenerateContent` |
| WebSocket | `GET /v1/responses`, `GET /v1/realtime` |
| Embeddings | `POST /v1/embeddings` |
| Images | `/v1/images/generations`, `/v1/images/edits`, `/v1/images/batches` |
| Video | `/v1/videos/generations`, `/v1/videos/edits`, `/v1/videos/extensions` |
| Voice | `/v1/tts`, `/v1/stt`, `/v1/custom-voices` |
| Search | `/v1/web_search`, `/v1/x_search`, `/v1/alpha/search` |
| Models and usage | `GET /v1/models`, `GET /v1/usage` |

The [upstream provider capability matrix (Chinese)](docs/interfaces/upstream_provider_matrix.md) lists which upstream platforms serve each endpoint.

## Plugins

Plugins extend TokenRouter with custom features without modifying the core application. The [daily check-in plugin](https://github.com/TokenFlux/tr-plugin-checkin), for example, adds a user check-in page, balance rewards, and an admin history page.

- Install from the built-in catalog or a custom source, or upload a `.tar.gz` package in **Plugin Management** (`/admin/plugins`). Review requested permissions before installation.
- Configure settings and operation limits, inspect logs and resource usage, and enable, disable, or restart plugins. Updates support rollback to the previous version; failed startup during an update triggers an attempt to restore the old version.
- Build user and admin pages, HTTP APIs, and scheduled tasks. Granted host permissions allow plugins to query usage, adjust balances, issue redeem codes, grant subscriptions, or send email. Each plugin has its own KV storage namespace.

The MIT-licensed Go and Web SDKs support backend processes and Vue pages. Start with the [official template](https://github.com/TokenFlux/tokenrouter-plugin-template) and [plugin development guide (Chinese)](docs/guides/plugins/index.md).

Plugins run as administrator-trusted code, without an OS or browser sandbox. Custom model providers are not currently supported. See [plugin operations (Chinese)](docs/operations/plugin_operations.md) for installation, recovery, and backup details.

## Quick Start

Deploy with Docker Compose:

```bash
mkdir -p tokenrouter-deploy && cd tokenrouter-deploy

# Download the Compose file and .env, and generate the database password and secrets
curl -sSL https://raw.githubusercontent.com/TokenFlux/TokenRouter/main/deploy/docker-deploy.sh | bash

docker compose up -d
```

Then open `http://localhost:8080`. You can set the admin password with `ADMIN_PASSWORD` in `.env`. If it is not set, TokenRouter generates a random password and prints it to the log:

```bash
docker compose logs tokenrouter | grep "admin password"
```

The [deployment guide (Chinese)](docs/guides/deployment/index.md) covers other options such as the install script and standalone containers.

## Development

The backend is written in Go and the frontend in Vue 3. To run the full stack from source:

```bash
docker compose -f deploy/docker-compose.dev.yml up --build
```

[Development workflow (Chinese)](docs/operations/development_workflow.md) lists the required toolchain versions, how to run the backend and frontend separately, and the test commands.

## Documentation

- [Usage and operations guides (Chinese)](docs/guides/index.md): deployment, payment setup, and external integrations.
- [Engineering documentation (Chinese)](docs/index.md): architecture, business rules, interfaces, and operational constraints. Start here before changing code.
- [Upgrade notes (Chinese)](docs/operations/upgrade_notes.md): changes to watch for in each release.

## License

This project is distributed under the [GNU Lesser General Public License v3.0 or later](LICENSE).

The `plugin-sdk/` directory uses the [MIT License](plugin-sdk/LICENSE).

Copyright (c) 2026 Wesley Liddick & TokenFlux

## Star History

<a href="https://star-history.com/#TokenFlux/TokenRouter&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=TokenFlux/TokenRouter&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=TokenFlux/TokenRouter&type=Date" alt="Star History Chart" />
  </picture>
</a>
