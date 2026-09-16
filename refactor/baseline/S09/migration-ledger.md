# S09 迁移与交接账本

| 原实现 | 当前所有者 | 实际消费者与验证 | 状态/退出 |
| --- | --- | --- | --- |
| pkg/qoder 原生客户端、签名、站点、模型、OAuth、SSE | upstream/qoder | 原 service/repository/account 适配调用改为新路径；测试同批迁移 | 平台协议、账号授权与首条 Chat 链已接入；阶段全量验收待验 |
| pkg/qoder/local_auth.go | upstream/qoder/localauth | 仅原 opt-in 本地凭据/真实 API 测试，无生产消费者 | 保留显式选择，不自动读取磁盘凭据 |

| service/qoder_gateway_service.go 请求/流转换与会话状态 | upstream/qoder/{payload,stream_conversion,conversation,execute}.go | 旧公开入口别名/委托；新 Chat 经 Execute；私有白盒测试按类型引用改名，未删断言 | B01/B02 已接入；凭据缓存、授权、刷新职责已拆分到 account 与平台协议 |
| Qoder Chat 的请求循环 | gateway/qoder.go；gateway/httpapi/qoder.go | app/qoder_gateway.go + legacybridge/qoder_chat.go，server/routes 直接绑定 | 首条链行为测试已取得证据，门禁诊断收尾中；旧 Messages/Responses HTTP 保留 S11 入站循环 |
| 公共 token 计量值、会话标识原语 | upstream/{usage,session_identity}.go；protocol/anthropic/metadata_user_id.go | 旧 ClaudeUsage 别名、旧 hash/metadata 入口委托；计费实现未迁动 | 不把标识原语当成资金写入；其余平台后续改绑 |
| Qoder HTTP/完成适配 | handler/qoder_http_compat.go、app/legacybridge/qoder_chat.go | 只转换旧请求上下文、选号、观测与完成接口；不新建缓存或循环 | S10/S11 退出未迁规则、S15/S16 清理旧兼容 |

| service/qoder_token_provider.go 会话缓存/单飞 | account/qoder_sessions.go | 旧 GetSession/Invalidate 委托；app QoderCredentialSessions 在停止顺序 30 取消并等待 | 原 key/身份世代/90 秒共享构建预算保持；唯一实例 |
| service/qoder_token_provider.go 平台交换及 session 构建 | upstream/qoder/credential_builder.go | 旧入口投影 CredentialInput 与 RequestDoer；无完整账号或持久化客户端 | 已迁，旧装配投影 S11/S15 清理 |
| service/qoder_oauth_service.go 授权状态/完成认领/凭据映射 | account/qoder_authorization.go | account/provider/qoder_authorization.go 只绑定协议交换；旧 service 委托；Start/Stop 使用相同 Store | 十分钟 TTL、创建时代理冻结、pending/成功重放保持 |
| service/qoder_oauth_service.go OAuth 原生完成及警告 | upstream/qoder/authorization.go | account provider 投影原生 token 到账号契约；原测试全部保留 | CN/国际站失败差异与警告脱敏保持 |
| handler/admin/qoder_oauth_handler.go | account/httpapi/qoder_oauth.go | 原路由调用新 handler 别名；构造接受窄授权接口，无 service/具体平台依赖 | HTTP 状态及文案不变；旧构造 S15/S16 删除 |
| service/qoder_token_refresher.go | account/qoder_refresh.go + upstream/qoder/refresh_exchange.go | 刷新协调器仍使用同一旧接口，资格/合并委托 account，交换委托平台 | S06 CAS、回读与缓存失效未复制；刷新调度未改动 |
| SSE 写出观测 | upstream/qoder/output_observation.go | 同步标注完整帧；gateway.OutputTracker 独立记录进度与语义 | FirstSemanticOutput 不替换旧 TTFT 字段，不改变现有计费/日志字段 |


## S09.2 当前迁移（尚未完成验收）

| 原实现 | 唯一实现/兼容方式 | 生产与测试边界 | 退出 |
| --- | --- | --- | --- |
| pkg/claude、pkg/anthropicfp | upstream/anthropic 常量/归一化，原目录删除 | 所有实际 import 同批更新，版本环境仍初始化一次 | 已改绑 |
| pkg/oauth 状态与 wire | account/claude_authorization_sessions.go；protocol/anthropic/oauth.go；upstream/anthropic/oauth | 旧 pkg 只有别名/委托；状态白盒测试随 account 移动 | pkg 兼容 S15/S16 |
| service/identity_service.go | upstream/anthropic/request_fingerprint.go | 生产名 RequestFingerprintService；旧 IdentityService 别名；账号 masking 仅投影 | 旧包装 S15/S16 |
| repository/identity_cache.go | upstream/anthropic/rediscache/fingerprint.go | 原 Redis 键/TTL/JSON；原 integration 测试使用别名，不复制缓存 | 旧构造 S15/S16 |
| repository/claude_oauth_service.go、claude_usage_service.go | upstream/anthropic/{oauth_client,usage_client}.go | 原客户端白盒参数改为原生字段，断言保留；usage 值归 protocol | 旧构造 S15/S16 |
| service/oauth_service.go 的 Claude 用例、admin/account_handler.go 的 OAuthHandler | account/claude_authorization.go、account/httpapi/claude_oauth.go | 保留代理覆盖、scope、成功后删除会话、Cookie 组织回退；其它平台接口仍留旧混合文件 | 旧聚合 S11/S15 |
| gateway_claude_oauth_body、gateway_billing_block、gateway_tool_rewrite | upstream/anthropic 对应请求规则文件 | 旧方法只读取上下文/设置和委托；字段提升按 Go 类型引用操作；不删除断言 | 请求编排 S11 |
| gateway_upstream_response、gateway_anthropic_passthrough 的响应代码 | upstream/anthropic/{stream,stream_passthrough,response,execute}.go；protocol/anthropic/{usage_events,usage_patch}.go | 标准和直通分别保留，动态 TTL 回调仍按原事件读取；既有提交状态带入 | 通用错误/完成 S11 |
| GatewayService.Forward 的账号内恢复循环 | upstream/anthropic/{exchange,exchange_passthrough}.go | 5 次/10 秒等预算由旧调用投影；实际单次 Execute 已接入生产；账号切换仍在外层 | 通用投影 S11 |
| bedrock 请求、签名、区域规则与流帧 | upstream/bedrock | 旧 Account 只投影 RouteInput；域默认模型别名到唯一表；原 Forward 已使用 Execute | 旧包装 S15/S16 |
| SSE Scanner 缓冲、技术错误脱敏、context 等待 | infra/httpclient/sse_buffer.go、upstream/{stream_error,wait}.go | 保留唯一池、取消和计时行为；原函数委托 | 旧包装 S15/S16 |

| settings_view Beta 常量/值/默认规则、gateway_upstream_request 评估 | upstream/anthropic/{beta_settings,beta_policy}.go | 旧设置值别名；读取仍一次，空规则/未知 scope/模型白名单的原序保持 | 设置业务聚合 S10；旧构造 S15/S16 |
| Claude token provider、刷新政策/版本、Claude refresher | account/{claude_token,token_provider_policy,token_version,claude_refresh}.go | 旧接口只传 Record/端口，同一 cache/RefreshAPI；后台 CAS 仍由 S06 协调器拥有 | Vertex 技术交换在 S09.4 改绑；旧包装 S15/S16 |
| gateway_messages_cache、gateway_count_tokens 构造 | upstream/anthropic/{message_cache,count_tokens_request}.go | 标准/直通分别保留；旧调用只投影 URL/Header/动态设置；不增加直通策略检查 | HTTP/选择编排 S11 |
| gateway_request thinking/tool 修复与切片 | protocol/anthropic/repair.go | 平台适用性和拒绝的占位 signature 由旧调用显式传入；不引用 bridge/具体平台 | 旧模型资格 S09 对应平台/网关 S11 |
| Anthropic/Bedrock 原生 Execute 观测 | protocol/anthropic/observation.go、upstream 输出契约 | 同步报告 usage 是否实际出现、语义与终态；TTFT 与 FirstSemanticOutput 独立；旧调用不据新字段改变结算 | 其余入站调用方 S11 |
| anthropic_session 与 signature 错误匹配 | upstream/anthropic/{session_digest,signature_errors}.go | 系统/messages 字节投影；诊断返回原调用方记录；摘要 key/TTL 不变 | 旧上下文入口 S11/S15 |


### 本批可机器核对的清单

- [逐文件声明/测试名/构建约束](anthropic-file-declarations.json.gz)记录本批原生包、协议及账号 Claude 文件，源码摘要用于增量核对。
- [普通符号引用](anthropic-symbols-normal.json.gz)、[unit 符号引用](anthropic-symbols-unit.json.gz)、[integration 符号引用](anthropic-symbols-integration.json.gz)由 Go 类型信息确认定义与引用位置，包含局部符号、包内调用、旧包装及外部消费者；不是把同名文本命中视为方法调用。
- [各标签和 OS 文件选择](anthropic-bedrock-buildsets.json)、[精确门禁许可](native-storage-gate-constraints.json)、[24 项夹具](anthropic-bedrock-depguard-fixtures.json)对应编译选择和方向验证。构建清单与类型加载均不是行为测试。
- [默认消费者 race 失败](anthropic-consumer-race.result.json)与[隔离测试级全局设置的 race](anthropic-consumer-isolated-race.result.json)分别保留。隔离不改变各测试内部并发。
- [Claude 完整凭据链真实存储 race](claude-native-credential-storage-race.result.json)验证新账号读取→原生交换→既有 CAS→Redis 回填，管理员交错更新不被旧刷新覆盖；[既有外层事务/失败回滚](anthropic-credential-storage-race.result.json)独立回归。


## S09.3 已通过本批门禁

| 原实现 | 当前唯一所有者 | 生产接入及保留边界 |
| --- | --- | --- |
| pkg/gemini、pkg/geminicli | upstream/gemini、codeassist；原目录删除 | 消费者 import 同批更新；普通/管理模型目录保持原独立形状 |
| geminicli 授权会话、GeminiOAuthService | account/gemini_authorization_sessions.go、gemini_authorization.go | 同一 Store/客户端/发现端口；新 StopContext 纳入原 app Hook；旧 wrapper 保留直接消费者需要的 proxy 投影 |
| GeminiTokenProvider / TokenRefresher | account/gemini_token.go、gemini_refresh.go | 原 project/账号键、cache、RefreshAPI、CAS 保持；Vertex 技术交换待 S09.4 |
| repository Gemini OAuth/Code Assist/Drive | upstream/gemini/codeassist | 60/30 秒 req 池及 Drive 原共享 HTTP 池复用；配置由构造投影；激活诊断原测试随实现移动 |
| Gemini 管理 OAuth HTTP | account/httpapi/gemini_oauth.go | 旧管理构造和 DTO 别名；URL、state/redirect/error 文案不变 |
| Gemini 响应与 stream、SSE 非流收集 | upstream/gemini/response.go、response_openai.go | 旧 service 仅投影 HTTP/配置/图片观测；流转换仍在 protocol/bridge；[字段引用映射](gemini-stream-field-map.json) |
| 三种 Gemini 请求构造 | upstream/gemini/request.go、service/gemini_request_compat.go | RequestPlan 显式认证、token/project、目标与 Vertex 回调，原 global failover 留 S11；账号内循环已由本批 exchange 文件接管 |
| 路径片段与原消息 ID | upstream/path_segment.go、session_identity.go | 原字节护栏、长度及时间/随机格式唯一实现，旧入口委托 |
| 原生 thought signature 清理 | protocol/gemini/signature_cleaner.go | 调用方显式传占位值；原递归测试随纯实现移动 |
| Gemini Batch 网络/JSONL 与共享报文 | upstream/gemini/batch_client.go、batch_jsonl.go；protocol/gemini/batch*.go | task 输入只投影实际 wire 字段；同一共享客户端；Task 状态、完成/清理编排留 S13 |

| 三条账号内 Gemini 重试循环 | upstream/gemini/exchange_{messages,native,openai}.go | 顺序/次数/16 秒上限与旧不可取消退避保持；账号策略与 Ops 只作注入端口；无第二套全局 failover |
| 三条完整平台调用与资源关闭 | upstream/gemini/execute.go | 生产 Forward/ForwardNative/OpenAI compat 已改绑；app NativeUpstreamAttempts 同步等待；错误 HTTP 适配/完成仍留 S11 |
| CreativeExecutor Gemini 网络与图片解析 | upstream/gemini/images.go、protocol/gemini/images.go、upstream/image_bytes.go | 保留生成/edit 的任务资格、Header 最终覆写、64 MiB 读取、最后图片选择；任务与资金留 S13 |
| Gemini AI Studio 模型 GET | upstream/gemini/model_get.go | 原路径片段护栏、8 MiB 读取与 WWW-Authenticate 回传；选择/目录回退留原调用者，不取得消费槽 |
| Gemini 错误展示与配额信号 | gateway/httpapi/gemini_error_mapping.go；upstream/gemini/quota_observation.go | 安全消息/状态映射归 HTTP，日窗口仍调用账号日期规则；native 不写账号健康 |
| 公共 Google message/空 parts 与兼容帧事实 | protocol/google/message.go；protocol/gemini/empty_parts.go；protocol/bridge/compat_output_observation.go | 原 Antigravity/Qoder 入口只委托，未改变各平台 TTFT、取消或重试判定 |


## S09.4 当前迁移（实施中）

| 原符号/文件 | 唯一实现 | 调用与退出边界 |
| --- | --- | --- |
| vertex_service_account.go 的字段选择、位置与缓存锁 | account/vertex_credentials.go、vertex_token.go | Claude/Gemini token 与 Batch 原入口只投影；原 Redis 实例、key、TTL 不变；B03 等锁取消固定修复 |
| Service Account JWT/代理/token 解析 | upstream/internal/googleauth/service_account.go；protocol/google/service_account.go | vertex 通过共享技术原语交换，旧测试入口委托；签名不接收旧实体或缓存 |
| Vertex URL、模型日期、Claude body | upstream/vertex/service_account.go | 服务、测试、Creative/Batch 的旧签名委托，原生 Gemini 以 URL/token 端口接入 |
| Gateway Vertex Beta/request | upstream/vertex/request.go | 旧 HTTP 适配只投影 Header、账号和策略；纯 Beta token 解析归 protocol/anthropic，Bedrock 同步委托 |
| Vertex Batch/GCS 客户端、JSONL 组合读取器 | upstream/vertex/batch_client.go | 旧构造和接口别名；GCS response Body 交接后由任务流关闭，无全量缓冲 |
| BuildVertexBatchJSONL | upstream/vertex/batch_jsonl.go | 只接收独立 wire 输入；任务 MIME 规范化与状态机留 S13 |

B03 原失败证据见 planning/vertex-repro.jsonl；修复仅改变等锁取消，不借迁移修改缓存身份、竞争退避、结果不明、失败分类或任务清理策略。


### 机器清单补充

- Gemini：[63 个文件的声明/测试/构建约束](gemini-file-declarations.json.gz)，以及三个标签的 gemini-symbols 引用账本；不包含 S09.4 后新增的 Service Account wire。
- Vertex：[文件声明](vertex-file-declarations.json.gz)、[普通](vertex-symbols-normal.json.gz)/[unit](vertex-symbols-unit.json.gz)/[integration](vertex-symbols-integration.json.gz)类型引用、[构建选择](vertex-buildsets.json)。引用数量含局部符号和测试，不当作迁移 API 或行为测试数量。
- [删除的历史精确许可](removed-migrated-exceptions.json)仅针对已经迁出的旧文件；原目录角色规则保持，不产生新增目录级豁免。


## S09.5 当前迁移（实施中）

| 原实现 | 当前唯一所有者 | 生产接入与保留边界 |
| --- | --- | --- |
| pkg/antigravity 客户端与 transformer | upstream/antigravity；旧目录删除 | 所有消费者同批改 import，纯转换仍 protocol/bridge；原生 UA/URL 可变状态仅一份 |
| OAuth session store | account/antigravity_authorization_sessions.go | 原 30 分钟 TTL、五分钟清理与 Start/Stop，原白盒测试随实现移动 |
| OAuth/项目发现/隐私/套餐投影 | account/antigravity_authorization.go、privacy、subscription | 通过独立 wire 客户端端口调用原生供应商；公开授权操作由 app 等待，旧有限重试不借本阶段修改 |
| token backfill 冷却与资格/刷新 | account/antigravity_token.go、antigravity_refresh.go | 原 ag:project/account key，S06 RefreshAPI/CAS、八秒请求刷新预算及后台十五分钟资格；旧 Provider 只投影，冷却不复制 |
| 管理 OAuth handler | account/httpapi/antigravity_oauth.go | 原路由/输入/错误形状保持，旧管理构造别名 |
| Gemini/Claude/Chat/Responses 及静态上游流 | upstream/antigravity/response_*.go | Gin/config/HTTP 错误策略留旧适配；sink 同步输出，原失败返回与断开后 usage drain 保持 |
| 原共享 Gemini 用量/收集选择、Anthropic usage 合并 | protocol/bridge/gemini_usage_projection.go、protocol/anthropic/usage_merge.go | 供不同具体平台复用，禁止平台互相 import；原 native 包和 service 入口仅委托 |

[流结果字段映射](antigravity-stream-field-map.json)使用 Go 类型信息，不替换其他平台同名字段。当前 [response-unit2](antigravity-response-unit2.result.json) 定向 unit 测试共 749 条通过事件，无失败/跳过；账号内重试、完整 Execute 改绑、真实存储及本平台门禁仍待完成，不宣布 S09.5 通过。


| S09.5 后续单元 | 当前唯一所有者 | 保留边界 |
| --- | --- | --- |
| 账号内普通/智能/单账号/credits 重试及容量去重 | upstream/antigravity/retry_*.go | RetryInput 不接收旧账号、Gin 或 config；账号健康、粘性与 Ops 通过同步动作投影，唯一去重 map 随实现移动 |
| Claude/Gemini 两条恢复链 | upstream/antigravity/recovery_claude.go、recovery_gemini.go | 保留签名两阶段、budget、模型兜底顺序、阈值与次数；开关仍在原时点读取 |
| Chat/Responses/Claude/Gemini/静态 upstream 单次执行 | upstream/antigravity/execute.go | 五分支生产改绑；实际响应由 Execute 关闭，旧 adapter 只映射 HTTP/账号错误及完成值；无新全局重试 |
| v1internal 请求、身份补丁、静态请求与探测 | upstream/antigravity/payload.go、static_request.go、probe.go | 复用 protocol 纯转换；探测不获取请求租约；原完整上下文/输出缓冲边界保持 |
| 账号订阅、额度、403 展示与状态写入 | account/antigravity_quota.go、health.go；upstream/antigravity/quota_errors.go | 供应商字段解析与账号展示分离；credits/model window/INTERNAL 500 的写入、缓存顺序保持；计数器实例不复制 |
| 仅供 unit 的旧私有入口 | service/antigravity_compat_unit_test.go | [29 项](antigravity-unit-only-compatibility.json)只保留原断言所需转接，普通生产构建不带入；[无消费者项](antigravity-removed-compatibility.json)删除 |

[11 项原生 Execute race](antigravity-native-execute-race.result.json)覆盖本地真实 HTTP 的协议输出/用量/关闭；[授权启停与消费](antigravity-auth-lifecycle-race.result.json)覆盖失败不消费、成功后隐私顺序及 Stop 超时报未完成。[真实 PostgreSQL/Redis](antigravity-native-storage-race.result.json)通过本地 TLS OAuth 与发现验证原 CAS，管理员新凭据未被覆盖。仍需平台全包、方向夹具、最终 lint/清单及 docs 后才可宣布本批 gate。


S09.5 已通过 [本批 gate](antigravity-gate.json)。当前仍保留旧入站模型/HTTP 错误/完成编排至 S11，平台交换、恢复、输出、配额解析与账号侧规则各自唯一；不宣布 S09 全量完成。

### S09.6 CN / Ollama / usageprovider（门禁通过）

- `service/upstream_usage_service.go` 的七种固定查询与 JSON 解析 → `upstream/{kimi,zhipu,deepseek,usageprovider}`；技术 HTTP → `upstream/internal/usageclient`；唯一注册表 → `account/provider.UpstreamUsageExecution`。app 直接绑定 executor，删除 `app/legacybridge/account_upstream_usage.go`；账号查询/监控实例、singleflight、身份复核与 CAS 保留 S06 唯一实现。
- 归一化值、错误、CN 窗口及 Ollama 观测 → `upstream/usageview`，account 兼容别名；技术请求 → `usagecontract`。字段 JSON、错误链、512KiB 上限、状态映射、版本路径、New API 状态→Token→钱包原查询顺序保持。
- Ollama HTML/网络/地址/Retry-After → `upstream/ollama`，响应体由原生抓取关闭；删除旧解析入口与 `legacybridge/account_ollama.go`。原四个 HTML 契约测试及唯一脱敏 fixture 随实现迁移，旧 account 链测试引用新位置。Chat reasoning/thinking 补齐及 max_tokens 限制迁入该包，启用资格与原日志点留旧网关。
- 共享 OpenAI 端点算法 → `infra/httpclient/endpoint.go`；日期字符串解析 → `pkg/timezone/parse.go`；SSE data 行及显式无状态 Responses 修复 → `protocol/openai`。旧函数只委托，未改变哪个入口应用修复。Kimi 精确并发文案与平台默认测试模型进入对应平台；健康决策仍归 account。
- 账号协议、模式、端点配置与测试资格属于 S06 account；通用 Anthropic 报文及 usage bucket 继续使用 S03/S09.2 唯一协议实现；CN 经通用 OpenAI 原生交换的进一步改绑随 S09.8 收尾。请求体改写时点、全局重试、账单及完成工作留 S11，不把百分比当作金额，也不把手工查询改为健康维护。
- 普通构建无消费者的兼容声明移除；仅被 unit 测试使用的五个 helper 保留在 `upstream_usage_compat_unit_test.go`，见 `usage-unit-compat.json`、`usage-removed-compat.json`。无新增缓存/锁/周期任务。
- 文档锚点：`docs/interfaces/upstream_usage.md#native_usage_adapters`；账号维护文档同步实际结构。最终门禁与类型消费者清单待本批验证完成。

S09.6 最终证据：`usage-gate.json`、`usage-file-declarations.json.gz`、`usage-symbols-{normal,unit,integration}.json.gz`、`usage-buildsets.json`；保留职责如上，最终全阶段校验在 S09.8 后串行执行。

### S09.7 Grok（实施中）

已接入：`pkg/xai` → `upstream/grok`；`repository/grok_oauth_client.go` → 原生 `oauth_client.go`；账号授权/会话/凭据读取与刷新 → `account/grok_{authorization,sessions,token,refresh,credential_snapshot}.go`；存储 → `account/rediscache/grok_sessions.go`；令牌 wire → `protocol/grok`；纯展示 → `upstream/usageview/grok_*`；CLI Header 与同请求 403 回退 → `upstream/grok/transport_fallback.go`。

依赖账本：`grok-package-move.json`、`grok-removed-old-exceptions.json`、`grok-unit-compat.json`。删除迁出 xai 的全部八项角色/历史规则，原生层不再许可 Redis；新 URL 叶子只能使用精确标准库/IP 判断，不反向依赖 egress 根用例。旧消费者只改绑准确 import。

后续必做：媒体/Voice/搜索/原生响应与探测执行、账号授权 HTTP/批量导入、app 唯一实例直接绑定、活动与真实 Redis/PostgreSQL race、逐符号消费者和协议输出证据、依赖夹具、文档及最终 S09.7 门禁。旧网关继续拥有视频任务绑定和资金结算，不能直接搬入 upstream。

S09.7 增量：`upstream/grok/{errors,search_count,sse_filter,request_headers,audio_usage,realtime_relay,voice_request,voice_execute,media_codec}.go`；共享 `protocol/{audio_usage,openai/sse_data,openai/sse_data_line}`、`upstream/{frame,image_upload,image_request}`。媒体 codec 不 import billing，其归一化函数由旧适配在原调用点投影，未提前查询价卡。Voice 原入口已调用新的 Executor；Grok 媒体本次仍在实施中。

### S09.7 2026-09-15 增量

| 来源 | 唯一实现 | 生产接入/保留职责 |
| --- | --- | --- |
| grok_quota_service 查询与 probe runtime | account/grok_quota.go；native billing_fetch/quota_probe | 旧 service 投影依赖、共享原 ProbeRuntime；平台健康回调继续逐项迁移 |
| admin/grok_oauth_handler | account/grok_import.go + account/httpapi/grok_oauth.go | app/account_import_probes 绑定原队列；旧 Admin 构造只委托 |
| Grok Voice/Realtime/media/content | upstream/grok 对应 Executor、RealtimeSession、VideoContent | 旧入站 HTTP/结算归 S11/S13，NativeUpstreamAttempts 拥有在途等待 |
| Grok body/model_input/compact/cache | upstream/grok BodyCodec / CacheIdentityInput | 外层投影账号资格和 HTTP seed，时间随机 ID 仍原时点，不复制状态 |
| openai_json_decode、tool_continuation、SSE JSON scanner | protocol/wirejson、protocol/openai | 旧入口委托；宽容入口/读取时点不变 |
| 通用 client-tool stream IO | upstream/client_tools_stream.go | 无平台互引；逐帧阻塞写，原有 scanner 上限和关闭 |
| Grok request/opaque replay retry | upstream/grok/responses_request.go、responses_exchange.go | URL/config/覆盖头在原时点投影；一次恢复不成为新 failover 循环 |

### S09.7 结果与存储增量

- Grok 原生 Responses、Chat→Responses、Composer 辅助图片均接入 `ResponsesExecutor`；桥接/辅助用 `SingleExchange` 保持没有解码重试，`PassRawStream` 保持原读取路径。HTTP 错误 envelope、共享客户端协议响应和全局 failover 继续由旧适配调用，退出 S09.8/S11。
- 新 `ResponsesObservation` 将已观测 usage、HTTP 提交、重试窗口、语义输出和 TTFT 分开；旧失败入口的 nil/结果返回由兼容层保持，未增加 Grok 失败扣款。
- `account/grok_team_rate_limit.go`、`grok_model_quota_block.go` 持有原进程内健康覆盖表；无新后台任务、无第二份锁和表。候选循环仍调用旧投影后进入同一实现。
- `account/grok_quota_view.go` 统一配额展示、历史快照和账期字段组合，JWT/Heavy 解释由 upstream 端口提供。`grok_spending_reauth.go` 的软性标记与账期规则归 account。
- 真实 Redis `grok-sessions-storage-race`：1 条通过，无跳过；32 个竞争者跨两个 Store 只有一个消费成功，原 key/JSON/TTL 可读取。真实 PostgreSQL `grok-account-storage-race`：8 条通过，无跳过，覆盖 CAS/重新授权竞争/同事务 outbox 失败回滚。原生及会话 unit race：109 条通过事件，不等价于真实供应商验证。

### S09.8 初始改绑

| 旧位置 | 新唯一所有者 | 保留边界 |
| --- | --- | --- |
| pkg/openai | upstream/openai | 纯入站字符串继续复用 gateway/clientmeta，内嵌指令字节不改 |
| pkg/openai OAuthSession/SessionStore | account/openai_sessions.go | 原 30 分钟/5 分钟、Start/Stop、原指针行为 |
| service/openai_ws_v2 | upstream/openai/wsrelay | 唯一双向 relay 与 metrics；入站连接/每轮计费留旧适配 S11 |
| platform/liveattestation | upstream/openai/liveattestation | Darwin/非 Darwin 选择与外部硬件限制保持 |
| service/openai_codex_identity | upstream/openai/codex_identity.go | 全局动态 resolver 唯一，旧入口只投影字符串 |
| repository/openai_oauth_service | upstream/openai/oauth_client.go | 同一技术 HTTP 端口/req 池；具体构造器返回 native client |
| service/http_upstream_profile | upstream/http_profile.go | 相同唯一 context key，旧函数委托 |
| service/openai_privacy_service | upstream/openai/privacy_client.go | 原超时/尽力失败/查询顺序；账户来源组合随后归 account |
| OpenAI token/claims/表单 wire | protocol/openai/oauth_values.go | 原生解析/expiry 校验仍在平台，不增加身份授权用途 |

### S09.8 2026-09-16 账号/传输增量

| 旧位置 | 新唯一实现 | 剩余边界 |
| --- | --- | --- |
| openai_oauth_service 授权/刷新/补全规则 | account/openai_authorization.go、openai_token_values.go | 旧装配只投影 proxy/TLS/native ports；HTTP 继续改绑 |
| openai_codex_pat_service | upstream/openai/pat_client.go + account/openai_pat_credentials.go | 旧 URL 测试覆盖保留，原 helper 转接待消费者清点 |
| openai_token_provider + token_refresher OpenAI 分支 | account/openai_token.go、openai_refresher.go | 缓存、刷新器、指标均复用原实例，配置投影仍由旧装配提供 |
| openai_agent_identity 签名/解密/注册交换 | upstream/openai/agent_identity.go | task 锁/条件写入、WS 失效和请求恢复继续本子步骤改绑 |
| openai_ws_client | upstream/openai/ws_client.go | pool、入站读与完整尝试编排仍待拆分 |
| HTTP context profile / Header copy | upstream/http_profile.go、header.go | 旧调用共用同一 key / 复制实现 |
| 独立 reasoning token 算术 | protocol/reasoning_usage.go | 原 Grok 和 WS 调用转接，算法与数值顺序保持 |


## S09.8 增量（本批门禁待验）

| 原实现 | 唯一实现 | 兼容与剩余职责 |
| --- | --- | --- |
| service/openai_ws_pool、openai_ws_client | upstream/openai/ws_pool、ws_client | 旧池别名与技术投影；原入站按需 Start，完整 WS 入站与失败编排 S11 |
| Agent Identity 签名及注册 | upstream/openai/agent_identity | 旧密钥形状投影；不持有账号/存储 |
| Agent Identity 任务锁、登记、母账号解析 | account/openai_agent_task、credential_shadow | 唯一共享协调器；旧兼容层只读写投影，状态与算法不复制 |
| openai_responses_tool_schema | protocol/openai/tool_schema | 字节修复与原纯测试同迁，平台适用选择留旧入站 S11 |
| openai_images 资源及 b64 回填 | upstream/openai/image_resources、image_backfill | 原开关/传输/日志投影；图片计费与任务状态机不迁动 |
| gateway_upstream_response 错误 JSON | upstream/error_message | 通用 wire 提取唯一实现，各平台原错误分类仍各自拥有 |

| Live 创建/sideband/证明加密 | upstream/openai/live_client、live_attestation_cipher；protocol/openai/live | 旧入站持有租约、会话接管与结算，S11；原生只接收技术参数 |
| Responses 标准 SSE/非流/SSE→JSON | upstream/openai/response_stream、response_nonstream | 同步 OutputSink；旧入站只投影 HTTP/观察/账号副作用，直通与其余协议继续迁移 |
| 首输出暂存/响应头预算 | upstream/openai/first_output_stage、http_exchange | 原内存/文件/队列预算与取消顺序；暂存白盒测试随实现移动，实际默认预算关系留旧入口验证 |
| Responses wire/usage/终态输出重建 | protocol/openai/response_forward_wire、image_output；protocol/bridge/response_forward_wire | 旧函数和状态类型只委托/别名；无第二份累积器 |
| Embeddings 单次请求 | upstream/openai/embeddings；protocol/openai/embeddings_usage | 真实 ForwardEmbeddings 调用 Execute，原错误分类/资金投影留 S11 |
| Codex 转换、预留工具别名、ID 配对 | upstream/openai/codex_transform、codex_tool_names；protocol/openai/compatibility_fields | 旧模型选择由显式纯规则传入；HTTP/WS 每请求恢复状态继续由原拥有者管理 |

| OpenAI quota 查询/重置/extra | account/openai_quota、openai_quota_snapshot；upstream/openai/quota_client；protocol/openai/quota、quota_credits、codex_limits | 账号恢复与缓存仍使用唯一能力；20s/512KiB 和两次读取不合并，旧服务仅投影 |
| 管理员 OAuth/PAT HTTP 与导入 | account/httpapi/openai_oauth；account/openai_account_import、openai_quota_actions | app/account_openai_oauth_http 直接装配；恢复八秒预算保留，操作/底层服务分别参与生命周期 |
| 图片 JSON/multipart 与上传 | upstream/image_request_parse | 原 RequiredCapability 和模型验证留旧路由，公开解析形状通过精确投影保持 |
| OAuth 图片 Responses codec / 尺寸 | upstream/openai/images_codec、image_dimensions | 时间函数在原缺省点调用；旧错误别名保持 errors.As，原行为测试通过旧入口 |
| OAuth/API Key 图片响应读取 | upstream/openai/images_response、images_apikey_response；protocol/openai/image_usage | 两条流读取、原 usage 合并与有界通道独立保留；错误观察/重试仍由旧入站端口提供 |

| 图片实际发送/响应体/活动释放 | upstream/openai/images_execute | API Key 与 OAuth 的旧 ForwardImages 调用 Execute；原健康/重试/资金结果投影保留 S11，任务 S13 |
| Responses passthrough 三种读取 | upstream/openai/response_passthrough | 旧 HTTP 心跳、Ops 时间与账号副作用端口保持原时点；SSE 前导与真实输出边界不合并 |
| OpenAI 错误与首输出规则 | upstream/openai/response_error_rules | 仅供应商信号/报文分类；账号禁用、跨账号循环和审核用例仍各自持有 |
| Chat/Messages 缓冲终态/空响应检测 | upstream/openai/response_buffered、silent_refusal | 原 Context 错误链保持；配置由旧入口投影，原 64 KiB/单行预算与16事件通道保留 |
| Responses→Chat/Messages 响应 | upstream/openai/response_chat、response_messages | protocol bridge 唯一转换；两个客户端的取消、错误和终态 usage 顺序分别保留；入站/完成 S11 |
| 延迟下游输出 | upstream/deferred_output、gateway/httpapi/output | Header 访问不提前；OutputState 不触碰 Header，HTTP 实际写入仍归同步 sink |

| Raw Chat 工具身份、usage/档位/终态 wire | protocol/openai/chat_stream_compat | 原三个终止信号、未知字段、空 id/name 删除边界保持；旧入口仅委托 |
| 流读取错误与稳定错误码 | upstream/openai/stream_read_error | 旧类型/哨兵指向同一实现；HTTP 本地超限错误通过明确参数传入 |
| CC SSE/JSON 共用读取 | upstream/openai/response_cc_read | 每轮档位观察通过旧端口投影；无新增共享缓存；原读取入口消费者清零删除 |
| Raw Chat / CC→Responses / CC→Messages 输出 | upstream/openai/response_raw_chat | 纯 bridge 唯一算法；随机/时钟外传，原 reasoning 缓存不复制，入站和完成职责 S11 |

| Compact/reasoning/store=false/schema/空图片请求兼容 | upstream/openai/request_compatibility；protocol/openai/recorded_effort | 只处理明确输入，不自行判断账号/读取设置；旧触发位置保留 S11 |
| Responses 出站 Header | upstream/openai/request_headers | 旧上下文只投影身份/策略；每次原账号 Header 与 session/指纹顺序保持，默认身份由同一原生 resolver 提供 |
| CC 请求构造与发送 | upstream/openai/request_chat | 原请求 context/代理/TLS/专属 Header 端口按原时点执行；响应由原单次拥有者读取关闭，未增加重试层 |

| OAuth token/refresh-lock Redis | account/rediscache/oauth_token；app/oauth_token_cache | 原键、TTL、锁语义不变；旧 GeminiTokenCache 为 alias/构造委托，共用唯一 Redis |
| Responses passthrough 请求头 | upstream/openai/request_passthrough | 标准与透传各自保持原顺序，原请求侧路由/身份只作参数投影 |
| Codex 账号 namespace/UUID/metadata | upstream/openai/account_identity | 不接收旧 Account/完整凭据；旧入口负责受控必要字段读取与影子来源，标识/错误文本与原 HEAD 核对 |

| Responses 被拒字段重试 | upstream/openai/rejected_field_retry | 原 Gin 请求仅持有预算句柄；全请求六次预算、每尝试独立去重，后续入站循环 S11 |
| Alpha Search 纯转换及单次执行 | upstream/openai/alpha_search_codec、alpha_search | 原 2xx 按次计费、PAT 回退及账号状态副作用顺序；旧入口准备策略投影，具体执行通过 Execute |

| Codex 指纹每尝试 ID 与 Header/body 改写 | upstream/openai/fingerprint | 原 context 仅暂存唯一状态指针与账号归属检查；时间/随机源外传；配置仍归 account，模式以显式字符串投影 |

| Alpha Search Header / PAT 元数据 | upstream/openai/alpha_search_headers；account/openai_alpha_metadata | HTTP 策略投影 S11；原先本地更新后持久化与失败行为保持 |
| 图片请求 Header | upstream/openai/images_request | 复用原 Responses 身份和覆盖端口，策略不进入具体平台 |
| input_tokens 查询发送/输出 | upstream/openai/count_input_tokens | 兼容计数与原生 JSON 分开；本地估算、账号资格和失败副作用仍由入站选择 S11 |

| WS 续接/重放/严格比较与失效密文处理 | upstream/openai/ws_payload、encrypted_content；protocol/openai/previous_response_id | 原始字节共享和头数组所有权不变；旧会话缓存、账号选择和重试时机 S11 |
| WS 出站握手头 | upstream/openai/ws_headers | 原输入 session、账号凭据与策略按原时点投影；旧连接拥有者和完整 relay 入站 S11 |
