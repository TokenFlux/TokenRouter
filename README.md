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

## 部署方式

详细部署说明见 [DEPLOY_GUIDE.md](docs/DEPLOY_GUIDE.md)。

## 许可证

This project is licensed under the [GNU Lesser General Public License v3.0](LICENSE) (or later).

Copyright (c) 2026 Wesley Liddick & TokenFlux
