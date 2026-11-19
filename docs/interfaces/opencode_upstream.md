# OpenCode 上游

本文记录 OpenCode 提供商的按量与 Go 模式、按模型协议路由、端点与认证规则、管理测试及 Go 用量查询。分组协议准入见[统一协议能力](protocol_capabilities.md)，查询生命周期见[API Key 上游用量查询](upstream_usage.md)。

## 章节导航

- [模式与模型协议](#opencode_routing)：`payg`/`go` 模式、协议覆盖优先级、目录同步与规范身份解析。
- [地址、认证与转发规则](#地址认证与转发规则)：协议路径槽、Bearer 与会话头、Gemini/Responses/Messages 转发细节。
- [管理测试与 Go 用量](#管理测试与-go-用量)：同步安全边界、探针行为与 `opencode_go` 窗口查询。

<a id="opencode_routing"></a>
## 模式与模型协议

OpenCode 提供商固定为 `platform=opencode`、`type=apikey`，凭据存在 `credentials.api_key`。`provider_mode` 支持两种模式（缺省为 `payg`）：

| 模式 | 对应服务 | 默认服务根地址 | 上游用量查询 |
| --- | --- | --- | --- |
| `payg` | Console / Zen 按量 | `https://opencode.ai/inference` | 无（仅展示本地统计） |
| `go` | Go 订阅 | `https://opencode.ai/inference/go` | 固定适配器 `opencode_go` |

### 按模型协议选择

提供商可启用 Messages、Responses、Chat Completions、Gemini GenerateContent 与 SystemOne；实际候选须进一步匹配最终上游模型的原生协议（例如内嵌 models.dev 目录中 `minimax-m3` 在 `payg` 走 Chat、在 `go` 走 Messages）。模型名在提供商映射完成后按完整 ID 精确匹配。

两种模式各自保存独立的协议表，切换模式不复用另一模式的协议配置。`credentials.opencode_model_protocols`（人工覆盖）与 `credentials.opencode_catalog_protocols`（已接受目录）采用相同的两级 JSON 结构：

```json
{
  "payg": { "custom-model": "openai_responses" },
  "go": { "custom-model": "anthropic_messages" }
}
```

有效协议按以下顺序读取：

1. 人工覆盖（`credentials.opencode_model_protocols`）。
2. 管理员同步并保存的目录协议（`credentials.opencode_catalog_protocols`）。
3. 内嵌默认目录。

目录同步与规范身份规则：

- 同步建议与草稿：模型同步返回上游模型列表与当前目录建议 `model_protocols`（优先读在线目录当前模式表，未发布回退内嵌表；已发布空表以空表为准）。表单用建议替换当前模式目录草稿，`model_protocols: {}` 清空旧建议，缺省该字段则保留原草稿。
- 持久化与覆盖：删除人工覆盖后恢复目录选择，同步不回插数据库旧覆盖；仅同步不持久化，保存提供商后生效；目录未识别型号需手动指定协议。
- Canonical 属性身份：目录同时保存协议与规范属性身份，显式 `canonical_model_id` 优先，缺失时仅接受完整型号与公共模型 ID 完全一致且作者唯一的匹配（模糊名称保持未知）。上游服务模型 ID、属性身份与计费身份分别保存；属性解析优先读已刷新目录，内嵌目录仅提供默认协议。
- 编码与路由约束：编码规则（如 Claude adaptive thinking、OpenAI 采样条件）按已核验作者身份与协议选择，账号认证、配额与站点规则由提供商负责，渠道价格继续从默认价卡排除。
- 快照锁定与换号：`upstream_protocols` 为空时关闭新调用，SystemOne 保持独立同步 JSON 入口。候选从同一提供商快照一跳映射并锁定最终模型与协议，实际请求不可保留旧协议二次重映射；换号重新解析候选，自动探针同样校验目标模型已启用协议。

## 地址、认证与转发规则

### 端点与地址槽

`credentials.base_url` 可覆盖服务根地址，模型同步请求其 `/v1/models`。`api_base_urls` 地址槽保存含 `/v1` 或 `/v1beta` 的完整协议根地址（版本段由协议 URL 构造器规范化），覆盖地址同样经过出站 URL 校验并复用提供商代理与传输配置：

| 协议 | 根地址后默认路径 | `api_base_urls` 地址槽 |
| --- | --- | --- |
| Responses | `/openai/v1/responses` | `responses` |
| Chat | `/openai/v1/chat/completions` | `chat_completions` |
| Messages | `/anthropic/v1/messages` | `anthropic` |
| Gemini | `/google/v1beta/models/{model}:generateContent`（流式用 `:streamGenerateContent`） | `gemini` |
| SystemOne | `/systemone/v1/systemone` | `systemone` |

### 认证、会话头与协议细节

- 统一 Bearer 认证：所有协议使用 `Authorization: Bearer <api_key>`。执行顺序为先应用 Header Override，再由 OpenCode 认证步骤移除其他协议的 API Key 头并写入规范 Bearer。
- UA 优先级：User-Agent 按「客户端 UA > 提供商配置 > `TokenRouter`」选取。
- 会话头透传：按 `x-opencode-session`、`session-id`、`session_id`、`conversation_id`、`x-session-affinity`、`x-session-id` 顺序读取并转发为 `X-OpenCode-Session`，缺失时不生成随机 ID。其他 API Key 提供商指向 `https://opencode.ai` 时沿用相同会话头规则，认证保持不变。
- 平台身份与限制：OpenCode 身份贯穿使用记录、错误策略与运行监控（错误处理按实际失败提供商身份）。SystemOne 拒绝 `stream=true`；Messages `count_tokens` 使用本地估算；能否调用具体模型仍受账户权限与服务限制约束。
- Gemini 编码错误处理：普通失败走外层重试与换号；`429` 用共享健康策略与 Retry-After（不走 Google 日重置），同提供商重试等待上限 8 秒，提供商冷却记录完整 Retry-After。协议签名修复有界执行并响应取消；响应头超时算提供商失败，仅调用者 Context 取消/过期才记为调用者取消。
- 原生 Responses：转发客户端 `max_output_tokens`，保留 `store` 与 `previous_response_id`；续接请求仍校验响应归属与来源提供商绑定，不可移动的 HTTP 续接不切往另一套上游凭据（上游持久性以实际响应为准，未通过真实账户验证）。
- 原生 Messages 用量：保留 uncached input、cache read、cache creation 总量与 5m/1h 明细、output 及 speed；OpenAI 兼容输出使用 inclusive input，完成记录与计费保留原生桶以防转换丢失 TTL 或 speed。

## 管理测试与 Go 用量

### 表单配置、同步安全与测试

- 界面分工：创建/编辑在「基本信息」编辑 `provider_mode` 与 `base_url`，在「请求与协议」编辑 `api_base_urls`（与 Kimi、Zhipu、DeepSeek 共用控件，留空槽显示推导默认地址）；模型协议覆盖表仅列出已启用的原生协议。
- 同步安全边界：已保存提供商的同步只使用持久化 Key、地址、模式与代理，拒绝临时配置请求体（空体或 `{}` 仅作兼容），保证 MCP 同步仅访问已保存目的地。创建预览可传临时 Key、根地址与模式，并与已保存同步共用在途跟踪和取消。编辑已保存提供商时，修改 Key、地址或模式须先保存再同步（表单此时隐藏同步入口，防止混用草稿地址与持久化密钥）。
- 探针与测试：OpenCode 测试仅执行一次模型映射以确定最终模型和协议，再直接写入载荷与 URL。Messages、Responses、Chat、Gemini 走原生测试交换（Kimi、Zhipu、DeepSeek 的 Messages/Responses 测试共用），上游 `401` 写提供商错误状态。
- SystemOne 与分组探测：SystemOne 测试与 Jev 共用决策交换，`401` 不写错误状态；分组探测按探测模型解析出的协议判断 OpenCode 候选能否测试。

### Go 用量查询

- 查询配置：Go 模式使用固定适配器 `opencode_go`，携带 Bearer API Key 请求 `GET https://opencode.ai/zen/go/v1/usage`。查询根地址独立配置，留空用官方管理根地址，不复用自定义推理地址。
- 窗口校验：返回滚动 5 小时、周、月三个窗口，每个窗口须含有效 `percent`、`status` 与 UTC `resetsAt`；超过 100% 的已用比例保留，剩余额度下限为 0。
- 失败与缓存：异常响应、认证错误、未订阅、限流与超时显示为查询失败，不影响转发资格。单查与批查沿用身份复核、独立并发与 5 分钟浏览器缓存；不提供钱包余额查询或后台额度监控，不按窗口百分比自动停调，也不修改控制台超额使用设置。

参考：[官方推理 API](https://opencode.ai/v2/docs/console/api/inference/)、[Go](https://opencode.ai/v2/docs/console/go/)、[官方 Go 查询实现](https://github.com/anomalyco/opencode/blob/dev/packages/core/src/usage-limits.ts)。接入协议核验日期：2026-10-26。
