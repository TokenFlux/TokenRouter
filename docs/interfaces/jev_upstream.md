# Jev 与 SystemOne

本文记录 Jev（TypeSafe）提供商的认证凭据、SystemOne 决策请求与图片/Box 规范、故障转移与错误策略、用量异常与定价、模型同步及管理端测试。分组权限见[统一协议能力](protocol_capabilities.md)，价格解析见[模型目录与市场](model_catalog_and_marketplace.md)。

## 章节导航

- [提供商、认证与请求执行](#systemone_execution)：凭据配置、请求格式、图片与 Box 规则、故障转移与错误改写。
- [用量与价格](#systemone_usage)：token 记录、无效用量告警及 models.dev / 补充价目规则。
- [管理、测试与品牌展示](#管理测试与品牌展示)：模型同步、决策测试面板、余额查询边界与 TypeSafe 品牌。

<a id="systemone_execution"></a>
## 提供商、认证与请求执行

### 提供商与入口

- 提供商身份：`platform=jev`、`type=apikey`，品牌标识为 `typesafe`。`credentials.api_key` 保存 TypeSafe 或兼容上游 API Key，`base_url` 缺省为 `https://api.typesafe.ai`。出站使用 Bearer 认证，地址校验、代理与请求头覆写由共享传输组件处理。
- 协议与入口：客户端入口为 `POST /v1/systemone`，除 Jev 外也可路由至声明该模型支持 SystemOne 的 OpenCode 提供商（见[OpenCode 上游](opencode_upstream.md)）。Jev 默认启用 `systemone`，须在分组中开放入口；提供商协议集合为空时关闭新请求。
- 请求模式：支持同步 JSON 与 `multipart/form-data`，问题类型覆盖 `noul`、`choice`、`score`、`box`；传入 `stream=true` 返回 `400`。

### 请求结构、图片与 Box 规则

- 必填字段：请求须含 `model`、`state`（字符串、对象或数组）与非空 `questions`。`instructions` 可省略或为 `null`；`noul` 缺 `instructions` 时至少需一个非 `null` 条件；`choice` 与 `score` 允许 `null` 条件描述。
- 内容审核：从 `state`、`instructions` 与 `criteria` 提取 JSON 解码后的键值（转义字符按明文匹配，字面反斜杠按一次解码处理）。
- 模型解析链：按「复合 Key -> Key 重定向 -> 分组映射 -> 提供商映射」替换顶层 `model`（multipart 改写首个 `request` 分段，保留二进制图片、boundary 与 `Content-Type`；重试从原输入重建）。
- 响应模型恢复：直连与管理员测试展示上游具体版本；配置别名时响应恢复客户端请求名（复合 Key 含分组前缀），`upstream_response_model` 存恢复前型号，计费按配置选用请求、分组映射或出站型号。
- 图片引用与 JSON 编码：图片放在顶层 `assets`，以唯一引用字符串关联 `state`/`instructions`/`criteria` 中的字符串值（不替换对象键）。JSON 图片用 `format=png/jpg/jpeg` 与带填充 `data_base64`（无 data URL 前缀或空白）。
- Multipart 图片：首个分段须命名为 `request`（`application/json`），`assets[引用].part` 指向唯一文件分段（每文件恰被一处引用，与 `data_base64` 并存时 `part` 优先）。
- 大小上限：网关限制解压后请求 ≤`32 MiB`，单张原始或 base64 解码图片 ≤`16 MiB`；实际格式、尺寸与展开总量由上游校验。
- 实验能力头：图片（Jev 1.13/1.14）与 Box（需 Jev 1.14）为 TypeSafe 实验功能，公开请求须显式带 `TypeSafe-Danger-Zone-Images: true`（网关原样转发、不自动补写，保留提供商头覆写优先级）。
- Box 约束：Box 无 `criteria`，返回归一化 `top`/`bottom`/`left`/`right`（在 `0` 到 `1` 间且 `top < bottom`、`left < right`）与必填 `confidence`（数值或 `null`）；Box 无「未找到」状态，需判存在性时配合 Noul。

### 故障转移、池模式与错误策略

模块分工上，`protocol/systemone` 负责结构校验与模型改写，`upstream/jev` 执行单次 HTTP 请求，`gateway/systemone` 管理换号与完成资格。

| 场景 | 处理规则 |
| --- | --- |
| 准入与计次 | 价格预检为必需步骤；每次换号重新校验协议、模型与价格。RPM 仅在首次准入累计一次，等待后与换号前资金复查不重复计 RPM；有效答案即使写客户端失败仍提交用量 |
| 普通故障转移 | `422` 参数错误直接交付；认证失败、限流与可恢复服务错误在应用提供商策略后进入有限换号。`429`/`529` 的 `Retry-After` 参与冷却并在重试耗尽时传给客户端；客户端取消、响应已交付或成功响应损坏时停止重试 |
| 池模式原地重试 | 命中配置状态码时先在同提供商原地重试（持有租约、每次重试前复查资金与价格、按默认退避并等待 `Retry-After`，取消立即释放租约），耗尽后再占换号预算；命中管理员停调策略直接换号 |
| 最终错误策略 | 按实际选中提供商平台（`jev` 或 `opencode`）匹配管理员错误规则（覆盖重试耗尽与 `422` 等直返错误）。规则改写后的错误返回未压缩 JSON（`Content-Type: application/json; charset=utf-8`，移除上游 `Content-Encoding`）；直返未命中则交付上游报文；开启自定义错误码但未命中时返回通用 `500` |

<a id="systemone_usage"></a>
## 用量与价格

- Token 记录与价卡：分别记录 `usage.input_tokens` 与 `usage.output_tokens`。TypeSafe 官方输出零价由价卡配置，输出 token 仍写入使用记录；计费模型来源、用户倍率、订阅与提供商成本沿用现有规则。
- 无效用量容错：成功响应的用量缺失、为负、类型错误或超出整数范围时，答案照常交付；网关记录 `systemone.usage_invalid` 告警与关联请求的 `usage_unknown` 尝试，跳过资金结算与正常账单，并结束本次尝试。合法零用量按正常响应处理。
- 目录与补充定价：models.dev v2 离线快照保留 Jev 原厂属性与报价；`model_supplements.json` 补充 `jev-latest`、`jev-preview`、`jev-1.13.0`、`jev-1.14.0` 的直连价、缺失属性与核验来源（目录明确值优先；渠道价可按完整 ID 配置，显式 `0` 有效）。
- 模态声明：输入模态为文本与图片，输出在模态分类中标为 `text` 并声明 `structured_output`。

## 管理、测试与品牌展示

- 模型同步：调用 `GET /v1/models` 解析 `models[].name` 并合并至表单供管理员保存白名单（新建提供商白名单为空）；公共模型列表按统一格式声明各模型可用协议。
- 决策测试与探测：手动测试、定时测试与分组探测共用 Jev 执行器（定时与分组探测发最小 Noul 请求）。手动测试默认 `jev-latest`，支持文本/JSON 状态、PNG/JPEG 上传（校验大小与宽高，阻止未引用或残留引用提交）及四类问题，单模型与批量共用问题集。
- 测试结果面板：与文字/图片测试共用弹窗及结果/JSON/日志面板，按提交快照渲染卡片（Noul 显正反概率，Choice 标选并排序，Score 显连续分、等级概率与 legend，Box 显定位框、四坐标与置信度或「未提供」，无效/缺失用量显未知；修改输入后标明展示的是上次结果）。
- 管理测试接口：传 `test_type=decision` 与 `systemone={state, questions, assets?}`，`model_id` 经提供商映射覆盖模型；后台图片/Box 测试显式附带 `TypeSafe-Danger-Zone-Images: true`，预览名与目标关联不进上游 JSON。
- 余额查询与品牌：TypeSafe 公开 API 与官方 SDK 无余额查询接口（核验日期：2026-10-11），Jev 余额查询默认关闭（兼容中继可配已有适配器）。`TypeSafe` 品牌用于分组品牌选项、模型图标（取自官方文档 favicon）、市场筛选与 SystemOne 展示；分组展示品牌优先 `display_brand`、空值回退分组名，执行平台取提供商配置。

参考：[图片](https://docs.typesafe.ai/images)、[Box](https://docs.typesafe.ai/primitives/box)、[TypeSafe API](https://docs.typesafe.ai/api)、[TypeSafe SDK](https://github.com/typesafe-ai/typesafe-sdk-js/blob/main/src/client.ts)、[官方模型与价格](https://docs.typesafe.ai/models)、[models.dev Jev](https://models.dev/models/typesafe/jev-latest/)。
