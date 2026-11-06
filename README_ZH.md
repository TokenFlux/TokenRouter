<div align="center">
  <p>
    <img src="assets/readme/banner-zh.svg" width="100%" alt="TokenRouter，开源的大模型统一网关。Messages、Responses、Chat Completions 和 Gemini 请求从同一个入口进入，转发到 Anthropic、OpenAI、Gemini 等上游平台，上游出错时自动切换。" />
  </p>

  <p>
    <a href="https://github.com/TokenFlux/TokenRouter/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/TokenFlux/TokenRouter/ci.yml?branch=main&style=flat-square&label=CI&logo=githubactions&logoColor=white&labelColor=27272A" alt="CI" /></a>
    <a href="https://github.com/TokenFlux/TokenRouter/releases"><img src="https://img.shields.io/github/v/release/TokenFlux/TokenRouter?display_name=tag&style=flat-square&label=release&labelColor=27272A&color=3F3F46" alt="Release" /></a>
    <a href="https://github.com/TokenFlux/TokenRouter/pkgs/container/tokenrouter"><img src="https://img.shields.io/badge/container-ghcr.io-3F3F46?style=flat-square&logo=docker&logoColor=white&labelColor=27272A" alt="Container: ghcr.io/tokenflux/tokenrouter" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/license-LGPL--3.0--or--later-3F3F46?style=flat-square&labelColor=27272A" alt="License: LGPL-3.0-or-later" /></a>
    <a href="https://discord.com/invite/7XZzukhX4E"><img src="https://img.shields.io/badge/Discord-join-5865F2?style=flat-square&logo=discord&logoColor=white&labelColor=27272A" alt="Discord" /></a>
  </p>

  <p><strong>简体中文</strong> · <a href="README.md">English</a> · <a href="#插件系统">插件系统</a> · <a href="#快速开始">快速开始</a> · <a href="#文档">文档</a></p>
</div>

## 简介

TokenRouter 是一个开源的大模型统一网关。它把 Anthropic、OpenAI、Gemini 等多家厂商的模型服务接到同一个入口，用户用一个 API Key 就能调用不同厂商的模型。

> [!WARNING]
> 本项目允许在遵守开源许可证的前提下用于商业中转服务，但不得冒充 TokenFlux 官方站点，或通过名称、Logo、域名、界面及宣传造成官方运营、授权、合作或背书关系的混淆。建议商业运营者定制前端并清晰标明实际运营主体；对于侵权或造成上述混淆的行为，相关权利人保留依法要求整改的权利。详见[商业使用与品牌声明](BRANDING.md)。

## 界面预览

### 仪表盘

<img src="assets/screenshots/dashboard-dark.jpg" alt="TokenRouter 仪表盘 · 深色模式" width="1600" />

### 模型广场

<img src="assets/screenshots/models-dark.jpg" alt="TokenRouter 模型广场 · 深色模式" width="1600" />

<details>
<summary>浅色模式截图</summary>

### 仪表盘

<img src="assets/screenshots/dashboard-light.jpg" alt="TokenRouter 仪表盘 · 浅色模式" width="1600" />

### 模型广场

<img src="assets/screenshots/models-light.jpg" alt="TokenRouter 模型广场 · 浅色模式" width="1600" />

</details>

TokenFlux 官方运营站点：[tokenflux.com](https://tokenflux.com)。

## 核心能力

| 方向    | 可以做什么                                                                                                           |
|-------|-----------------------------------------------------------------------------------------------------------------|
| 模型接入  | 兼容 Anthropic Messages、OpenAI Responses、Chat Completions 和 Gemini 协议，分组允许的协议之间可以互相转换                             |
| 上游平台  | 接入 Anthropic、OpenAI、Gemini、Antigravity、Grok、Qoder、Kimi、智谱和 DeepSeek，支持 OAuth 账号、API Key、AWS Bedrock 和 Vertex AI |
| 调度与重试 | 按模型、协议能力、限流状态和粘性会话选择上游，上游出错时自动换下一个                                                                              |
| 访问控制  | 管理用户、团队、分组和 API Key 的额度、并发与 RPM。登录支持邮箱密码、Passkey、两步验证，以及 GitHub、Google、LinuxDo、OIDC、微信和钉钉                       |
| 计费与支付 | 按用量计费，支持余额、订阅套餐、额度包、兑换码和邀请返利。内置易支付、支付宝、微信支付、Stripe、Airwallex 和 EPUSDT（EPay 协议）                                                  |
| 内容审核  | 按关键词和哈希规则拦截请求，可以自动封禁违规用户                                                                                        |
| 管理后台  | 查看用量日志和统计、配置监控告警和邮件报表、在线升级和回滚，网页创作台可以生成和编辑图片。界面支持简体中文和英语                                                        |
| 插件扩展  | 添加用户页面、管理页面、HTTP 接口和定时任务，在后台安装、配置、更新和回滚插件 |

### 协议与接口

| 接口类型                    | 常用入口                                                                                              |
|-------------------------|---------------------------------------------------------------------------------------------------|
| Anthropic Messages      | `POST /v1/messages`                                                                               |
| OpenAI Responses / Chat | `POST /v1/responses`、`POST /v1/chat/completions`                                                  |
| Gemini                  | `POST /v1beta/models/{model}:generateContent`、`POST /v1beta/models/{model}:streamGenerateContent` |
| WebSocket               | `GET /v1/responses`、`GET /v1/realtime`                                                            |
| 向量                      | `POST /v1/embeddings`                                                                             |
| 图片                      | `/v1/images/generations`、`/v1/images/edits`、`/v1/images/batches`                                  |
| 视频                      | `/v1/videos/generations`、`/v1/videos/edits`、`/v1/videos/extensions`                               |
| 语音                      | `/v1/tts`、`/v1/stt`、`/v1/custom-voices`                                                           |
| 搜索                      | `/v1/web_search`、`/v1/x_search`、`/v1/alpha/search`                                                |
| 模型与用量                   | `GET /v1/models`、`GET /v1/usage`                                                                  |

详细见[上游提供商能力矩阵](docs/interfaces/upstream_provider_matrix.md)。

## 插件系统

插件可以为 TokenRouter 添加自定义功能，无需修改主程序代码。例如，[每日签到插件](https://github.com/TokenFlux/tr-plugin-checkin)提供用户签到页、余额奖励和管理员历史记录页。

- 在后台的**插件管理**（`/admin/plugins`）中，从内置目录或自定义来源安装，也可以上传 `.tar.gz` 安装包。安装前需审阅并确认插件申请的权限。
- 配置插件参数和操作额度，查看日志与资源用量，启用、禁用或重启插件。更新后可以回滚到上一版本；更新时新版本启动失败，会尝试恢复旧版本。
- 开发用户页面、管理页面、HTTP 接口和定时任务。获得对应宿主权限后，插件可以查询用量、调整余额、签发兑换码、发放订阅或发送邮件，每个插件有独立的 KV 存储命名空间。

Go 和 Web SDK 使用 MIT 许可证，支持后端进程和 Vue 页面开发。可以从[官方模板](https://github.com/TokenFlux/tokenrouter-plugin-template)和[插件开发手册](docs/guides/plugins/index.md)开始。

插件按管理员信任的代码运行，不提供操作系统或浏览器沙箱。目前不支持自定义模型提供商。安装、恢复和备份方法见[插件运维](docs/operations/plugin_operations.md)。

## 快速开始

使用 Docker Compose 部署：

```bash
mkdir -p tokenrouter-deploy && cd tokenrouter-deploy

# 下载 Compose 文件和 .env，并生成数据库密码和密钥
curl -sSL https://raw.githubusercontent.com/TokenFlux/TokenRouter/main/deploy/docker-deploy.sh | bash

docker compose up -d
```

启动后打开 `http://localhost:8080`。管理员密码可以在 `.env` 的 `ADMIN_PASSWORD` 里设置，没有设置时会随机生成并打印到日志：

```bash
docker compose logs tokenrouter | grep "admin password"
```

更多部署方式，见[部署指南](docs/guides/deployment/index.md)。

## 本地开发

后端使用 Go，前端用 Vue 3。从源码启动完整环境：

```bash
docker compose -f deploy/docker-compose.dev.yml up --build
```

环境要求、分别启动前后端的方法和测试命令，见[开发、验证与上游同步](docs/operations/development_workflow.md)。

## 文档

- [使用与运维指南](docs/guides/index.md)：部署、支付配置和外部系统集成。
- [工程文档](docs/index.md)：架构、业务规则、接口和运维约束，修改代码前从这里查。
- [升级说明](docs/operations/upgrade_notes.md)：各版本需要注意的变化。

## 许可证

本项目依据 [GNU Lesser General Public License v3.0 或更高版本](LICENSE) 发布。

`plugin-sdk/` 目录使用 [MIT 许可证](plugin-sdk/LICENSE)。

Copyright (c) 2026 Wesley Liddick & TokenFlux

## Star 趋势

<a href="https://star-history.com/#TokenFlux/TokenRouter&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=TokenFlux/TokenRouter&type=Date&theme=dark" />
    <img src="https://api.star-history.com/svg?repos=TokenFlux/TokenRouter&type=Date" alt="Star History Chart" />
  </picture>
</a>
