# Antigravity 上游

本文记录 Antigravity 平台的 OAuth 接入、Claude 与 Gemini 专用端点、协议转换、跨平台分组选号、模型额度与失败恢复规则。终端用户使用教程与未经验证的上游能力不在本文范围内；完整分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。

## 章节导航

- [提供商与凭据](#antigravity_account_contract)：OAuth 接入、已移除的静态接入、project 校验与隐私脱敏。
- [专用端点与协议适配](#专用端点与协议适配)：`/antigravity/*` 强制平台路由、协议转换链路、输出上限与 thinking 控制。
- [跨平台分组选号与模型额度](#跨平台分组选号与模型额度)：多平台同组、模型映射与白名单顺序、配额与计费归属。
- [失败与恢复](#失败与恢复)：RetryInfo、容量冷却、粘性换号计费与回归测试范围。
- [平台执行与提供商职责](#antigravity_native_execution)：流生命周期复用、令牌刷新预算、探测与错误观测顺序。

<a id="antigravity_account_contract"></a>
## 提供商与凭据

Antigravity 平台标识为 `antigravity`，仅支持 `oauth` 提供商：

- 授权与元数据：管理端通过 `/api/v1/admin/antigravity/oauth/*` 生成授权 URL、交换 code 或校验/刷新 refresh token，再由通用提供商创建/更新接口持久化。access token 在使用前自动刷新；project ID、订阅与 tier、privacy mode、额度和上游 User-Agent 存为提供商及运行时元数据。
- 原生协议与封装：协议目录声明平台适配版 GenerateContent，Messages、Responses 与 Chat 通过分组转换开放（见[统一协议能力](protocol_capabilities.md)）。上游请求发往 `v1internal` 端点，内容为 Gemini 格式并封装 `project`、`model` 与 `request` 外壳。
- 已移除静态接入：API Key 与历史 `upstream` 接入已移除，迁移 `286` 停用存量静态提供商并保留记录 ID、凭据、分组关联与历史用量引用。接入中转网关时应按对应协议创建 Anthropic 或 Gemini API Key 提供商（填完整网关前缀）；Google 官方 Gemini Key 使用 Gemini 提供商。
- 资格与隐私：standard-tier OAuth 缺少必需 project ID 时直接拒绝。导入、凭据刷新与批量导入后检查并设置适用隐私状态；凭据、refresh token、project ID 与上游内部标识永不暴露在客户端错误或模型列表中。

## 专用端点与协议适配

### 专用端点

专用路由在 API Key 鉴权前写入 `ForcePlatform=antigravity`，仅选择 Antigravity 提供商，同时完整执行请求体限制、client request ID、Ops 错误日志、endpoint 归一化、分组/团队/模型/协议权限及订阅与余额准入：

| 入口 | 客户端协议 | 说明 |
| --- | --- | --- |
| `GET /antigravity/models` | Claude 模型列表 | 返回当前 Key 与分组可请求的 Antigravity 模型 |
| `POST /antigravity/v1/messages` | Anthropic Messages | 转换并转发 Claude 请求 |
| `POST /antigravity/v1/messages/count_tokens` | Anthropic token count | 路由保留，当前返回 `404`，客户端改用本地估算 |
| `GET /antigravity/v1/models`、`/usage` | Claude 自省接口 | 模型列表与 Key 用量 |
| `/antigravity/v1beta/models/*` | Gemini v1beta | list、get、generate、stream、countTokens 等接口 |

Claude Code 将 base URL 设为部署地址的 `/antigravity` 并填写 TokenRouter Key；Gemini 入口的 `x-goog-api-key` 同样填写 TokenRouter Key。

通用入口与 `/antigravity/*` 均按最终分组执行协议门禁，Gemini GET 模型列表不受生成协议开关影响（见[统一协议能力](protocol_capabilities.md#group_protocol_routes)）。

### 协议转换与控制参数

- Anthropic Messages：经 Antigravity request transformer 转为上游 Gemini/内部格式并双向转回响应与 SSE，保留工具定义、tool choice、thinking、缓存断点、图片、usage 与停止原因；schema cleaner 清理不支持的 JSON Schema。`web_search` 保持已选模型，不强制改为 Gemini 2.5 Flash。
- OpenAI 兼容链路：通用 Chat Completions 与 Responses 选中 Antigravity OAuth 时经过以下双向桥接，每次 failover 从原始请求重建并刷新工具名回程映射，流开始后不换号：

```text
Chat Completions / Responses
  -> OpenAI-compatible normalized form
  -> Anthropic request
  -> Antigravity/Gemini upstream
  -> Anthropic response/stream
  -> original OpenAI protocol
```

- Gemini 原生链路：`/antigravity/v1beta/*` 保持 Google 错误与流格式，不经过 OpenAI envelope。
- 输出上限：Chat 输出转换上限 `64000`；Messages 的 Claude 路线为 `64000`、其他模型为 `65000`（均与目录值取较小值）；Gemini 直通读取自身能力。管理员覆盖与路线条件通过[统一解析器](model_catalog_and_marketplace.md#model_attributes)生效。
- Thinking 控制：转换器按解析出的预算或档位发 thinking 控制。已登记 Opus 4.6/4.7/4.8 的 adaptive 编码用 `24576` 预算，客户端 effort 单独转换；签名恢复清除禁用控制，预算恢复使用修正后预算。
- 模块归属：通用 Claude/Gemini 报文变体（`max_tokens`、metadata、tools 与 OpenAI 兼容报文分离）、schema 清理及流状态归 `protocol`；v1internal 外壳、身份补丁、原生 session ID、模型回退与流协议归 `upstream/antigravity`；授权、项目发现、令牌回填与健康归 `provider`，纯转换不反读平台状态。

## 跨平台分组选号与模型额度

- 跨平台同组：Antigravity 可与其他平台加入同一分组，无需混合调度开关；选择器依次校验成员、模型范围、协议路线、状态、额度与并发，`/antigravity/*` 强制 Antigravity 平台。
- 归属与失效：客户端协议决定响应格式，分组决定权限/倍率/策略，选中提供商决定上游认证与传输。提供商移出分组或资格变更后刷新共享快照与平台桶；既有会话仍受签名、会话隔离、粘性与缓存计费约束。
- 模型与身份补丁：模型名称与通用候选来自[统一模型目录](model_catalog_and_marketplace.md#model_catalog_resolution)，白名单为空允许任意型号；推理参数过滤、thinking 后缀与额度键由平台执行规则处理。身份提示词用通用 Antigravity 补丁，型号由请求 `model` 字段指定。
- 模型解析顺序（最终模型不进行二次提供商映射）：

1. 执行一次管理员配置的提供商模型映射（未命中沿用请求模型名）。
2. 附加本次请求的 thinking 后缀（若有）。
3. 校验最终模型白名单（仅允许 thinking 变体时基础名加 thinking 通过；仅允许基础名时 thinking 请求被拒）。

- 配额与计费归属：额度查询按「提供商 + 模型 scope」保存上游 reset 与 remaining（可含 AI Credits）；`429`/`503` 区分模型限流、credits 用尽与共享容量不足。上游提供商额度独立于用户余额、订阅和 Key 限额；使用记录 `platform` 记实际执行提供商，提供商成本与用户售价分别解析，选中 Antigravity 不改变用户价格。

## 失败与恢复

- 限流与重试：`RetryInfo` 等待较短时在同提供商有上限等待；模型限流较长时标记模型或提供商并请求换号。单提供商模式允许有总上限的退避重试，多提供商模式优先换号；Context 取消立即停止。
- 共享容量冷却：`MODEL_CAPACITY_EXHAUSTED` 在当前提供商有限重试，耗尽后按非空模型名写 10 秒进程内冷却，后续请求跳过容量重试（冷却写入前的并发请求仍可完成各自重试，快速轮换提供商只会继续命中同一共享容量池）。
- 粘性换号计费：粘性会话换提供商时，可将普通输入按 cache-read 计费以反映缓存失效成本，该标志传入结算输入。
- 凭据与脱敏：OAuth 刷新后仍被拒时，返回脱敏提示要求重新授权并核查 project ID，同时将提供商标为可恢复错误。仅白名单安全上游提示可透传；响应体日志受开关、字节上限与脱敏约束。
- 修改回归清单：调整适配器时须覆盖非流式/流式、Claude/Gemini/OpenAI 三种客户端格式、工具与 thinking、单/多提供商限流、移出分组后快照失效及用量归属。

<a id="antigravity_native_execution"></a>
## 平台执行与提供商职责

- 执行与探测边界：单次平台交换、普通/智能重试、credits 请求、进程内模型容量冷却与响应体关闭归 `upstream/antigravity`；提供商切换、付款主体与资金完成由网关编排。指定提供商探测复用平台重试且不占用户/提供商并发槽，探针 UA 通过显式请求字段传递（不走 Context 键），不记 Ops 错误也不改动粘性会话，与转发共用关停屏障。
- 共享流生命周期：Gemini、Messages、Chat、Responses 输出及非流式 SSE 汇总共用[Gemini 流生命周期](gemini_upstream.md#gemini_stream_lifecycle)（帧规范化、多候选终态、空事件保活、空闲截止与 5 秒用量尾帧上限）。
- 终态与保活边界：各协议保留自有内容转换器，收到 finish reason 先结束内容，待用量尾帧结束后再发最终 usage 与终态；尾部显式上游错误不可生成成功终态（保留已观测用量）。Gemini 原生不发 `DONE` 或注释心跳；保活见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。HTTP 提交、重试窗口关闭与语义输出分开判定，实际平台在请求期固定供完成器使用。
- 授权与刷新预算：OAuth 会话、交换后一次性删除、项目/套餐发现、隐私设置与校验归 `provider`（报文变体归 `protocol/google`）。token provider 保持 project 回填冷却、缓存键、8 秒请求刷新预算、15 分钟后台刷新资格与 CAS 兼容。
- 健康与关停：额度展示、credits/模型窗口与 `INTERNAL 500` 惩罚归提供商健康层；停止时取消在途操作并等待有限重试收尾，预算用尽报告未完成、不算排空成功。
- 错误观测优先级：模型窗口先于一般错误处理；`503` 共享容量不足不升级为提供商冷却；缺少模型信息的 `429` 回退使用最终模型并按提供商级默认规则处理。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)、[接口目录](index.md)。
