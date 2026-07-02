# Grok / xAI OAuth 使用说明

TokenRouter 支持通过 xAI OAuth 接入 Grok 订阅账号，并将 OpenAI-compatible Responses 流量转发到 xAI。

## 基本信息

- 平台名：`grok`
- 账号类型：OAuth 订阅账号
- 网关目标：`/v1/responses` 和 `/responses`
- 转发目标：`${XAI_BASE_URL:-https://api.x.ai/v1}/responses`

## 初始模型

- `grok-4.3`
- `grok-build-0.1`
- `grok-4.20-0309-reasoning`
- `grok-4.20-0309-non-reasoning`
- `grok-4.20-multi-agent-0309`

## 环境变量

- `XAI_OAUTH_CLIENT_ID`
- `XAI_OAUTH_SCOPE`
- `XAI_OAUTH_REDIRECT_URI`
- `XAI_OAUTH_AUTHORIZE_URL`
- `XAI_OAUTH_TOKEN_URL`
- `XAI_BASE_URL`

## 暂不支持

- Grok public Chat Completions
- image
- video
- TTS
- transcription
- browser automation
- cookies
- Grok web scraping
