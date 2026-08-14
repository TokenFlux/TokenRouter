<div align="center">
  <img src="assets/logo.svg" alt="TokenRouter" width="112" />

  <h1>TokenRouter</h1>

  <p><strong>AI API gateway and subscription quota management platform</strong></p>

  <p>
    <a href="https://github.com/BrandonVee/TokenRouter/actions/workflows/backend-ci.yml"><img src="https://github.com/BrandonVee/TokenRouter/actions/workflows/backend-ci.yml/badge.svg" alt="CI" /></a>
    <a href="https://github.com/BrandonVee/TokenRouter/releases"><img src="https://img.shields.io/github/v/release/BrandonVee/TokenRouter?display_name=tag" alt="Release" /></a>
    <a href="https://github.com/BrandonVee/TokenRouter/pkgs/container/tokenrouter"><img src="https://img.shields.io/badge/container-ghcr.io%2Fbrandonvee%2Ftokenrouter-2496ED?logo=docker&logoColor=white" alt="Container" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-LGPL--3.0--or--later-4c1.svg" alt="License: LGPL-3.0-or-later" /></a>
  </p>

  <p><a href="README.md">简体中文</a> | <strong>English</strong></p>
</div>

## Overview

TokenRouter is a self-hosted AI API gateway and management platform for unified access to multiple upstream AI services. Users send requests with platform-issued API keys, while TokenRouter handles authentication, routing, account scheduling, request forwarding, usage tracking, and billing.

The project provides user and administration web interfaces for individuals and teams that need to manage upstream accounts, distribute API quotas, and operate AI services in one place.

TokenRouter builds on [Sub2API](https://github.com/Wei-Shaw/sub2api). Thanks to the upstream project and all contributors.

## Core Features

- Unified management for multiple upstreams and accounts
- API key, user, team, and group management
- Model mapping, request routing, and failover
- Concurrency, rate, and quota controls
- Usage tracking, balances, subscriptions, and billing
- User console, administration dashboard, and operational monitoring

## Supported Platforms

TokenRouter currently includes adapters for Anthropic, OpenAI, Gemini, Antigravity, Grok / xAI, and Qoder. See the [upstream account capability matrix (Chinese)](docs/interfaces/upstream_account_matrix.md) for the detailed support scope.

## Deployment

TokenRouter can be deployed with the installation script, Docker Compose, a source build, or Apple container.

- [Deployment guide (Chinese)](docs/guides/deployment/index.md)
- [Docker image documentation (Chinese)](deploy/DOCKER.md)
- [Apple container deployment guide (Chinese)](docs/guides/deployment/apple_container.md)

## Documentation

- [Usage and operations guides (Chinese)](docs/guides/index.md)
- [Interface documentation (Chinese)](docs/interfaces/index.md)
- [Engineering documentation (Chinese)](docs/index.md)

## License

This project is distributed under the [GNU Lesser General Public License v3.0 or later](LICENSE).

Copyright (c) 2026 BrandonVee
