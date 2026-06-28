<div align="center">
<h1>TokenRouter</h1>

[![Go](https://img.shields.io/badge/Go-1.25.7-00ADD8.svg)](https://golang.org/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED.svg)](https://www.docker.com/)

**AI API 网关平台 - 订阅配额分发管理**

</div>

## 项目概述

TokenRouter 是一个 AI API 网关平台，用于分发和管理 AI 产品订阅的 API 配额。用户通过平台生成的 API Key 调用上游 AI 服务，平台负责鉴权、计费、负载均衡和请求转发。

TokenRouter 基于 [Sub2API](https://github.com/Wei-Shaw/sub2api) 开发，在此感谢上游项目的贡献。

## Grok / xAI OAuth 支持

TokenRouter 支持通过 xAI OAuth 接入 Grok 订阅账号，并将 OpenAI-compatible Responses 流量转发到 xAI。

- 平台名：`grok`
- 账号类型：OAuth 订阅账号
- 网关目标：`/v1/responses` 和 `/responses`，转发到 `${XAI_BASE_URL:-https://api.x.ai/v1}/responses`
- 初始模型：`grok-4.3`、`grok-build-0.1`、`grok-4.20-0309-reasoning`、`grok-4.20-0309-non-reasoning`、`grok-4.20-multi-agent-0309`
- 暂不支持：Grok public Chat Completions、image、video、TTS、transcription、browser automation、cookies、Grok web scraping

相关环境变量：`XAI_OAUTH_CLIENT_ID`、`XAI_OAUTH_SCOPE`、`XAI_OAUTH_REDIRECT_URI`、`XAI_OAUTH_AUTHORIZE_URL`、`XAI_OAUTH_TOKEN_URL`、`XAI_BASE_URL`。

## 部署方式

详细部署说明见 [DEPLOY_GUIDE.md](docs/DEPLOY_GUIDE.md)。

## 许可证

This project is licensed under the [GNU Lesser General Public License v3.0](LICENSE) (or later).

Copyright (c) 2026 Wesley Liddick & TokenFlux
