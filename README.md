<div align="center">
  <img src="assets/logo.svg" alt="TokenRouter" width="112" />

  <h1>TokenRouter</h1>

  <p><strong>AI API 网关与订阅配额管理平台</strong></p>

  <p>
    <a href="https://github.com/BrandonVee/TokenRouter/actions/workflows/backend-ci.yml"><img src="https://github.com/BrandonVee/TokenRouter/actions/workflows/backend-ci.yml/badge.svg" alt="CI" /></a>
    <a href="https://github.com/BrandonVee/TokenRouter/releases"><img src="https://img.shields.io/github/v/release/BrandonVee/TokenRouter?display_name=tag" alt="Release" /></a>
    <a href="https://github.com/BrandonVee/TokenRouter/pkgs/container/tokenrouter"><img src="https://img.shields.io/badge/container-ghcr.io%2Fbrandonvee%2Ftokenrouter-2496ED?logo=docker&logoColor=white" alt="Container" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-LGPL--3.0--or--later-4c1.svg" alt="License: LGPL-3.0-or-later" /></a>
  </p>

  <p><strong>简体中文</strong> | <a href="README_EN.md">English</a></p>
</div>

## 项目简介

TokenRouter 是一个自托管的 AI API 网关与管理平台，用于统一接入和管理多个上游 AI 服务。用户通过平台生成的 API Key 发起请求，平台负责鉴权、路由、账号调度、请求转发、用量统计和计费。

项目同时提供用户端和管理端 Web 界面，适合需要集中管理上游账号、分发 API 配额并统一运营 AI 服务的个人或团队。

TokenRouter 基于 [Sub2API](https://github.com/Wei-Shaw/sub2api) 持续开发，感谢上游项目及所有贡献者。

## 核心功能

- 多上游、多账号统一管理
- API Key、用户、团队和分组管理
- 模型映射、请求路由与故障转移
- 并发、速率和配额控制
- 用量统计、余额、订阅与计费
- 用户控制台、管理后台与运行观测

## 支持平台

TokenRouter 当前包含 Anthropic、OpenAI、Gemini、Antigravity、Grok / xAI 和 Qoder 六个平台适配器，详细支持范围见[上游账号能力矩阵](docs/interfaces/upstream_account_matrix.md)。

## 部署

项目支持安装脚本、Docker Compose、源码编译和 Apple container 等部署方式。

- [部署指南](docs/guides/deployment/index.md)
- [Docker 镜像说明](deploy/DOCKER.md)
- [Apple container 部署指南](docs/guides/deployment/apple_container.md)

## 文档

- [使用与运维指南](docs/guides/index.md)
- [接口文档](docs/interfaces/index.md)
- [工程文档](docs/index.md)

## 许可证

本项目依据 [GNU Lesser General Public License v3.0 或更高版本](LICENSE) 发布。

Copyright (c) 2026 BrandonVee
