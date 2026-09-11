# S03 迁移与交接清单

本清单描述已接入的实现与保留的消费者边界。逐文件 import、声明、构建选择和直接包消费者见 [migration-inventory.json.gz](migration-inventory.json.gz)；每个旧符号记录现存委托引用及实际新声明，不用文件名前缀替代混合文件分析。AST 元数据（声明正文以摘要替代，避免重复存储仓库源码）见 [current-ast.json.gz](current-ast.json.gz)，兼容包消费者见 [remaining-consumers.json](remaining-consumers.json)。方法通过类型别名保持同一方法集；动态调用链结合以下职责和 Wire 绑定核对。

## 实现与混合职责

| 原入口/文件 | 唯一实现及已迁职责 | 留在原入口的职责与退出阶段 |
| --- | --- | --- |
| `pkg/apicompat/types.go`、stream event wire | `protocol/anthropic`、`protocol/openai`：报文、自定义编解码、RawMessage、tool output 原文 | `compat.go` 类型别名；直接 wire 消费者已改用所有者，旧转换调用入口留 S11/S16 |
| apicompat 的 Anthropic/Responses/Chat 转换文件 | `protocol/bridge`：六方向转换、工具 ID/namespace/custom tools、每流状态、usage 和终态 | 旧入口注入 Runtime、选择 RequestOptions；型号规则在 capability，S09/S11 完成平台接入后删除包装 |
| `anthropic_to_responses.go` 中型号判断 | `routing/capability/responses_bridge_model.go` 保留桥接专属型号拼写、版本和采样策略 | apicompat 只将两个判断投影为 `DropSampling`、`SupportsMaxEffort`；不将管理员识别差异统一成新规则 |
| `pkg/antigravity/{claude_types,gemini_types}.go` | `protocol/anthropic`、`protocol/gemini`：明确 wire 变体 | v1internal 包装、模型目录/默认安全策略仍属平台，S09 |
| Antigravity request transformer/schema cleaner | `bridge/gemini_contents.go`、`gemini_internal_options.go`、`gemini_internal_schema.go`：schema、tool、thinking、signature 纯转换 | project、身份、session ID、模型默认值/上限选择与诊断输出仍在旧平台，S09 |
| Antigravity response/stream transformer | `GeminiToAnthropicResponseProcessor`、`GeminiToAnthropicStreamProcessor` 拥有全部转换状态 | 平台解包、旧 ID/随机失败回退及 usage hook 接入留原入口，S09；无第二份流状态 |
| `service/gemini_messages_compat_service.go` | `bridge/gemini_native.go`、`gemini_messages_stream.go`：原生 schema、内容、usage 解析与 Messages SSE 状态 | HTTP 读取/解压、write/flush、首次 token 计时、模型恢复、图片观察、重试/取消仍在 service，S09/S11 |
| `service/gemini_chat_completions_compat_service.go` | `gemini_compat_stream.go`、`gemini_response_chain.go`：原生转换与兼容 SSE 状态 | 下游 bridge 的执行组合、写失败与结束、工具名恢复留 service，S09/S11。两种 Gemini 流保留不同的 thinking/index/usage 时序 |
| `pkg/httputil/body.go` | `protocol/openai/lenient_json.go`：纯字节修复与 BodyLimitError | HTTP 读取及解压继续由 httpx 实现，旧入口将 Limit 转成 MaxBytesError，S11/S16；删除两个已无消费者的旧预分配常量 |
| `pkg/googleapi/{error,status}.go` | `protocol/google`：错误结构和解析；`gateway/httpapi/google_status.go`：HTTP 状态映射 | 激活诊断与平台建议留 googleapi，S09 |
| `pkg/claude/{constants,cli_version}.go`、`pkg/openai/request.go` | Anthropic wire 常量、`gateway/clientmeta` 的版本/UA/originator 解析 | 默认 Header、env、平台默认值、许可裁决留旧入口，S09/S11 |
| domain 协议/平台常量、目录、分组默认规则 | `protocol.ProtocolID`、`routing/capability`：24 项能力、原生/默认集合、校验、单步 fallback | domain 按需别名/委托；Account/Group 解析字段、错误 reason、投影留 S06；剩余别名 S15/S16 |
| service account/group protocol 与 routing | `capability.AccountProtocols` 消费独立值投影并逐次判断 | 不接收 Account/Group/凭据；存取字段、缺省/显式空与平台执行留 S06/S09 |
| 目录展示、扩展路由门禁 | `gateway/httpapi.ProtocolEndpoints` 唯一路由声明；`routing/httpapi` 唯一目录 handler | app 投影展示地址；server 原 URL、中间件顺序和鉴权保持；旧 service 目录实现已删除 |
| service reasoning effort policy、domain 类型 | `protocol` 拥有 wire 值，`routing` 拥有映射/排序/校验/上限 | 旧网关读取/修改请求、桥接默认值和审计仍留 S11 |
| `service/channel.go`、time pricing | `billing/pricing` 拥有价卡/区间/时间规则的类型、Clone、校验与计算 | service 的渠道实体、仓储与缓存留 S06；时区加载缓存唯一在 provider |
| ModelPricingResolver | `pricing.ResolvePriceCards`、interval 等纯规则 | 原始分组匹配、按需渠道/目录读取、错误降级留旧入口，S04/S06；按次媒体不增加 token 查询 |
| BillingService | `pricing.CalculateCost`、CostInput、ModelPricing、CostBreakdown、媒体/搜索/音频/长上下文和展示规则 | config/模型策略/时刻投影及旧签名；余额/额度/缓存接口、schema 常量留 S04，账号成本查询留 S04/S06 |
| account stats/image size/video resolution/media pricing 混合文件 | `pricing/account_stats.go`、`image_billing_size.go`、`video_billing_resolution.go` 等纯匹配/计算 | 旧渠道读取、结果字段写回和资金路径保持，S04/S06 |
| PricingService 目录规则 | `pricing/catalog_{types,rules,parse,query}.go`：解析/浅合并/候选/专属回退；返回诊断 | 文件/网络、日志、锁/热更新留 provider；元数据查询不继承跨型号价格回退 |
| PricingService 运行状态 | `billing/provider/pricing.go`：唯一缓存、hash、fallback/override、Initialize/Start/Stop/Wait | 旧 service 只持 runtime 指针及旧构造参数，无锁/目录/定时器；旧私有测试用快照初始化，S16 清理 |
| repository 定价远端客户端 | `billing/provider/remote.go`，复用 infra/httpclient | 旧 NewPricingRemoteClient 委托；代理失败不直连策略/原错误文本保持，S16 |
| pricing 装配 | `app/pricing.go` 将一次加载的 config 投影 Options，`app/legacybridge/pricing.go` 提供旧平台能力 | 原 PricingInitialization、PricingService hook 顺序；Wire 只把 provider 改为 app 构造；没有 Ent 生成 |

静态 fallback 数据和算法只有一份。旧公开入口保留 alias/delegate，消费者清零的私有包装删除；仅被旧 unit 测试使用的 23 项转接移至 `service/pricing_legacy_unit_test.go`，实际移除列表见 [retired-private-entries.json](retired-private-entries.json)。型号策略相关的 97 个测试/辅助声明回到旧兼容入口，使用初始断言原文；纯 bridge 另测相同不透明模型名下显式选项的独立作用，不在测试中复制型号策略。

## 构建、依赖、测试与文档

- [构建选择](build-selections.json)及 `selection-*.json` 覆盖普通、unit、integration、e2e、wireinject、embed、Darwin/Linux。文件名带 integration 不自动等于 integration 标签；实际事件见 [验证摘要](verification.md)。
- [依赖规则变更](dependency-rules.json)、[精确例外与实际 import](dependency-exceptions.json)及[可丢弃夹具](dependency-fixtures.json)记录路径与命中；原 service/handler 规则不变，六项旧 unit depguard 继续独立计入基线。
- [Wire 差异](wire-diff.patch.gz)只涉及定价装配，原 `go generate ./cmd/server` 入口保持；[重复生成](wire-reproducible.json)无差异。
- 工具/平台策略的保留以表中退出阶段为准。HTTP 执行链、真实输出判定与取消不迁入 bridge；没有任何账号调度、结算事务或其他平台模块完成的声明。
- 稳定文档入口：`system_architecture.md#dependency_layers`、`gateway_request_lifecycle.md#protocol_conversion_boundary`、`protocol_capabilities.md#protocol_catalog`、`routing_and_billing.md#group_model_pricing`、`model_catalog_and_marketplace.md#model_catalog_metadata_lookup`、`development_workflow.md#backend_dependency_rules`；各上游协议分派章节同步当前职责。

## 分组夹具修正

`group_repo_integration_test.go`、`group_repo_duplicate_integration_test.go`、`group_repo_sort_integration_test.go`、`group_media_pricing_migration_integration_test.go` 共 45 处 Group 字面量只补原本省略的默认协议/fallback/图片策略字段。显式空、非法协议及原断言不变，SQL 约束不变。全量 integration 和针对 GroupRepoSuite/复制 outbox 回滚的真实 PostgreSQL 测试实际通过；本轮未暴露需要原 HEAD 复现的分组生产缺陷。
