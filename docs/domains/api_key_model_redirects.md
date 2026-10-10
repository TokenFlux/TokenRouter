# API Key 模型重定向

每个 API Key 可以保存一组独立的 `model_mapping`，把客户端提交的模型别名重定向到内部目标模型。规则只对当前 Key 生效，价格配置、分组和提供商的全局配置保持原样。

本文覆盖规则配置、匹配顺序、请求和响应的模型链，以及模型列表的展示。分组映射、提供商映射和供应商自己的模型别名见其他文档。

## 章节导航

- [配置接口](#配置接口)：修改 Key 创建、更新或校验时读取。
- [规则约束](#规则约束)：核对可接受的来源和目标。
- [匹配顺序](#匹配顺序)：修改精确/通配符解析时读取。
- [请求与响应](#请求与响应)：修改中间件、日志或响应恢复时读取。
- [复合 Key](#复合-key)：核对与前缀选组的先后顺序。
- [模型列表](#模型列表)：修改模型列表里可见的别名时读取。

## 配置接口

创建 API Key 时可传入：

```json
{
  "name": "review-key",
  "group_id": 12,
  "model_mapping": {
    "codex-auto-review": "gpt-5.6-luna",
    "team-preview-*": "gpt-5.6-luna"
  }
}
```

更新 API Key 时：

- 省略 `model_mapping`：保留已有规则。
- 传入 `{}`：清空全部规则。
- 传入非空对象：用新对象替换全部规则。

API Key 查询与列表响应会返回完整的 `model_mapping` 对象。

## 规则约束

- 最多 100 条规则。
- 来源和目标去除首尾空格后必须包含 1 至 100 个字符。
- 来源支持精确名称，或一个位于末尾的 `*` 通配符。
- 目标是一个具体的模型名，不含通配符。
- 来源区分大小写，去除首尾空格后不能重复。
- 来源与目标不能相同。

非法规则返回 HTTP `400`，错误码为 `INVALID_API_KEY_MODEL_MAPPING`。

<a id="redirect_order"></a>
## 匹配顺序

`internal/routing/modelmap` 是 Key 与提供商共同使用的纯匹配实现。Key 的配置校验和别名展示由 apikey 拥有；gateway 负责请求字段读取、改写时机和响应恢复，纯匹配包不接收提供商、Key 或请求对象。

每个模型在一次请求里只匹配一次，映射结果不再参与后续规则的匹配：

1. 精确规则优先。
2. 多个通配符命中时，来源前缀最长的规则优先。
3. 都没有命中时，模型名保持原样。

例如以下配置：

```json
{
  "codex-*": "gpt-5.6-sol",
  "codex-auto-*": "gpt-5.6-luna",
  "codex-auto-review": "gpt-5.6-luna",
  "gpt-5.6-luna": "another-model"
}
```

`codex-auto-review` 得到 `gpt-5.6-luna`，匹配到此结束，结果停在 `gpt-5.6-luna`。

## 请求与响应

重定向发生在 API Key 鉴权和复合 Key 选组之后、分组与提供商映射之前。JSON、multipart、Gemini URL、Responses 工具模型、异步媒体、批量图片、WebSocket 每轮模型及 Live `session.model` 使用相同规则。

客户端响应中的协议模型元数据会恢复为原始别名。正文中的同名文本不会被替换。用量记录中：

- `requested_model` 保存客户端提交的模型别名。
- `upstream_model` 保存实际上游模型。
- `model_mapping_chain` 保存去重后的映射链。

模型权限、提供商资格和计费，都从 Key 重定向后的目标模型开始计算，价格按目标模型匹配。

## 复合 Key

复合 Key 先使用完整的 `前缀/模型` 选择分组，再对去掉前缀的模型应用该 Key 的共享规则。

例如客户端请求 `GPT/codex-auto-review`，规则为 `codex-auto-review -> gpt-5.6-luna`，则选中 `GPT` 分组后向内部传递 `gpt-5.6-luna`，客户端响应仍展示 `GPT/codex-auto-review`。

<a id="model_list_projection"></a>
## 模型列表

`/v1/models`、`/models`、Gemini、Antigravity 与批量图片模型列表展示原本可请求的模型，再追加那些目标当前可以请求的精确别名。

- `codex-auto-review -> gpt-5.6-luna` 会同时展示 `gpt-5.6-luna` 和 `codex-auto-review`。
- 目标当前不可请求时不展示别名。
- 通配符来源不会被枚举为具体模型 ID。
- 复合 Key 返回带分组前缀的别名。
- Gemini 原生 `/v1beta/models` 从统一可请求目录筛选支持 Gemini 协议的模型，自定义列表仅取交集，再追加 Key 精确别名；别名继承目标能力元数据，原模型与顺序保留。单模型查询在有限目录中定位请求名称及其可能的精确别名目标，再对这些型号执行资格校验。复合 Key 按前缀定位一个绑定分组；分组权限、自定义列表和协议检查与列表一致。

保存规则时只校验格式，目标模型此时可以还没有可用的分组策略或提供商；实际请求时，按正常的路由错误返回。

相关文档：[路由与结算](routing_and_billing.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[领域目录](index.md)。
