# Grok / xAI 上游

本文记录 Grok 平台的提供商凭据、客户端协议转发、图片与视频媒体能力、搜索与 Voice、异步视频归属结算、模型规则及运行时配置。xAI 套餐价格不在本文范围内，完整平台分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。

## 章节导航

- [提供商与认证](#grok_account_contract)：OAuth 与 API Key 凭据、端点模式、档位判定及凭据恢复预算。
- [协议与转发规则](#协议与转发规则)：原生与转换入口、SSE 保活、Responses WS 与执行观测边界。
- [媒体、搜索与语音](#媒体搜索与语音)：图片/视频格式与资格、Web/X 搜索、Voice/Realtime 及异步视频结算。
- [模型、调度与错误处理](#模型调度与错误处理)：模型属性、未知缺价、调度器接入、CAS 健康与 `grok_missing_usage`。
- [客户端与进程配置](#客户端与进程配置)：CLI 配置生成、`provider.grok` 参数与 Free 软配额门禁。

<a id="grok_account_contract"></a>
## 提供商与认证

Grok 平台名固定为 `grok`，正式支持两类提供商（其余通用类型无正式转发实现，`cosy` 仅属 Qoder，见[上游提供商能力矩阵](upstream_provider_matrix.md)）：

| 类型 | 凭据来源 | 端点与默认行为 |
| --- | --- | --- |
| `oauth` | 浏览器授权、refresh token 或 SSO cookie 换取的 Build OAuth token | 文本按 `grok_default_base_url_mode` 选 CLI 代理/公共 API/区域 API；官方 CLI 主机在媒体请求时自动切到官方媒体 API |
| `apikey` | xAI API Key | 默认上游地址 `https://api.x.ai/v1`，支持自定义 base URL |

凭据、端点与档位规则：

- OAuth 会话与临时输入：state 与 PKCE 会话优先存 Redis 并带一次性消费标记防并发重复兑换，仅在 Redis 写入失败时回退进程内短期存储。SSO cookie、邮箱密码等临时输入只用于兑换 Build OAuth token，不写入凭据、响应或日志。
- 密码授权开关：由 `provider.grok.password_auth_enabled` 控制，默认关闭且隐藏管理端入口；开启时仅临时将密码换 SSO 再换 OAuth token。重新授权成功后清除软消费上限重授权标记，并按凭据快照与 CAS 更新，防止旧请求覆盖新令牌。
- 自定义端点：手动配置的 base URL 优先于 `grok_default_base_url_mode`；所有自定义地址及媒体/账单子路径均经过统一 URL 白名单与 SSRF 校验（不影响官方授权与刷新端点）。
- 默认模型与已移除开关：`grok_default_text_model` 仅用于允许省略模型的请求，网关不自动将 `grok`、`grok-latest`、媒体简称或 Claude/GPT 改写为默认模型；已移除的 `grok_cross_client_model_map_enabled` 提交时返回 `400`。
- 档位判定：OAuth 访问令牌 JWT 的数字或字符串 `tier` 为首选信号（新 JWT 携带则覆盖，缺失保留旧值），账单月限额辅助确认付费档位；明确的 Free、SuperGrok Lite、SuperGrok Plus、Heavy 直接生效。
- `SuperGrokPro` 推断：模糊值 `SuperGrokPro` 仅在 24 小时内观测到 `grok-4.5` Responses Heavy 请求与令牌窗口时显示为 Heavy（其他模型快照沿用该结果但不刷新，过期或其他模型窗口不可升级）。管理端徽章与用量条共用规范化档位。
- 凭据恢复与健康预算：换号凭据预算 15 秒；条件写入 5 秒、提交确认 250ms、缓存清理 500ms，与运行时阻断联动。普通额度快照与 OpenAI 共用写入节流间隔，`429`、窗口用尽与成功恢复绕过节流，持久化采用「只延长限流」与「版本 CAS 恢复」。
- 模块归属：OAuth/SSO 交换与模型额度解析归 `upstream/grok`，报文归 `protocol/grok`，授权/刷新/恢复/健康归 `provider`。

## 协议与转发规则

主要网关入口包括 `/v1/responses`、`/responses`、Chat Completions、Messages、Responses WebSocket、`/v1/web_search`、`/v1/x_search`、`/v1/tts`、`/v1/stt`、`/v1/custom-voices` 和 `/v1/realtime`（Voice 与搜索同时支持不带 `/v1` 的别名）。

- 原生与转换协议：提供商原生协议集合为 HTTP Responses、Chat、Images、Video 和 Voice；Responses WebSocket、Compact、Web/X 搜索属于分组转换到 Responses 的入口，不作为提供商原生选项（见[统一协议能力](protocol_capabilities.md)）。
- 文本协议门禁：分组通过 `allowed_protocols` 控制 Messages、Responses 与 Chat（见[统一协议能力](protocol_capabilities.md#group_protocol_routes)）。禁用时在提供商选择、计费、重试和 fallback 之前返回对应协议 `403`。Chat 桥接不适用时回到原生 Chat 分支。
- 交换次数与观测边界：原生 Responses 在同提供商内保留一次密文/Compact 解码恢复；Chat 转 Responses 与图片辅助请求仅交换一次。HTTP 提交、关闭重试窗口、语义输出与 TTFT 相互独立；执行结果可与错误并存，失败结算按 Grok 入口规则判定，仅凭响应 ID、前导事件或部分输出不足以触发成功路径动作。
- SSE 保活：上游 `event: ping` 按通用规则在读取侧丢弃，下游连接由统一保活维护（见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)）；JSON `type` 声明业务事件时保留，未知字段、缓冲上限和不完整帧遵循保守放行。
- WS 与媒体隔离：Responses WebSocket 仅原生支持 Grok 与 OpenAI，兼容开关不扩展到其他平台；图片和视频受独立媒体资格与分组策略控制，与文本协议集合无关。

## 媒体、搜索与语音

### 图片与视频请求格式

- 能力校验与推理控制：图片请求校验最终模型输出模态，未知型号须先配 image 输出能力。Composer 可调推理取自[统一模型属性](model_catalog_and_marketplace.md#model_attributes)的 `reasoning_options`（空集清理，未知保留客户端值）；图转文与 `logprobs`/`stop` 限制由平台规则处理。
- 参考图字段兼容：JSON 图片编辑与视频生成可在 `image`、`images`、`reference_images` 和 `mask` 提供参考图。标准 xAI 用 `url`，兼容旧字段 `image_url` 并在转发前规范化为 `url`（并存时优先非空 `url`，空白时用 `image_url`）；multipart 上传文件转为 `url` data URL。
- 创作台 `generate` 与 `edit`：`generate` 走 `/v1/images/generations`；`edit` 走 xAI JSON `POST /v1/images/edits`（不用 OpenAI multipart）。单图用 `image: {"type":"image_url","url":"data:image/png;base64,..."}`，多图用 `images` 数组（最多 3 张源图）。
- 创作台字段保留：`edit` 保留 `model`、`prompt`、`resolution`、`aspect_ratio`，固定 `response_format: "b64_json"` 并从 `data[].b64_json` 解析输出。

### 媒体提供商资格与错误

| 场景 | 资格与处理规则 |
| --- | --- |
| API Key 提供商 | 始终具备新媒体生成资格 |
| OAuth 提供商 | 须有 xAI 计费探测给出的明确付费证据；Free、禁止访问、未观测、格式错误或结论不明确均不承接新媒体生成（仅出现每周用量周期不能证明付费） |
| 首次探测与覆盖 | 导入时主动探测，未观测 OAuth 在首次媒体转发前探测；管理员可通过 `extra.grok_media_eligible`（`true`/`false`/`null` 恢复探测，省略保留）覆盖 |
| 无合格提供商 | 媒体端点返回 HTTP `503`，错误类型 `grok_media_no_eligible_provider`（不影响聊天及已有视频状态查询） |
| 空图片响应与 `405` | 图片接口成功须至少含 1 张有效图片，空 HTTP `200` 触发 failover；端点返回 `405` 时未输出前换号，非池模式临时排除 30 分钟（公共池跳过默认冷却，`405` 不写模型级冷却） |

### 搜索与 Voice

- Web 与 X 搜索：`POST /v1/web_search` 与 `POST /v1/x_search` 仅限 Grok 提供商，接受查询或 `input`（最多 20 条），选号前先内容审计，最多尝试 4 个提供商。
- 搜索工具参数：`web_search` 走原生 Responses `web_search`；`x_search` 强制用 `x_search`，支持 `allowed_x_handles`、`excluded_x_handles`、`from_date`、`to_date` 及图片/视频理解开关。
- 搜索结果与计费：仅返回真实工具来源 URL 对应结果，计费模型记为 `grok-web-search` 与 `grok-x-search`。每次调用用独立服务端 request ID 作为结算幂等键（不合并相同查询/IP/UA）。Responses 与 Chat 保留原生 `x_search` 字段，并从 usage/工具事件恢复 `SearchCount` 作为 token 外附加费。
- Voice 与 Realtime 入口：覆盖 TTS、STT、自定义 Voice CRUD/下载及 `GET /v1/realtime`（代理 xAI Voice WS），仅限 Grok 提供商并在全会话持有并发槽。TTS 按字符数、STT 按音频时长生成 `AudioUsage`。
- Realtime 与附加计费：Realtime 须在任一方向观测到非空音频数据后才按连接时长计费（握手失败、纯文本或仅转录不收费；正常/常见断开照常结算已确认音频会话）。Voice 与搜索价格取自价格配置（`NULL` 用代码默认价，`0` 免费），叠加基础分组倍率，与文本 token 计价分离。

### 异步视频任务归属与结算

1. 任务绑定：新视频创建后按 `request_id` > `id` > `task_id`（含 `data.*`/`video.*` 嵌套）提取标识，按「规范化任务 ID + `user_id` + `api_key_id`」持久化分组与提供商绑定。后续查询与下载直连原提供商不重调度；即使复合 Key 映射被删，仍按绑定构造最小 Grok 分组视图（不要求新媒体资格，照常校验 Key/用户/归属）。
2. 安全下载：视频 content 先确认状态，再由服务端携带上游凭据代理下载并安全透传 Range 与内容头，不向客户端暴露上游 URL 或 bearer token。
3. 一次性结算：创建仅存快照（模型、计费模型、分辨率、时长、创建时间）不立即扣费；查询或下载首次见官方 `status=done` 且含 `video.url` 时，通过 Redis `SET NX` 抢占结算权（持久化失败时释放重试，用任务 ID 派生稳定 request ID 防重扣）。
4. 结算字段兜底：模型与时长优先取完成响应，分辨率取创建快照；时长缺失回退创建快照再用 8 秒，分辨率仅空值默认 `480p`。自定义标签（提交校验 ≤50 字符、无控制字符）与完整正数秒数直通计费（不限 15 秒）；缺模型用官方默认族。
5. 摘要与价卡：每次状态查询先于计费更新摘要，`done`/`failed`/`expired`/取消均记终态（含零费用失败与已计费再查，见[请求 ID 与请求查询](../operations/request_lookup.md#request_storage)）。媒体路由模型不影响账单模型；视频价卡按「分辨率档位 > 显式默认价 > 不可用 > 目录同型号同分辨率每秒价」解析，`video` 按「数量 × 时长」、按次按数量，叠加常规倍率（`2K` 保留图片几何与视频计费双义）。

## 模型、调度与错误处理

- 模型与推理属性：候选来自统一目录（空白名单允许任意 ID），文本与媒体用映射后完整模型 ID。推理属性由[统一解析器](model_catalog_and_marketplace.md#model_attributes)读 xAI 条目并叠加管理员覆盖：Grok 4.5/4.6/4.7 `xhigh`、4.3 `none` 来自目录；Build 0.1 与 4.20 reasoning 无可调档位（空集）；4.20 系列存在 `logprobs` 限制。
- 别名与属性应用：Messages 与 WS 在确定最终模型后应用属性再算缓存身份（已知无可调档位移除 `effort`，目录缺项保留传值）。`grok-latest`、`grok-build-latest` 及未登记别名按完整 ID 处理，缺项不猜能力。
- 定价边界：未知 Grok 文本型号无手动定价时保持缺价，不借用 `grok-4.6` 或其他型号价格；图片/视频按完整型号、尺寸与分辨率查目录报价；Voice 与搜索读手动操作价。
- 调度器接入：目标分组 `scheduler_type=advanced` 时，经模型、媒体、状态、配额与 transport 硬过滤后走通用 Top-K 评分；`basic` 按基础顺序选择。Grok 请求/令牌快照、媒体付费资格与 HTTP bridge 为可选平台信号，缺失不影响正常参与。
- 错误分类与 CAS：凭据失效、资格变化与限流采用凭据快照分类 + CAS 更新（比较快照为私有字段不进 JSON），旧请求无法封禁已刷新提供商。内容策略 `403`（不写提供商健康）、凭据 `401`/`403`、付费资格拒绝与可切换上游错误分开处理，仅响应未开始前的可切换错误换号。
- `grok_missing_usage` 检查：非流式 Grok Chat 响应须至少有一个正数聚合 token 桶（输入、输出、缓存写或读），缺失、全零或仅图片/文本明细的 200 在提交前返回 `grok_missing_usage` 故障转移错误。判断综合平台、计费模型、映射后模型与响应模型，避免中转绕过或误拒非 Grok 别名。

## 客户端与进程配置

- 客户端配置生成：API Key 页「使用密钥」支持生成 Grok Build CLI、Codex CLI 与 OpenCode 配置（合并前备份 `config.toml`）。Codex 用环境变量存 Key、设 `requires_openai_auth=false` 并用 HTTP/SSE Responses 关闭 WS（无需登录 ChatGPT）。
- Grok Build CLI 地址：配置指向 TokenRouter 对外 `/v1` 地址（非 `api.x.ai` 或内部 OAuth 代理），OAuth 流量默认走 Grok CLI 订阅代理。
- 进程配置 `provider.grok`：涵盖 OAuth（`oauth.authorize_url`、`token_url`、`client_id`、`scope`、`redirect_uri`）、API 地址（`base_url`）与验证码密钥（`yescaptcha_client_key`）。
- 版本与放宽开关：`cli_version` 须为无 `v` 前缀且无 build metadata 的规范 SemVer（代理传输继续校验最低版本）；`allow_unsafe_url_overrides` 与 `unsafe_allow_concurrency_above_one` 为独立显式放宽开关。
- 环境变量与密钥保护：上述参数在启动前发布为进程配置，不读取隐式 `XAI_*` 或 `YESCAPTCHA_*` 变量（v1 升级自动导入旧值）。配置中的 secret、token 与上游 URL 永不进入前端配置或错误响应。
- Free 软配额门禁：`scheduling.grok_free_quota` 控制明确标为 Free 的 OAuth 提供商本地滚动窗口软门禁（默认 24 小时、500000 token、95% 停调、统计缓存 60 秒）。未知/付费档位及统计失败放行（fail-open），首次未命中放行并后台刷新（普通链按链共享缓存，高级调度器按实例独立）；管理端查额度与导入探测绕过门禁。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)、[接口目录](index.md)。
