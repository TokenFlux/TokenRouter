# 复合 API Key

复合 API Key 允许一个密钥绑定多个分组。每个映射由分组和前缀组成，请求通过 `前缀/模型 ID` 选择分组。

本文覆盖复合 Key 的配置、单请求选组、支持入口、错误和实时连接限制，不覆盖选组后的渠道/账号调度或单 Key 模型重定向规则。

## 章节导航

- [创建与更新](#创建与更新)：修改持久映射和校验时读取。
- [请求选组](#请求选组)：修改前缀解析和请求改写时读取。
- [支持入口](#支持入口)：新增协议入口时核对。
- [错误](#错误)：保持协议错误形状时读取。
- [实时接口限制](#实时接口限制)：修改长连接能力时读取。

## 创建与更新

创建复合 Key 时提交 `is_composite: true` 和完整的 `composite_groups`：

```json
{
  "name": "多平台开发",
  "is_composite": true,
  "composite_groups": [
    { "group_id": 10, "prefix": "GPT" },
    { "group_id": 20, "prefix": "Claude" }
  ]
}
```

更新接口对 `composite_groups` 使用完整替换语义。复合 Key 不能同时提交 `group_id`。从复合 Key 转回普通 Key 时必须提交 `is_composite: false` 和目标 `group_id`。

每个复合 Key 可配置 1 至 20 个映射。同一 Key 内分组不能重复，前缀按小写判重。前缀去除首尾空格后长度必须为 1 至 32，只能包含字母、数字、下划线和连字符。

<a id="group_selection"></a>
## 请求选组

以下示例选择前缀为 `GPT` 的分组，内部实际模型为 `gpt-5`：

```json
{
  "model": "GPT/gpt-5",
  "messages": [{ "role": "user", "content": "Hello" }]
}
```

前缀匹配不区分大小写。服务只按第一个 `/` 拆分，因此 `GPT/vendor/model` 的实际模型 ID 是 `vendor/model`。

Gemini 原生入口将前缀放在模型 URL 中：

```text
POST /v1beta/models/Gemini/gemini-2.5-pro:generateContent
```

服务在鉴权后移除前缀，再执行分组权限、回退、订阅、倍率、限流、账号选择和计费。使用记录明细的客户端模型名保留带前缀名称，内部模型和上游模型记录为去前缀名称；模型分布按内部请求模型聚合，因此普通 Key 与不同复合前缀请求同一模型时只形成一个统计项。

复合 Key 选择 `subscription` 结算模式时，所有前缀映射都必须位于该指定订阅套餐的分组范围内；配置页只展示用户权限与套餐范围的交集。每次请求在前缀选组、默认组或不可用分组回退完成后都会重验最终分组。套餐变更、旧 Key 配置或直接调用接口都不能借由另一条映射、默认组或回退组使用套餐外分组；指定订阅额度不足时也不得回退余额。

## 支持入口

- OpenAI、Anthropic 及兼容入口的 JSON 请求。
- 图片生成与编辑的 JSON 或 multipart 请求。
- 单模型批量图片提交。
- Gemini `/v1beta/models/<前缀>/<模型>:<动作>` 入口。
- 无模型的用量、视频任务查询和批任务管理接口；这些入口仍校验身份、用户与团队状态、IP 和资源归属，不重新选组消费。

`/v1/models`、裸 `/models`、Gemini 模型列表和批量图片模型列表会按映射顺序聚合可用模型，并在每个模型 ID 前添加对应前缀。

## 错误

缺少前缀、未知前缀或非法前缀均返回 HTTP 400，并使用对应入口的 OpenAI、Anthropic 或 Google 错误结构。主要错误码如下：

- `COMPOSITE_KEY_MODEL_PREFIX_REQUIRED`
- `COMPOSITE_KEY_PREFIX_NOT_FOUND`
- `COMPOSITE_KEY_PREFIX_INVALID`
- `COMPOSITE_KEY_PREFIX_DUPLICATE`
- `COMPOSITE_KEY_GROUP_DUPLICATE`
- `COMPOSITE_KEY_GROUPS_REQUIRED`
- `COMPOSITE_KEY_TOO_MANY_GROUPS`
- `COMPOSITE_KEY_ENDPOINT_UNSUPPORTED`

## 实时接口限制

复合 Key 不支持 `/v1/live`、Codex Realtime、Responses WebSocket 和 Live sideband。这些入口可能在同一连接中切换模型，服务会返回 `COMPOSITE_KEY_ENDPOINT_UNSUPPORTED`，不会自动选择默认分组。

相关文档：[API Key 模型重定向](api_key_model_redirects.md)、[路由与结算](routing_and_billing.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)。
