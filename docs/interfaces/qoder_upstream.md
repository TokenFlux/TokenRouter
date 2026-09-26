# Qoder 原生上游

Qoder Cosy 原生集合仅含 qoder_chat；公开 Messages/Responses/Chat 由分组明确映射到此协议。统一配置字段与入口门禁见[统一协议能力](protocol_capabilities.md)。

TokenRouter 通过 Qoder COSY 网关路径支持 Qoder 原生上游账号。面向请求公开的别名会映射到 Qoder 路由键，原始路由键仍可作为直接请求模型，以满足兼容和运维需要。

本文说明 Qoder 账号、站点、模型能力、请求适配、定价、配额和失败边界。TokenRouter 的共用调度与账本语义不在本文定义范围内；实现中尚不存在的 Qoder 企业登录变体也不在支持承诺内。

## 章节导航

- [账号类型](#账号类型)：修改凭据导入、OAuth、刷新或站点选择时读取。
- [客户端协议](#客户端协议)：修改 Messages、Responses 或 Chat 准入时读取。
- [模型别名与映射](#模型别名与映射)：修改模型目录、路由键或限制时读取。
- [站点思考控制](#站点思考控制)：修改协议原生推理控制时读取。
- [上下文窗口](#上下文窗口)：修改各站点上下文能力或请求载荷时读取。
- [计费范围](#计费范围)：修改 Qoder 价格查找或零费用行为时读取。
- [上游账号用量](#上游账号用量)：修改配额探测或调度冷却时读取。
- [运维](#运维)：修改故障转移、错误分类或导入导出时读取。

<a id="qoder_account_contract"></a>
## 账号类型

- `cosy` 账号可以在国际站（`global`）或中国站（`cn`）使用 PAT 引导或设备 OAuth 凭据。
- Qoder 创建和导入校验要求 `platform=qoder` 与 `type=cosy` 双向同时成立；OAuth、API Key、Upstream、Bedrock 和 Service Account 不能作为 Qoder 账号保存。
- 创建及编辑的凭据校验由 account 规则和 `account/provider.CreateCredentialHooks` 组合；站点 PAT 交换、机器身份准备使用原生账号记录，保留 CN/Global 差异与原调用时点。
- `credentials.site` 选择站点。缺失时为兼容已有账号而解析为 `global`。
- `credentials.refresh_mode` 记录令牌来源。缺失时解析为 `cosy`；中国站标准 OAuth 使用 `qodercn20`。
- 手工导入可以只提供 `pat`，也可以提供一组现有 COSY 令牌。
- 现有 COSY 令牌凭据包括 `security_oauth_token`、`refresh_token`、`machine_id`、`machine_token`、`machine_type`、`uid` 或 `aid`，以及可选的组织元数据。
- 国际站 OAuth 和手工 COSY 凭据通过国际站 Center 流程刷新。中国站标准 OAuth 先刷新 OpenAPI 令牌，再完成 `userinfo -> status`；中国站手工 COSY 凭据使用 Gateway 旧刷新路径。PAT 会话根据原始 PAT 重建。
- 中国站集成覆盖标准 QODER_PAT 和 QoderCN20 登录。不支持企业专属域名 `PERSONAL_TOKEN`、组织选择、AK/SK 和区域发现。

## 客户端协议

Qoder 分组支持 Anthropic Messages、OpenAI Responses 和 Chat Completions，新建默认值是空集合。迁移前已有分组按旧行为启用三项；与其它平台相同，管理员可关闭全部文本协议，此时用户“使用 Key”界面显示无可用文本协议，网关在账号选择和计费前返回协议原生 `403`。

协议开关不改变 Qoder 站点、模型路由、思考控制或账号资格。Responses 子路径和 WebSocket 仍不属于 Qoder 能力，即使 Responses 协议已启用也不会开放。

## 模型别名与映射

国际站公开别名：

- `claude-opus-4-6`
- `auto`
- `performance`
- `efficient`
- `lite`
- `qwen3.8-max`
- `qwen3.7-max`
- `qwen3.7-plus`
- `kimi-k3`
- `kimi-k2.7-code`
- `glm-5.3`
- `glm-5.2`
- `deepseek-v4-pro`
- `deepseek-v4-flash`
- `minimax-m3`

中国站公开别名：

- `auto`
- `qwen3.8-max`
- `qwen3.7-max`
- `qwen3.7-plus`
- `qwen3.6-flash`
- `deepseek-v4-pro`
- `deepseek-v4-flash`
- `glm-5.3`
- `glm-5.2`
- `kimi-k2.7-code`
- `minimax-m2.7`

账号模型列表和默认别名解析遵循 `credentials.site`。没有账号上下文的列表使用稳定并集，并把国际站模型排在前面。混合站点分组公开其可调度账号支持的并集，但站点专属别名和路由键不会调度给不兼容账号。显式账号映射仍是覆盖机制，未知原始路由键继续透传。

两个站点的 `qwen3.8-max` 都映射到正式路由 `qmodel_38max`。已移除的 `qwen3.8-max-preview` 别名和 `qmodel_preview` 路由不会被静默重定向。仍需使用旧请求名称的账号必须配置显式 `model_mapping`，例如 `qwen3.8-max-preview -> qmodel_38max`。

Qoder 账号的 `model_mapping` 与其他平台使用相同的重写规则：

- 键：该路由层接受的模型名称。
- 值：最终 Qoder 路由或上游模型名称。
- 映射本身不会限制可请求模型范围。

需要把账号限制到特定最终路由或上游模型时，应使用 `model_whitelist`。网关先应用映射，再检查白名单；未配置白名单的账号不受限制。渠道级映射同样只执行一步重写，不要配置 `custom -> 公共别名 -> 路由键` 这类别名链，应直接配置 `模型 -> 上游路由键`。

## 站点思考控制

站点能力快照已基于 Qoder 国际站和中国站 1.24.2 验证。该版本会通过 OpenAPI User-Agent、`Cosy-Version`、签名载荷中的 `cosyVersion` 和推理请求中的 `business.version` 传递。

能力查找发生在账号级模型映射和公共别名解析之后，因此直接映射到已知路由键的自定义请求模型会获得相同处理。国际站和中国站共用的路由键使用相同思考能力。未知路由键以及未经验证的国际站专属模型不会被修改。

| 站点 | 公开模型 | 路由键 | 思考能力 | 下游映射 |
| --- | --- | --- | --- | --- |
| 国际站 | `qwen3.8-max` | `qmodel_38max` | 仅开关 | 任意有效强度、启用/自适应开关或正数预算都会开启思考，不发送级别 |
| 国际站 | `qwen3.7-max` | `qmodel_latest` | 仅开关 | 与 Qwen3.8-Max 相同 |
| 国际站 | `qwen3.7-plus` | `qmodel` | 仅开关 | 与 Qwen3.8-Max 相同 |
| 国际站 | `deepseek-v4-pro` | `dmodel` | High / Max | Minimal、Low、Medium 映射为 High；High、Very High、Max 映射为 Max；任何正数预算映射为 Max |
| 国际站 | `deepseek-v4-flash` | `dfmodel` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 国际站 | `glm-5.3` | `gmodel` | Low / High / Max | Minimal、Low 映射为 Low；Medium、High 映射为 High；Very High、Max 映射为 Max；任何正数预算映射为 Max |
| 国际站 | `glm-5.2` | `gm51model` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 中国站 | `auto` | `auto` | 用户不可编辑 | 不覆盖 |
| 中国站 | `qwen3.8-max` | `qmodel_38max` | 仅开关 | 与国际站 Qwen3.8-Max 相同 |
| 中国站 | `qwen3.7-max` | `qmodel_latest` | 仅开关 | 与 Qwen3.8-Max 相同 |
| 中国站 | `qwen3.7-plus` | `qmodel` | 仅开关 | 与 Qwen3.8-Max 相同 |
| 中国站 | `qwen3.6-flash` | `q36fmodel` | 用户不可编辑 | 不覆盖 |
| 中国站 | `deepseek-v4-pro` | `dmodel` | High / Max | Minimal、Low、Medium 映射为 High；High、Very High、Max 映射为 Max；任何正数预算映射为 Max |
| 中国站 | `deepseek-v4-flash` | `dfmodel` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 中国站 | `glm-5.3` | `gmodel` | Low / High / Max | 与国际站 GLM-5.3 相同 |
| 中国站 | `glm-5.2` | `gm51model` | High / Max | 与 DeepSeek-V4-Pro 相同 |
| 中国站 | `kimi-k2.7-code` | `kmodel` | 用户不可编辑 | 不覆盖 |
| 中国站 | `minimax-m2.7` | `mmodel` | 用户不可编辑 | 不覆盖 |

网关从各入站协议读取原生控制字段：

- Chat Completions：读取 `reasoning_effort`，兼容回退到 `reasoning.effort`。
- Responses：读取 `reasoning.effort`，兼容回退到 `reasoning_effort`。
- Anthropic Messages：读取 `output_config.effort`、`thinking.type` 和 `thinking.budget_tokens`。

显式 `thinking.type=disabled` 或强度 `none` 始终优先。否则，显式有效强度优先于正数预算，其次是 `enabled` 或 `adaptive`；字段缺失或无效时保持关闭。可切换模型在关闭时使用 Qoder 的 `reasoning_effort=none` 覆盖，避免请求回退到上游默认值。虽然 Qoder 把 Qwen3.8-Max 的思考标记为默认开启，这一规则仍保持 TokenRouter 的显式控制契约。未知强度字符串会被忽略，不会拒绝请求。

## 上下文窗口

上下文查找发生在账号级模型映射和公共别名解析之后。每次请求都根据最终路由和已选账号站点选择经验证的最大上下文。故障转移选择另一站点账号时，会在重新构建 Qoder 载荷前重新计算能力。

| 站点 | 最大输入 Token | 路由键 |
| --- | ---: | --- |
| 国际站 | 1,000,000 | `ultimate`、`performance`、`qmodel_38max`、`qmodel_latest`、`qmodel`、`kmodel_latest`、`gmodel`、`gm51model`、`dmodel`、`dfmodel`、`mmodel` |
| 国际站 | 256,000 | `kmodel` |
| 国际站 | 180,000 | `auto`、`efficient`、`lite` |
| 中国站 | 1,000,000 | `qmodel_38max`、`qmodel_latest`、`qmodel`、`q36fmodel`、`dmodel`、`dfmodel`、`gmodel`、`gm51model` |
| 中国站 | 256,000 | `kmodel` |
| 中国站 | 200,000 | `mmodel` |
| 中国站 | 180,000 | `auto` |

存在官方运行时 `contextConfig` 的路由，会把所选上限写入 `model_config.max_input_tokens`、`chat_context.extra.ideModelConfigOverride.max_input_tokens` 和 `parameters.context_length`。最大值固定的路由只写入 `model_config.max_input_tokens`。未知、隐藏或已移除的原始路由键继续透传，使用保守的 200,000 Token 回退值，并且不会收到虚构的运行时上下文选择。

TokenRouter 不读取客户端声明的上下文上限。Chat Completions、Responses 和 Anthropic Messages 的输出 Token 字段仍只控制输出。客户端继续拥有自己的模型目录、压缩阈值和截断行为；`/v1/models` 和 `/models` 不公开非标准上下文元数据。

<a id="qoder_execution_boundary"></a>
## 平台执行与输出边界

Qoder 原生客户端、站点/模型能力、签名、报文转换和会话增量状态由 `upstream/qoder` 唯一拥有。`Executor.Execute` 接收本次协议、已投影目标和同步输出端口；平台不读取 Gin、旧账号实体或配置对象。`gateway/provider.QoderRuntime` 唯一持有平台执行器与会话存储，app 为 Chat 和其余入口绑定同一实例；目标使用原生账号记录，令牌与客户端仍按原时点取得。主 Chat 链直接使用原生 gateway 执行器，Messages/Responses 及兼容 Chat 尝试通过 `gateway/httpapi.ForwardQoderAttempt` 同步输出，继续共享同一运行时，不另建尝试循环。

只供显式导入和 opt-in 测试使用的本地凭据读取隔离在 `upstream/qoder/localauth`，正常服务不自动读取本机 Qoder 登录资料。

HTTP 适配器拥有实际写入和 Flush，转换器逐段输出，不聚合整条 SSE。`gateway/httpapi.QoderRequestMetadata` 复制本次请求头并投影原 Key ID 与客户端标记，平台会话键计算仍由 upstream 唯一执行。HTTP 边界测试连接相同的输出 Writer 与平台流实现。流中已发生服务并已收到 usage 后，上游继续报错时会同时返回部分结果与错误；完成入口使用这些已观测计量结算一次，保持失败响应、失败反馈及原会话回滚，不把失败绑定为成功会话，也不重新推理或估算缺失用量。尚未发生服务或未观测用量的失败不生成这类部分结算。

在准备和取得凭据之后、发起推理之前检查原请求取消，取消后不启动新的推理。已经进入上游的流式请求仍脱离客户端取消，在原十五分钟执行预算内收集尾部 usage；非流保持取消传播。响应体由平台执行关闭，账号与用户 Lease 由请求编排完成释放。

账号授权的十分钟会话、完成认领、pending 和成功重放由 `account.QoderAuthorization` 持有，`account/provider` 只投影原生交换结果。刷新资格及新旧凭据合并属于 account，实际站点交换属于 upstream；持久化继续经过既有刷新协调和身份 CAS。请求失败后的凭据身份判断和刷新锁等待也由 account 拥有，保留立即回读、100ms 轮询和 3 秒预算；仅凭据轮换后才重试，不返回旧凭据充当刷新成功。供应商错误到限流/过载的账号写入由 account/provider 执行，沿用脱离请求取消的 5 秒预算与尽力失败语义。

运行时凭据缓存由 `account.QoderSessions` 唯一持有，保留身份世代和 90 秒共享构建预算。`account/provider.QoderTokenProvider` 负责凭据投影及供应商构建，`QoderTokenRefresher` 组合站点交换与账号凭据合并，传输复用原 HTTP 池和 TLS 策略。单个等待者取消不影响其他等待者；应用停止会取消共享构建、等待已进入操作并拒绝迟到回填。各平台令牌及 Qoder 会话失效由 `account.CompositeTokenCacheInvalidator` 统一调用原缓存端口，不改变备用键清理或尽力删除语义。授权 HTTP 实现位于 `account/httpapi`，URL、管理员中间件、state 和冻结代理语义保持。

## 计费范围

Qoder 与其他平台使用同一套价格解析规则，不再要求公开别名或路由键必须手工定价。先按价格配置的 `billing_model_source` 选定请求模型、分组映射模型或上游模型，再对该模型解析价格；不会跨这三种身份寻找另一行价格。

1. 分组的显式单价或有效区间优先于共享价卡。
2. 没有分组显式价卡时使用共享价格配置；没有共享价格配置时使用内置模型价格。
3. 分组与共享价卡仅填写 Fast/Flex、Max 推理或分时倍率时，保留继承的全部基础价格，只覆盖对应倍率。
4. 显式价卡的未填字段沿用通用回退规则，显式 `0` 仍表示免费。空价卡不遮蔽内置价格。

模型市场、管理界面的默认价格填充和实际结算共用上述语义。有内置价格的公开别名可以正常展示并扣费；没有任何 token 基础价的路由键仍显示未定价。图片请求与其他平台一样使用通用图片计费回退。分组的模型白名单独立保存，模型权限与共享价格配置无关。

账号统计仍优先使用自定义规则，再按“应用模型定价到账号统计”开关决定是否采用客户计费基数；否则与其他平台一样查询上游模型的内置价格，不因 Qoder 请求别名而禁止回退。Qoder 自定义统计规则现有的请求名、分组映射名、上游名匹配顺序保持不变。

成功的零费用请求仍会写入完整使用记录，并以零金额走完正常订阅和余额结算流程。存在正数余额计费金额时，Qoder 仍计入用户与平台维度的美元配额。

## 上游账号用量

Qoder 有独立的上游月度 Credits 配额。TokenRouter 只把它作为账号用量和容量信息，它与 TokenRouter 用户余额、订阅以及用户与平台维度的美元配额相互独立。

账号用量界面会查询所选站点的 Gateway `/api/v2/quota/usage` 端点，并把最近成功快照保存到 `account.extra.qoder_quota_snapshot`。国际站请求始终使用 COSY 签名；中国站的 `qodercn20` 和 PAT 账号同样使用 COSY 签名，旧版或导入的 COSY 会话则按官方客户端行为使用 `security_oauth_token` Bearer 鉴权。中国站请求会在可用时携带缓存的 `orgId`，1.24.2 的常规配额查询不发送 `quota_key`。

实时查询失败时，管理界面可以同时显示缓存快照和降级用量错误。完整上游月度 Credit 余额是 `userQuota`、`addOnQuota` 与 `orgResourcePackage` 或 `sharedQuota` 之和，与 qodercli 用量视图一致。对于非个人零配额账号，`isQuotaExceeded=true` 或已耗尽的正数合计配额会把正常账号 `rate_limited_until` 调度信号设置到 Qoder 的 `expiresAt`；仍有附加或组织 Credit 时会阻止或清除过期配额锁。

观测到的 `personal_standard` 结构如果 `total=0`、`remaining=0` 且 `expiresAt` 极远，只用于展示，直到真实请求错误确认限制。请求时的错误码 `115`、`agentLimitResetTime` 或 HTTP 429 仍走正常账号限流冷却路径。

## 运维

Qoder 以 `qoder` 平台键参与调度快照、错误透传、故障转移和管理端用户平台用量视图。对于可重试的上游故障，例如 Qoder 权益拒绝错误码 `112`、Agent 限制、429 或 5xx，网关可以在任何流式分块写出前切换到另一账号；流式输出开始后只返回符合流语义的错误，不再切换账号。错误码 `112` 被视为模型或账号权益拒绝，而不是认证令牌故障，因此不会触发令牌刷新。

管理端账号数据导出和导入会保留用于备份迁移的 `qoder`、`cosy` 账号及其凭据。

相关文档：[上游账号能力矩阵](upstream_account_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口边界](http_api.md)和[接口目录](index.md)。
