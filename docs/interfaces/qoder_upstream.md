# Qoder 原生上游

TokenRouter 通过 Qoder COSY 网关路径，支持 Qoder 原生的上游提供商。对外公开的别名会映射到 Qoder 的路由键，原始路由键也可以直接作为请求模型，用于兼容和运维。

本文说明 Qoder 的提供商、站点、模型能力、请求适配、定价、配额和失败处理。TokenRouter 共用的调度和账本规则见其他文档；代码里还没有实现的 Qoder 企业登录变体，不在支持范围内。

## 章节导航

- [提供商类型](#提供商类型)：修改凭据导入、OAuth、刷新或站点选择时读取。
- [客户端协议](#客户端协议)：修改 Messages、Responses 或 Chat 的准入时读取。
- [模型别名与映射](#模型别名与映射)：修改模型目录、路由键或限制时读取。
- [站点思考控制](#站点思考控制)：修改协议原生的推理控制时读取。
- [上下文窗口](#上下文窗口)：修改各站点的上下文能力或请求载荷时读取。
- [平台执行与输出](#qoder_execution_boundary)：修改执行器、流输出、凭据缓存或授权时读取。
- [计费范围](#计费范围)：修改 Qoder 的价格查找或零费用行为时读取。
- [上游提供商用量](#上游提供商用量)：修改配额探测或调度冷却时读取。
- [运维](#运维)：修改故障转移、错误分类或导入导出时读取。

<a id="qoder_account_contract"></a>
## 提供商类型

- `cosy` 提供商可以在国际站（`global`）或中国站（`cn`）使用 PAT 引导或设备 OAuth 凭据。
- 创建和导入时，要求 `platform=qoder` 和 `type=cosy` 互相对应；OAuth、API Key、Upstream、Bedrock 和 Service Account 都不能保存为 Qoder 提供商。
- 创建和编辑时的凭据校验，由 provider 的规则和 `provider/provider.CreateCredentialHooks` 组合完成；站点的 PAT 交换和机器身份准备使用原生的提供商记录，保留 CN 和 Global 的差异，并在调用时执行。
- `credentials.site` 选择站点。为了兼容已有的提供商，没有这个字段时按 `global` 处理。
- `credentials.refresh_mode` 记录令牌的来源。没有时按 `cosy` 处理；新版桌面授权在两个站点都使用 `device_token`；旧中国站 OAuth 的 `qodercn20` 继续保留。
- 手工导入可以只提供 `pat`，也可以提供一组现有的 COSY 令牌。
- 现有的 COSY 令牌凭据包括 `security_oauth_token`、`refresh_token`、`machine_id`、`machine_token`、`machine_type`、`uid` 或 `aid`，以及可选的组织元数据。
- 新版桌面 OAuth 使用所选站点的 OpenAPI `deviceToken/refresh`。轮换沿用已授权账号和组织，响应中携带的用户 ID 必须与绑定身份一致；首次授权从 userinfo 的顶层或嵌套 `organization` 读取组织 ID。需要补齐组织名称时，用该组织 ID 查询 tags。个人账号跳过 tags 请求。account plan 提供账号类型。轮询返回有效访问令牌即可完成授权；未返回刷新令牌时，到期后需要重新授权。旧国际站 COSY 凭据继续通过 Center 刷新，旧中国站 `qodercn20` 保留 OpenAPI 与 status 完成流程，手工 COSY 凭据保留 Gateway 刷新路径。PAT 会话根据原始 PAT 重建。
- 中国站支持标准 QODER_PAT 和新版桌面设备授权，旧 QoderCN20 凭据仍可刷新。企业专属域名的 `PERSONAL_TOKEN`、组织选择、AK/SK 和区域发现都不支持。

## 客户端协议

Qoder Cosy 的原生协议是 `qoder_chat`。分组开放 Messages、Responses 或 Chat 入口，且提供商启用了 `qoder_chat` 时，网关按分组的 `protocol_fallbacks` 选路。映射表里没有该入口时，网关自动匹配 `qoder_chat`。显式列出转换目标时，列表须包含 `qoder_chat` 才能选择 Qoder。将该入口设为 `[]` 时，只允许原生协议，Qoder 不参与该入口的调度。

分组可以开放哪些入口，见[统一协议能力](protocol_capabilities.md#group_protocol_routes)。管理员关闭全部文本协议时，用户"使用 Key"的界面显示没有可用的文本协议，网关在提供商选择和计费之前，返回对应协议的 `403`。

协议开关不影响 Qoder 的站点、模型路由、思考控制和提供商资格。Responses 子路径和 WebSocket 不属于 Qoder 的能力，启用 Responses 协议也不会开放它们。

<a id="qoder_account_catalog"></a>
## 模型别名与映射

模型来自所选账号的 Gateway `/api/v2/model/list`，只读取桌面客户端的 `app` 场景。响应中的 `enable` 决定该路由是否进入账号候选，推理时上游还会检查权益；BYOK 模型不进入 COSY 账号目录。

手动同步和默认模型清单为每个启用模型提供一个请求 ID。优先使用由显示名生成的唯一别名，例如 `cantus`；缺少可用显示名或名称冲突时使用原始路由键。账号快照同时记录服务端路由键，网关用它发出推理请求。已保存的原始键继续可用于显式映射。显示名和可确认的公共型号身份分别保存，动态档位不会由别名推算价格。

管理员在已保存提供商的「模型」页点击「同步上游支持的模型」时，网关会查询该账号，把成功结果存入 `provider.extra.qoder_model_catalog`。查询最多四个并发，同账号的并发请求合并，单次上游查询预算十五秒。快照跨重启保留，持续使用至下一次手动同步成功或账号身份改变。

有效空目录会清空自动候选；网络错误、认证失败和非法响应不会清空最近成功目录。快照记录站点、已知账号和组织。更换站点、账号、组织或 PAT 后旧观察失效。同一已识别账号轮换访问令牌时仍可使用有效快照；缺少账号标识时以凭据摘要区分。写入还比较查询前的完整提供商配置，拒绝迟到结果。正常提供商快照和 outbox 传播已接受的目录更新。

模型页将可用展示 ID 去重后加入白名单草稿，并把已有的对应内部键收敛为展示 ID，保留自定义项和通配项；点击「更新」后白名单生效。下方展示上游返回的所有模型，包括禁用项、推理档位、输入输出限制及上下文选项。管理端、公开目录、调度与请求转换共用账号快照。管理接口的 `qoder_model_catalog_status` 和同步接口的 `sync_status` 都包含 `model_details`。

已移除模型不再提供自动候选或旧别名，管理员保存的配置不被自动改写。白名单筛选账号可用的路由。上游移除路由或别名后，已保存的白名单项保留并提示缺失。管理员可以通过显式提供商映射指定自定义上游路由。手动映射到当前可用路由仍然有效；已观察到但被账号禁用的路由不会被手动映射解禁。

Qoder 提供商的 `model_mapping` 和其他平台使用相同的改写规则：

- 键：这一层路由接受的模型名。
- 值：最终的 Qoder 路由或上游模型名。
- 映射本身不限制可以请求的模型范围。

需要把提供商限制在特定的最终路由或上游模型上时，使用 `model_whitelist`。网关先应用映射，再检查白名单；没有配置白名单的提供商不受限制。分组级的映射同样只改写一步，所以不要配置 `custom -> 公共别名 -> 路由键` 这样的别名链，直接配置 `模型 -> 上游路由键`。

## 站点思考控制

协议基线是桌面客户端 `0.5.14` 内嵌的 `1.1.86-hotfix.5.3` 运行时。模型查询和推理均使用桌面应用身份 `Cosy-ClientType: 10`，场景和业务产品均为 `app`。运行时版本用于 OpenAPI User-Agent、`Cosy-Version`、签名载荷中的 `cosyVersion` 和推理报文的 `business.version`；桌面外壳版本不替代这些协议版本。

推理控制来自所选账号模型的 `thinking_config`、`efforts`、`supports_disabled` 和默认值。`low`、`medium`、`high`、`xhigh`、`max` 分别保留。缺省请求使用模型默认值；明确关闭优先于启用，不能关闭推理的模型收到关闭请求时返回参数错误。已支持的强度原样发送，缺失的中间档位提升到最近的更高可用档位，超出范围取最高档位；正数 budget 使用最高可用档位。只有开关的模型只发送开关。

各入口读取原生字段：Chat 优先 `reasoning_effort`，Responses 优先 `reasoning.effort`，Messages 读取 `output_config.effort`、`thinking.type` 和 `thinking.budget_tokens`。无效强度不构成明确控制；其余有效字段仍可生效，否则继承模型默认值。`enabled` 或 `adaptive` 没有 budget 时使用模型默认强度。分组属性可缩小可用档位。原生模型必须开启推理时，显式关闭请求返回 HTTP 400；上游的故障转移和监控只处理实际发生的上游故障。

## 上下文窗口

输入上限来自账号目录的 `max_input_tokens` 和 `context_config`，选择已声明的最大可用窗口，再应用管理员覆盖。目录的原始默认窗口仍保留在快照中。模型属性和请求报文使用同一观察；已退役型号不再继承曾经绑定到该路由键的上下文。

有上下文选项的路由将输入上限写入 `model_config.max_input_tokens`、`chat_context.extra.ideModelConfigOverride.max_input_tokens` 和 `parameters.context_length`。没有选项的路由只写模型上限。分组配置、账号观察和统一目录都缺少输入上限时，编码器使用 200,000；属性预览仍显示未知。

TokenRouter 不读取客户端声明的上下文上限。Chat Completions、Responses 和 Anthropic Messages 里的输出 Token 字段只控制输出。客户端自己维护模型目录、压缩阈值和截断行为；`/v1/models` 和 `/models` 不公开非标准的上下文元数据。

<a id="qoder_execution_boundary"></a>
## 平台执行与输出

`provider/qodermodel` 保存每个账号的模型索引和原生能力约束。预览、探测和推理按最终路由键、客户端协议和原生上游协议 `qoder_chat` 匹配分组属性。属性引用可以借用其他型号的字段，出站使用原模型的路由键、默认设置和协议限制。`upstream/qoder` 实现桌面协议、站点、签名、报文转换和会话历史。`Executor.Execute` 接收本次的协议、整理好的目标和同步输出接口；平台代码不读取 Gin、旧的提供商实体或配置对象。`gateway/provider.QoderRuntime` 持有平台执行器和会话存储（各只有一个），app 为 Chat 和其他入口绑定同一个实例；目标使用原生的提供商记录，令牌和客户端在调用时取得。主 Chat 链路直接使用 gateway 的执行器，Messages、Responses 和兼容的 Chat 尝试，通过 `gateway/httpapi.ForwardQoderAttempt` 同步输出，共用同一个运行时，没有另外的尝试循环。

三个入口均由客户端提交完整历史。Qoder Responses 支持省略 `store` 或传 `store:false`。请求使用 `store:true`、`previous_response_id`、`conversation` 或 `item_reference` 时返回 HTTP 400。多轮工具调用需在本轮 `input` 中同时提供原始消息、函数调用及其结果。响应 ID 用于标识本次输出和使用记录；Redis 粘性路由只绑定实际请求的会话键。网关只为原生会话匹配保留指纹和用量。

只用于手动导入和 opt-in 测试的本地凭据读取隔离在 `upstream/qoder/localauth`。macOS 桌面导入读取对应站点的 `auth.v1.dat`、`auth.machine-id` 和系统 Keychain，验证存储版本与站点；旧 CLI 文件仍可显式导入。凭据只在内存中解密，正常服务不会读取本机客户端登录资料。

推理报文使用新版客户端的规范 `content`：用户文本块放在 `content` 数组，工具结果保留字符串或文本块，文本请求不再生成旧的 `contents` 副本。`chat_context.text` 和 `originalContent` 使用字符串，旧 `aliyun_user_type` 提示保持空值；模型显示名及视觉标记来自本次账号观察。自动缓存标记写到输出副本，避免改变后续会话指纹使用的输入。

实际的写入和 Flush 由 HTTP 适配器负责，转换器逐段输出，整条 SSE 不会被聚合。`gateway/httpapi.QoderRequestMetadata` 复制本次的请求头，并整理出原来的 Key ID 和客户端标记，平台的会话键计算只在 upstream 执行。HTTP 测试连接同一个输出 Writer 和平台流实现。

工具能力取本轮实际生效的推理开关和档位。分组禁用工具调用时，三个文本入口都会移除可选工具声明并保留历史调用和结果；要求调用工具或指定函数会在联系上游前返回 HTTP 400。`allowed_tools` 指定的函数子集会按原声明顺序发往上游，`required` 也遵循同一检查。这些本地拒绝不参与提供商的成功率统计。

客户端输出按[统一用量口径](protocol_capabilities.md#client_usage_cache)呈现已观察到的缓存读取与写入。OpenAI 输入总量包含全部输入桶，Messages 保持普通输入与缓存分离；`cacheable_tokens` 只表示可缓存量，不当作缓存写入。当前 Qoder 解析没有生成非零写入桶，序列化器仅保留调用方已经提供的真实计数。

流里已经开始服务、并且收到了 usage 之后，上游如果继续报错，会同时返回部分结果和错误：完成入口用这些已经观测到的用量结算一次，同时返回失败响应、失败反馈并回滚会话；这个失败不会被记成成功的会话，系统也不会重新推理或估算缺失的用量。还没有开始服务、或者没有观测到用量的失败，不做这种部分结算。

解析器补上的工具类型 `function`，不作为工具调用的身份。并行工具流里，缺少 ID、索引和名称的参数片段，不会被写进任意一个工具；单个工具的参数片段，继续写入已有的调用，不会因为默认类型被拆成新的块。

准备和取得凭据之后、发起推理之前，检查原请求是否已经取消，取消后不再启动新的推理。已经进入上游的流式请求，和客户端的取消脱钩，在十五分钟的执行预算内收集尾部的 usage；非流式请求按取消传播。响应体由平台执行关闭，提供商和用户的 Lease 由请求编排负责释放。

### 授权与凭据缓存

提供商授权的十分钟会话、完成认领、pending 和成功重放，由 `provider.QoderAuthorization` 持有，`provider/provider` 只整理交换的结果。刷新资格和新旧凭据的合并在 provider，实际的站点交换在 upstream；持久化经过已有的刷新协调和身份 CAS。请求失败后，凭据身份的判断和刷新锁的等待也由 provider 负责：立即回读一次，之后每 100ms 轮询，总预算 3 秒；只有凭据确实轮换了才重试，旧凭据不会被当作刷新成功返回。供应商错误导致的限流或过载写入，由 `provider/provider` 执行，使用和请求取消脱钩的 5 秒预算，尽力而为，失败时不影响请求。

运行时的凭据缓存由 `provider.QoderSessions` 持有（只有一个），带身份世代和 90 秒的共享构建预算。`provider/provider.QoderTokenProvider` 负责凭据整理和供应商构建，`QoderTokenRefresher` 组合站点交换和提供商凭据的合并，传输复用 HTTP 池和 TLS 策略。单个等待者取消不影响其他等待者；应用停止时，取消共享的构建，等待已经进入的操作，并拒绝迟到的回填。各平台令牌和 Qoder 会话的失效，统一由 `provider.CompositeTokenCacheInvalidator` 调用各自的缓存接口，同时清理备用键，删除失败时只尽力而为。授权的 HTTP 实现在 `provider/httpapi`。

## 计费范围

Qoder 和其他平台使用同一套价格解析规则，有明确报价的公共模型名可以使用统一目录价格，原始路由键和动态档位没有目录价格时保持未定价。先按价格配置的 `billing_model_source`，选定请求模型、分组映射模型或上游模型，再为这个模型解析价格；三者之间不会互相查找另一个模型的价格。

1. 共享价格配置里手动填写的单价或有效区间优先。
2. 分组没有关联价格配置时，使用统一的目录价格。
3. 价卡只填了 Fast/Flex、Max 推理或分时倍率时，保留继承来的全部基础价格，只覆盖对应的倍率。
4. 手动价卡里没填的字段，按通用的回退规则处理，填 `0` 表示免费。空的价卡不会遮住目录价格。

模型市场、管理界面的默认价格填充和实际结算，都使用上面的规则。有目录价格的公开别名，可以正常展示和扣费；没有任何 token 基础价的路由键，显示为未定价。图片请求和其他平台一样，使用同一型号手动配置的目录图片报价。分组的模型白名单单独保存，模型权限和共享价格配置无关。

提供商统计优先使用独立的成本规则，没有命中时，和其他平台一样查询上游模型的目录价格，不使用用户的自定义价格，也不会因为 Qoder 的请求别名而禁止回退。Qoder 自定义统计规则按请求名、分组映射名、上游名的顺序匹配。

成功的零费用请求，同样写入完整的使用记录，并以零金额走完正常的订阅和余额结算流程。

## 上游提供商用量

Qoder 有独立的上游月度 Credits 配额。TokenRouter 只把它当作提供商的用量和容量信息，它和 TokenRouter 的用户余额、订阅相互独立。

新版 `device_token` 提供商用 Bearer 访问所选站点的 OpenAPI `/sash/api/v2/me/usage`。`displayMode=qoder` 表示 Credits 观察；`displayMode=enterprise` 表示由上游管理的外部用量页，不生成零余额或额度耗尽判断。浏览器只提供经过 HTTPS 校验的详情地址或回退地址。

旧提供商继续使用 Gateway `/api/v2/quota/usage`：国际站、CN PAT 和旧 `qodercn20` 使用 COSY 签名，导入的 CN COSY 会话使用 Bearer；CN 可附带已保存的组织 ID。

通用 Credits 余额只合计 `userQuota`、`addOnQuota` 和 `orgResourcePackage`。`dedicatedResourcePackages` 单独保留名称、余额、有效期和适用范围标签，不加入通用余额。存在仍可使用的专属 Credits 时，不因通用余额耗尽而停调整个提供商；具体路由资格继续由模型目录和实际请求错误决定。

新接口的 Credits 视图要求显式提供布尔字段 `isQuotaExceeded` 和 `isPlanQuotaProrated`。额度包有效期和范围标签同时支持蛇形与驼峰拼写。缺失必填字段或结构无效时，服务保留已有快照和调度冷却。

成功查询将额度及账号身份保存到 `provider.extra.qoder_quota_snapshot`。查询失败时，管理端仅在站点、账号与组织匹配时显示上次额度。同一已识别账号刷新令牌后仍可使用快照。升级前保存的额度缺少身份标记，首次成功查询后恢复降级展示。通用额度确已耗尽时沿用到 `expiresAt` 的调度冷却；`personal_standard` 的零配额占位结构不据此停调。请求错误码 `115`、`agentLimitResetTime` 或 HTTP 429 继续走正常限流处理。

## 运维

Qoder 以 `qoder` 平台键参与调度快照、错误透传、故障转移和管理端的平台用量视图。对于可以重试的上游故障，例如 Qoder 权益拒绝错误码 `112`、Agent 限制、429 或 5xx，网关可以在写出任何流式分块之前换到另一个提供商；流式输出开始后，只返回符合流协议的错误，不再换提供商。错误码 `112` 视为模型或提供商的权益拒绝，和认证令牌无关，所以不会触发令牌刷新。

管理端的提供商数据导出和导入，会保留 `qoder`、`cosy` 提供商和它们的凭据，用于备份和迁移。

相关文档：[上游提供商能力矩阵](upstream_provider_matrix.md)、[网关请求生命周期](../architecture/gateway_request_lifecycle.md)、[路由与结算](../domains/routing_and_billing.md)、[HTTP 接口](http_api.md)和[接口目录](index.md)。
