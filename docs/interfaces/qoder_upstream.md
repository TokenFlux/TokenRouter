# Qoder 原生上游

本文记录 Qoder（COSY 网关）提供商的站点凭据、协议路由、模型目录与映射、思考与上下文控制、平台执行边界、计费、上游 Credits 配额及运维规则。未实现的 Qoder 企业登录变体不在支持范围内；完整分类见[上游提供商能力矩阵](upstream_provider_matrix.md)。

## 章节导航

- [提供商与认证](#qoder_account_contract)：`cosy` 凭据约束、国际/中国站点、`refresh_mode` 与各模式刷新路径。
- [客户端协议](#客户端协议)：`qoder_chat` 选路、`protocol_fallbacks` 与 Responses 边界。
- [模型别名与映射](#模型别名与映射)：账号目录同步、快照失效、白名单草稿与映射顺序。
- [站点思考与上下文窗口](#站点思考与上下文窗口)：桌面协议基线、推理档位提升及输入上限注入。
- [平台执行与输出](#qoder_execution_boundary)：无状态历史、报文格式、工具门禁、部分结算与凭据缓存。
- [计费、上游配额与运维](#计费上游配额与运维)：价格解析、Credits 视图、错误码 `112`/`115` 与导入导出。

<a id="qoder_account_contract"></a>
## 提供商与认证

创建与导入时强制 `platform=qoder` 与 `type=cosy` 一一对应；OAuth、API Key、Upstream、Bedrock 与 Service Account 均不可保存为 Qoder 提供商。

| 字段 / 模式 | 说明与刷新路径 |
| --- | --- |
| `credentials.site` | `global`（国际站，缺失时默认）或 `cn`（中国站）；站点 PAT 交换与机器身份准备在调用时保留站点差异 |
| `refresh_mode=device_token` | 新版桌面 OAuth（两站通用），走所选站 OpenAPI `deviceToken/refresh`；轮换沿用账号与组织且响应 user ID 须与绑定一致 |
| `refresh_mode=cosy`（缺省） | 旧国际站 COSY 经 Center 刷新；手工导入的 COSY 令牌（`security_oauth_token`、`refresh_token`、`machine_id`、`machine_token`、`machine_type`、`uid`/`aid` 及可选组织元数据）经 Gateway 刷新 |
| `refresh_mode=qodercn20` | 旧中国站 OAuth，保留 OpenAPI 与 status 完成流程 |
| `pat` 引导 | 手工导入可仅提供 `pat`，会话按原始 PAT 重建 |

认证与站点边界：

- 新版桌面授权元数据：首次授权从 userinfo 顶层或嵌套 `organization` 读组织 ID，需补组织名时按组织 ID 查 tags（个人账号跳过），`account plan` 提供账号类型。轮询拿到有效访问令牌即完成授权；未返回刷新令牌时到期须重新授权。
- 中国站支持范围：支持标准 `QODER_PAT`、新版桌面设备授权与旧 `QoderCN20` 刷新；不支持企业专属域名 `PERSONAL_TOKEN`、组织选择、AK/SK 与区域发现。

## 客户端协议

- 原生协议与选路：Qoder Cosy 原生协议为 `qoder_chat`。分组开放 Messages、Responses 或 Chat 且提供商启用 `qoder_chat` 时，按分组 `protocol_fallbacks` 选路：映射表缺项自动匹配 `qoder_chat`；显式列出目标时须包含 `qoder_chat`；设为 `[]` 仅允许原生协议，Qoder 不参与该入口调度。
- 门禁与不支持子能力：分组协议开关见[统一协议能力](protocol_capabilities.md#group_protocol_routes)；关闭全部文本协议时「使用 Key」提示无可用协议，并在选号和计费前返回对应协议 `403`。协议开关不影响站点、模型路由、思考控制或资格；Responses 子路径与 WebSocket 不属于 Qoder 能力。

<a id="qoder_account_catalog"></a>
## 模型别名与映射

- 目录来源：模型来自所选账号 Gateway `/api/v2/model/list` 的桌面 `app` 场景。响应 `enable` 决定候选资格（推理时上游仍校验权益），BYOK 模型不入 COSY 目录。
- 展示别名与路由键：每个启用模型分配一个请求 ID，优先取显示名生成的唯一别名（如 `cantus`），缺显示名或冲突时回退原始路由键。快照同步保存服务端路由键供推理使用；已保存的原始键仍可用于显式映射。显示名与公共型号身份分开保存，动态档位不按别名推算价格。
- 手动同步与缓存：管理员在已保存提供商的「模型」页点击「同步上游支持的模型」后，结果存入 `provider.extra.qoder_model_catalog`（最多 4 并发、同账号合并并发、单次查询预算 15 秒），跨重启保留直至下次成功同步或账号身份变化。
- 失效与一致性：有效空目录清空自动候选；网络错误、认证失败与非法响应保留上次成功目录。快照记录站点、账号与组织，变更站点、账号、组织或 PAT 后失效；同一已识别账号轮换令牌仍有效（无账号标识时按凭据摘要区分）。写入前比对查询前的完整提供商配置以拒绝迟到结果，通过正常快照与 outbox 传播。
- 管理端模型页：模型页把可用展示 ID 去重写入白名单草稿，并将对应内部键收敛为展示 ID（保留自定义与通配项，点「更新」生效），同时展示含禁用项、推理档位、输入输出限制与上下文选项的全部模型（`qoder_model_catalog_status` 与 `sync_status` 均含 `model_details`）。
- 移除模型与手动映射：上游移除模型后自动候选与旧别名移除，但保留已存白名单项并提示缺失；手动映射可指向可用自定义路由，但无法解禁已被账号禁用的路由。
- 映射与白名单顺序：`model_mapping` 键为本层接受名、值为最终 Qoder 路由或上游名，本身不限制范围；用 `model_whitelist` 限制最终路由（先映射后查白名单，空表不限）。分组映射为单步改写，应直接配置 `模型 -> 上游路由键`，不要串联 `custom -> 公共别名 -> 路由键`。

## 站点思考与上下文窗口

### 协议基线与思考控制

- 协议基线：桌面客户端 `0.5.14` 内嵌运行时 `1.1.86-hotfix.5.3`。模型查询与推理使用 `Cosy-ClientType: 10`，场景和业务产品为 `app`；运行时版本填入 OpenAPI User-Agent、`Cosy-Version`、签名 `cosyVersion` 与推理 `business.version`（桌面外壳版本不替代）。
- 档位映射规则：控制能力取自账号模型的 `thinking_config`、`efforts`、`supports_disabled` 与默认值，保留 `low`、`medium`、`high`、`xhigh`、`max`。缺省用模型默认；明确关闭优先于启用，不支持关闭的模型收到关闭请求返回 HTTP `400`（不触发上游故障转移或监控）。
- 档位提升：支持的强度原样发送，缺失中间档位提升至最近更高可用档位，超范围或正数 budget 取最高可用档位；仅开关型模型只发开关。
- 入口字段读取：Chat 优先 `reasoning_effort`，Responses 优先 `reasoning.effort`，Messages 读 `output_config.effort`、`thinking.type` 与 `thinking.budget_tokens`。
- 无效值与默认回退：无效强度不构成明确控制（其余有效字段仍生效，否则用模型默认）；`enabled`/`adaptive` 无 budget 时用模型默认，分组属性可缩小可用档位。

### 上下文窗口

- 上限解析：输入上限取账号目录 `max_input_tokens` 与 `context_config` 声明的最大可用窗口，再叠加管理员覆盖（快照保留原默认窗口；退役型号不继承该路由键的历史上下文）。
- 字段注入：带上下文选项的路由将输入上限同时写入 `model_config.max_input_tokens`、`chat_context.extra.ideModelConfigOverride.max_input_tokens` 与 `parameters.context_length`；无选项路由只写模型上限。
- 缺失兜底：分组、账号观察与统一目录均缺输入上限时，编码器使用 `200,000`，属性预览仍显示未知。
- 客户端边界：TokenRouter 不读取客户端声明的上下文上限，三个入口的输出 token 字段只控制输出；`/v1/models` 与 `/models` 不公开非标准上下文元数据。

<a id="qoder_execution_boundary"></a>
## 平台执行与输出

### 执行边界与无状态历史

- 模块归属：`provider/qodermodel` 维护单账号模型索引与原生能力约束（预览、探测与推理按最终路由键、客户端协议与 `qoder_chat` 匹配分组属性；属性引用可借用他型号字段，出站仍用原路由键与限制）；`upstream/qoder` 实现桌面协议、站点、签名、转换与会话历史，不依赖 Gin 或旧实体。
- 本机凭据导入隔离：本机凭据读取隔离在 `upstream/qoder/localauth`（仅用于手动导入与 opt-in 测试，macOS 读站点 `auth.v1.dat`、`auth.machine-id` 与 Keychain 并校验版本与站点，旧 CLI 文件可显式导入；仅在内存中解密，正常服务不读本机登录资料）。
- 完整历史与 Responses 限制：三个入口均由客户端提交完整历史。Qoder Responses 仅接受省略 `store` 或 `store:false`；传入 `store:true`、`previous_response_id`、`conversation` 或 `item_reference` 返回 HTTP `400`。
- 会话键与工具轮次：多轮工具调用须在当轮 `input` 中同时提交原始消息、函数调用与结果。响应 ID 标识当次输出与使用记录，Redis 粘性仅绑定实际请求会话键，网关只为原生会话匹配保留指纹与用量。
- 规范报文：推理用新版规范 `content`（用户文本放 `content` 数组，工具结果保留字符串或文本块，文本请求不生成旧 `contents` 副本）；`chat_context.text` 与 `originalContent` 用字符串，`aliyun_user_type` 留空，显示名与视觉标记取自账号观察。
- 缓存标记与流式写出：自动缓存标记写到输出副本以防污染后续指纹；转换器逐段写出并 Flush，不聚合整条 SSE。

### 工具、用量与取消策略

- 工具门禁：能力取本轮生效的推理开关和档位。分组禁用工具调用时，三个入口移除可选工具声明并保留历史调用与结果；强制调用或指定函数在联系上游前返回 HTTP `400`（`allowed_tools` 按原顺序转发子集，`required` 遵循同一校验），本地拒绝不计入提供商成功率。
- 缓存用量口径：遵循[客户端用量与缓存](protocol_capabilities.md#client_usage_cache)（OpenAI 总输入含全部输入桶，Messages 分离普通输入与缓存；`cacheable_tokens` 非缓存写入，Qoder 当前不生成非零写入桶，序列化器仅保留调用方提供的真实计数）。
- 流内报错部分结算：流式已开始服务且观测到 usage 后上游再报错，返回部分结果与错误：按已观测用量结算一次、返回失败反馈并回滚会话（不标成功，不估算缺失用量）；未开始服务或无用量观测不做部分结算。
- 并行工具解析：解析器补全的默认类型 `function` 不作为工具调用身份；并行工具流中缺少 ID、索引和名称的参数片段不写入任意工具，单工具参数片段继续追加而不拆块。
- 取消与超时：拿到凭据后、发起推理前检查客户端是否已取消。已进入上游的流式请求与客户端取消脱钩，在 15 分钟执行预算内收集尾部 usage；非流式正常传播取消。响应体由平台执行关闭，提供商与用户租约由编排层释放。

### 授权与凭据缓存

- 授权与刷新协调：授权保持 10 分钟会话、完成认领及 pending/成功重放，持久化走刷新协调与身份 CAS。请求失败后的刷新等待为「立即回读一次 + 每 100ms 轮询，总预算 3 秒」，仅凭据确实轮换才重试，旧凭据不当作刷新成功。供应商错误触发的限流或过载写入与请求取消脱钩，采用 5 秒尽力而为预算，写失败不影响请求。
- 会话缓存与关停：运行时凭据缓存带身份世代与 90 秒共享构建预算，单个等待者取消不影响他人；应用停止时取消共享构建、等待在途操作并拒绝迟到回填。缓存失效时尽力同步清理备用键。

## 计费、上游配额与运维

### 计费范围

Qoder 共用统一价格解析规则（有目录价的公开别名正常计价，无目录价的原始路由键与动态档位保持未定价）。按 `billing_model_source` 选定请求模型、分组映射模型或上游模型后独立查价，三者间不互查：

1. 共享价格配置中的手动单价或有效区间优先。
2. 分组未关联价格配置时使用统一目录价。
3. 价卡仅填 Fast/Flex、Max 推理或分时倍率时，保留继承基础价并只覆盖对应倍率。
4. 手动价卡未填字段按通用规则回退，填 `0` 表示免费；空价卡不遮挡目录价。

模型市场、管理界面默认价与结算共用上述规则；图片使用同型号目录图片报价；分组模型白名单与价格配置无关。提供商统计优先用独立成本规则，未命中时按上游模型查目录价（不用用户自定义价，不因别名禁止回退）；Qoder 自定义统计按「请求名 -> 分组映射名 -> 上游名」匹配。零费用成功请求同样写入完整使用记录并走零金额结算。

### 上游提供商用量

Qoder 上游月度 Credits 仅作提供商容量参考，与 TokenRouter 用户余额、订阅独立：

- 新版查询协议：`device_token` 用 Bearer 查所选站 OpenAPI `/sash/api/v2/me/usage`。`displayMode=qoder` 为 Credits 视图，要求显式布尔值 `isQuotaExceeded` 与 `isPlanQuotaProrated`（有效期与范围标签兼容蛇形与驼峰，缺必填或结构非法保留旧快照与冷却）。
- 企业页与旧查询：`displayMode=enterprise` 为外部托管用量页，不判零余额或耗尽，浏览器仅展示 HTTPS 校验地址。旧提供商查 Gateway `/api/v2/quota/usage`（国际站、CN PAT、旧 `qodercn20` 用 COSY 签名，导入的 CN COSY 用 Bearer；CN 可附带组织 ID）。
- 余额汇总与停调：通用 Credits 仅合计 `userQuota`、`addOnQuota` 与 `orgResourcePackage`；`dedicatedResourcePackages` 单独保存名称、余额、有效期与范围标签，存在可用专属 Credits 时不因通用余额耗尽而停调整个提供商（具体路由资格仍由目录与实际错误决定）。
- 快照降级：成功查询将额度与账号身份写入 `provider.extra.qoder_quota_snapshot`；查询失败时仅在站点、账号与组织匹配时展示上次快照（同账号刷新令牌仍有效；升级前缺身份快照在首次成功查询后恢复降级展示）。
- 额度冷却：通用额度确已耗尽时沿用至 `expiresAt` 的调度冷却（`personal_standard` 零配额占位不据此停调）；请求错误码 `115`、`agentLimitResetTime` 或 HTTP `429` 走正常限流处理。

### 运维与故障转移

- 平台键 `qoder` 参与调度快照、错误透传、故障转移与管理端平台用量视图。
- 可重试上游故障（权益拒绝 `112`、Agent 限制、`429`、`5xx`）在写出首个流式分块前换号；流式输出开始后只返回流协议错误，不换号。错误码 `112` 属于模型或提供商权益拒绝，与令牌无关，不触发令牌刷新。
- 管理端提供商导入导出完整保留 `qoder`/`cosy` 提供商及其凭据用于备份和迁移。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)、[接口目录](index.md)。
