# S11 入口与执行契约矩阵（验收映射）

以 `server/routes/gateway.go` 和 `routing/capability/protocol.go` 的真实分派与能力目录为来源。平台目录支持不表示分组默认启用；鉴权、显式协议集合和候选资格仍先行约束。Anthropic 下的 Bedrock/Vertex 以及 Gemini 下的 Service Account 是账号/执行变体，不新增数据库平台。

本表区分**路由清点**与**实际场景证据**。路由行保留迁移分派信息，不表示“入口 × 平台 × 原生/转换 × 传输”的笛卡尔积已逐格通过。E01—E24 只覆盖具名测试的实际场景；共享实现、父测试通过或全量总数不能推导未列组合的覆盖。每次 attempt、fresh/DB 复核重新确定原生/转换；一次请求只有所属核心的一套账号循环。

最终普通、unit、integration 全量分别为 **11,749 / 19,773 / 12,698** 条通过事件，均无失败；跳过分别 **4 / 8 / 5**，原因见[明细](final-skips.json)。首轮 CN 六个子场景与父项失败已修复，原失败与最终重跑结果保留于[验收汇总](verification.md)。事件包含父子测试，各批集合重叠，不可相加为独立案例数；本矩阵不替代其余门禁、构建和生命周期证据。

## 路由清点（非逐格测试通过表）

| 入口 | 分组平台 | 传输 | 新 HTTP 入口 | 原生/转换边界 | 相关场景证据（非本格全覆盖） |
| --- | --- | --- | --- | --- | --- |
| Messages | anthropic | HTTP 非流 | MessagesHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E04](#e04)；原生局部场景 |
| Messages | anthropic | SSE | MessagesHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E04](#e04)；原生局部场景 |
| Messages | openai | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E06](#e06)、[E07](#e07)；共享 HTTP/恢复场景 |
| Messages | openai | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E06](#e06)、[E07](#e07)；共享 HTTP/恢复场景 |
| Messages | gemini | HTTP 非流 | MessagesHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Messages | gemini | SSE | MessagesHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Messages | antigravity | HTTP 非流 | MessagesHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Messages | antigravity | SSE | MessagesHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Messages | grok | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E11](#e11)；Responses/图片桥接局部场景 |
| Messages | grok | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E11](#e11)；Responses/图片桥接局部场景 |
| Messages | qoder | HTTP 非流 | QoderCompatibleHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E09](#e09)、[E10](#e10)；Chat 夹具与兼容边界 |
| Messages | qoder | SSE | QoderCompatibleHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E09](#e09)、[E10](#e10)；Chat 夹具与兼容边界 |
| Messages | kimi | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Messages | kimi | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Messages | zhipu | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Messages | zhipu | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Messages | deepseek | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Messages | deepseek | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Responses | anthropic | HTTP 非流 | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E05](#e05)；转换事件场景 |
| Responses | anthropic | SSE | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E05](#e05)；转换事件场景 |
| Responses | openai | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E06](#e06)、[E07](#e07)；共享 HTTP/恢复场景 |
| Responses | openai | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E06](#e06)、[E07](#e07)；共享 HTTP/恢复场景 |
| Responses | gemini | HTTP 非流 | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Responses | gemini | SSE | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Responses | antigravity | HTTP 非流 | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Responses | antigravity | SSE | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Responses | grok | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E11](#e11)；Responses/图片桥接局部场景 |
| Responses | grok | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E11](#e11)；Responses/图片桥接局部场景 |
| Responses | qoder | HTTP 非流 | QoderCompatibleHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E09](#e09)、[E10](#e10)；Chat 夹具与兼容边界 |
| Responses | qoder | SSE | QoderCompatibleHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E09](#e09)、[E10](#e10)；Chat 夹具与兼容边界 |
| Responses | kimi | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Responses | kimi | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Responses | zhipu | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Responses | zhipu | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Responses | deepseek | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Responses | deepseek | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Chat Completions | anthropic | HTTP 非流 | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E05](#e05)；转换事件场景 |
| Chat Completions | anthropic | SSE | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E05](#e05)；转换事件场景 |
| Chat Completions | openai | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E06](#e06)、[E07](#e07)；共享 HTTP/恢复场景 |
| Chat Completions | openai | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E06](#e06)、[E07](#e07)；共享 HTTP/恢复场景 |
| Chat Completions | gemini | HTTP 非流 | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Chat Completions | gemini | SSE | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Chat Completions | antigravity | HTTP 非流 | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Chat Completions | antigravity | SSE | CompatibleTextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E08](#e08)；共享编排/action 参考；本组合未独立证明 |
| Chat Completions | grok | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E11](#e11)；Responses/图片桥接局部场景 |
| Chat Completions | grok | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E11](#e11)；Responses/图片桥接局部场景 |
| Chat Completions | qoder | HTTP 非流 | QoderChatHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E09](#e09)、[E10](#e10)；Chat 夹具与兼容边界 |
| Chat Completions | qoder | SSE | QoderChatHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E09](#e09)、[E10](#e10)；Chat 夹具与兼容边界 |
| Chat Completions | kimi | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Chat Completions | kimi | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Chat Completions | zhipu | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Chat Completions | zhipu | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Chat Completions | deepseek | HTTP 非流 | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |
| Chat Completions | deepseek | SSE | OpenAITextHandler | 逐候选确定原生或单步转换；不扩大账号原生集合 | [E03](#e03)、[E06](#e06)；仅共享编排；CN 六项定价回归待验 |

## 辅助、会话与未迁任务边界

| 入口 | 平台/传输 | 当前唯一入口与核心 | 特殊契约 |
| --- | --- | --- | --- |
| Gemini model actions | Gemini/Antigravity，原生 HTTP/SSE | GeminiNativeHandler；gateway/text 与对应单次执行 | 原 action/错误形状，signature 跨账号处理，读取与取消时点；[E08](#e08)、[E13](#e13) |
| Messages count_tokens | Anthropic、OpenAI/CN、Grok；HTTP | CountTokensHandler、OpenAITextHandler；text/count_tokens、single_count、tokenestimate | 通用无槽选择；Grok 本地计数不增加资金检查；AG/Qoder 保持原 404；[E12](#e12) |
| Responses input_tokens | OpenAI/Grok/CN；HTTP | OpenAITextHandler；text/input_tokens | 独立同账号/换号预算；只释放选择器实际交付的资源，不加入完成队列；[E12](#e12) |
| 模型 list/get | 适用平台；HTTP | ModelsHandler，routing 展示/查询投影 | 稳定顺序、空成功不恢复默认、查询失败原回退、非消费；[E13](#e13) |
| Embeddings | OpenAI；HTTP | AuxiliaryHandler、media | 原过滤、计量和响应头，资源由单次执行拥有；[E14](#e14) |
| Images generation/edit | OpenAI/Grok；HTTP 或原有 SSE | MediaHandler、media | 实际图片产出与部分失败资格、mandatory 完成、隔离池；[E14](#e14) |
| Grok 视频 generation/edit/extension/status/content | Grok；HTTP/资源流 | MediaHandler、media/video_tasks | 原归属、pending/billed、完成认领与唯一费用效果；[E15](#e15)、[E16](#e16) |
| TTS/STT/custom voices | Grok；HTTP/二进制 | AuxiliaryHandler、media | 管理/下载不套用生成准入，观测音频计量；[E16](#e16) |
| Voice realtime | Grok；WebSocket | AuxiliaryHandler、media | 原连接/租约与音频观测，不改变关闭帧；[E16](#e16) |
| Responses WebSocket | OpenAI/Grok；WebSocket | ResponsesWSHandler、gateway/ws | 每 turn 输入/模型/价格快照，continuation，硬绑定和恢复载荷；[E17](#e17) |
| Live create/sideband | OpenAI；HTTP/WebSocket | LiveHandler、gateway/live | controller/observer、B06、独立租约，仍仅零费用用量记录；[E18](#e18) |
| AlphaSearch | OpenAI；HTTP | AuxiliaryHandler、media；原生 upstream | 原供应商搜索资格，不经过 Brave/Tavily 配额；[E14](#e14) |
| WebSearch/XSearch 与工具模拟 | 原支持入口；HTTP/SSE | SearchHandler、searchtools | search 配额与回滚仍唯一；合成估算 token 不变成真实供应商计费；[E19](#e19)、[E20](#e20) |
| Qoder 首条固定 Execute 链 | Qoder Chat；HTTP/SSE | QoderChatHandler → gateway.Execute → scheduler/upstream/billing/usage | 本地平台转换/端口夹具；成功、部分失败和慢写，不把完成替身计为真实资金提交；真实存储组合另见 E23；[E09](#e09)、[E10](#e10)、[E23](#e23) |
| image batches、creative 与批量任务状态机 | Gemini/Vertex 等任务渠道 | 原明确任务路由与服务，S13 交接 | 只复用已迁平台/资金能力；本阶段不迁任务状态机或扩展 HTTP API |

所有新入口在应用构造时接入共享 `GatewayRequestsAndAttempts`；停止后不进入新的平台尝试，已进入的 Qoder 流保留原尾部收集预算。完成队列、Redis、SQL 按原总预算与依赖顺序停止，超时不算 drain 成功。

## 实际契约证据

下列测试名均存在于当前测试文件，并在所链归档中有对应 `Action=pass`。括号标注源文件的实际 build tag；无 tag 不等于只在普通集合执行。运行命令以配套 result/交接记录为准，不由文件名推定。单项通过不表示所在整批无其他失败/跳过。

| 契约与关联入口 | 当前实际测试/文件 | 保存证据与覆盖边界 |
| --- | --- | --- |
| <a id="e01"></a>E01 所有入口：路由/别名与前置拒绝 | [TestGatewayRoutesClientProtocolGateRejectsAliasesBeforeReadingBody](../../../backend/internal/server/routes/gateway_test.go)（无 build tag）<br>[TestGatewayRoutesResponsesSubpathGuardRunsBeforeProtocolGate](../../../backend/internal/server/routes/gateway_test.go)（无 build tag）<br>[TestGatewayRoutesNonNativeResponsesWebSocketIsRejected](../../../backend/internal/server/routes/gateway_test.go)（无 build tag） | [已保存 pass 事件](assembled-http-routes.events.json.gz)。本地路由拒绝夹具；不证明上游成功。 |
| <a id="e02"></a>E02 复合 Key：JSON/multipart/Gemini URL | [TestResolveCompositeAPIKeyRequestJSON](../../../backend/internal/server/middleware/api_key_composite_test.go)（无 build tag）<br>[TestResolveCompositeAPIKeyRequestMultipart](../../../backend/internal/server/middleware/api_key_composite_test.go)（无 build tag）<br>[TestResolveCompositeAPIKeyRequestGeminiURL](../../../backend/internal/server/middleware/api_key_composite_test.go)（无 build tag） | [已保存 pass 事件](native-auth-consumers.events.json.gz)。认证/改写夹具，保留不同报文的读取与恢复边界。 |
| <a id="e03"></a>E03 共享 Messages/兼容循环 | [TestMessagesRetryRebuildsAndCompletesOnlyFinalAttempt](../../../backend/internal/gateway/text/messages_test.go)（无 build tag）<br>[TestMessagesWrittenFailureCannotSwitchOrComplete](../../../backend/internal/gateway/text/messages_test.go)（无 build tag）<br>[TestFixedMessagesExecutorKeepsGeminiPartialCompletionDifference](../../../backend/internal/gateway/text/executor_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-execute-native-race.json.gz)。端口替身证明重建、输出窗口和入口完成差异，不代表所有平台组合。 |
| <a id="e04"></a>E04 Anthropic 原生 Messages 非流/SSE | [TestGatewayService_AnthropicAPIKeyPassthrough_ForwardDirect_NonStreamingSuccess](../../../backend/internal/service/gateway_anthropic_apikey_passthrough_test.go)（无 build tag）<br>[TestGatewayService_AnthropicAPIKeyPassthrough_ForwardStreamMissingTerminalPreservesPartialUsage](../../../backend/internal/service/gateway_forward_partial_usage_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-forward-target-unit.json.gz)。本地响应夹具；非流结果与缺终态的部分 usage。 |
| <a id="e05"></a>E05 Responses/Chat ← Anthropic 转换 | [TestHandleResponsesBufferedStreamingResponse_ToolArgumentsAreValidJSON](../../../backend/internal/service/gateway_forward_as_responses_test.go)（unit）<br>[TestHandleResponsesBufferedStreamingResponse_PreservesMessageStartCacheUsage](../../../backend/internal/service/gateway_forward_as_responses_test.go)（unit）<br>[TestHandleCCBufferedFromAnthropic_ToolArgumentsAreValidJSON](../../../backend/internal/service/gateway_forward_as_chat_completions_test.go)（unit） | [已保存 pass 事件](parallel-evidence/s11-forward-target-unit.json.gz)。转换事件夹具，工具参数与 cache usage；不等同整条入站路由验收。 |
| <a id="e06"></a>E06 OpenAI HTTP/Chat/Messages 编排 | [TestOpenAITextHTTPPreludeErrorOrder](../../../backend/internal/handler/openai_text_http_adapter_test.go)（无 build tag）<br>[TestOpenAITextHTTPWaitAndSnapshot](../../../backend/internal/handler/openai_text_http_adapter_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-openai-http-entry-tests4.json.gz)。HTTP/等待替身证明错误次序和快照；具体恢复另见 E07。 |
| <a id="e07"></a>E07 OpenAI 同账号 HTTP 恢复 | [TestRunHTTPRecoveryBoundaries](../../../backend/internal/gateway/provider/openaiforward/http_test.go)（无 build tag）<br>[TestRunHTTPTransportFailureDoesNotRecover](../../../backend/internal/gateway/provider/openaiforward/http_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-native-forward-final-race.json.gz)。执行端口夹具限定恢复条件；不证明真实 OpenAI 交换。 |
| <a id="e08"></a>E08 Gemini/Antigravity 原生 action | [TestAntigravityGatewayService_ForwardGemini_UsesConfiguredProjectFallback](../../../backend/internal/service/antigravity_gateway_service_test.go)（无 build tag）<br>[TestAntigravityGatewayService_ForwardGemini_RetriesCorruptedThoughtSignature](../../../backend/internal/service/antigravity_gateway_service_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-forward-target-unit.json.gz)。Antigravity 原生 Gemini 执行夹具；普通 Gemini、Service Account 及所有非流/SSE 组合不能据此一并计为通过。 |
| <a id="e09"></a>E09 Qoder Chat 非流/SSE 与部分失败 | [TestQoderNativeGatewayAttemptsAndCompletion](../../../backend/internal/app/qoder_gateway_test.go)（无 build tag）<br>[TestQoderNativeGatewayLeadingUsageFailureClosesRetryWithoutSemanticOutput](../../../backend/internal/app/qoder_gateway_test.go)（无 build tag）<br>[TestQoderNativeGatewaySlowSinkAndWriteFailure](../../../backend/internal/app/qoder_gateway_test.go)（无 build tag） | [已保存 pass 事件](assembled-http-routes.events.json.gz)。真实平台转换器配合 fixtureClient、选择和完成替身；验证尝试/释放/完成次数，不证明 PostgreSQL 扣款或 Redis 原子性。 |
| <a id="e10"></a>E10 Qoder 兼容入口与取消释放 | [TestQoderCompatibleAttemptBoundaries](../../../backend/internal/gateway/text/qoder_compatible_test.go)（无 build tag）<br>[TestQoderStreamReleaseDoesNotFireOnClientCancel](../../../backend/internal/handler/qoder_gateway_handler_test.go)（无 build tag）<br>[TestQoderNonStreamReleaseStillFiresOnClientCancel](../../../backend/internal/handler/qoder_gateway_handler_test.go)（无 build tag） | [已保存 pass 事件](qoder-count-http-race.events.json.gz)。本地端口/HTTP 夹具；流与非流分别验证，不将 Chat 输出覆盖推给全部 Messages/Responses 场景。 |
| <a id="e11"></a>E11 Grok Responses 非流/SSE 与 Chat 图片桥接 | [TestForwardGrokResponses_PropagatesSearchCountFromJSON](../../../backend/internal/service/openai_gateway_grok_search_billing_test.go)（unit）<br>[TestForwardGrokResponses_PropagatesSearchCountFromSSE](../../../backend/internal/service/openai_gateway_grok_search_billing_test.go)（unit）<br>[TestForwardAsChatCompletionsForGrokComposerBridgesImageInput](../../../backend/internal/service/openai_gateway_grok_test.go)（unit） | [已保存 pass 事件](parallel-evidence/s11-grok-target-unit.json.gz)。本地 xAI 响应夹具；证明计量观测与桥接，非真实搜索/图像生成。 |
| <a id="e12"></a>E12 count_tokens / input_tokens | [TestCountTokensAttemptBoundaries](../../../backend/internal/gateway/text/count_tokens_test.go)（无 build tag）<br>[TestEstimateOpenAIInputTokens_RequestSamples](../../../backend/internal/gateway/tokenestimate/estimate_test.go)（无 build tag） | [已保存 pass 事件](count-native-race.events.json.gz)。无槽尝试边界与本地估算样本；估算不是计费凭据，真实 API 对照另列跳过。 |
| <a id="e13"></a>E13 模型展示/Gemini models | [TestGatewayModels_CustomModelsListCanReturnEmptyWhenSelectionsUnavailable](../../../backend/internal/handler/gateway_models_test.go)（无 build tag）<br>[TestGeminiV1BetaHandler_PlatformRoutingInvariant](../../../backend/internal/handler/gemini_v1beta_handler_test.go)（unit）<br>[TestGatewayModelsCompositeKeyAggregatesMappingsInOrder](../../../backend/internal/handler/gateway_models_test.go)（无 build tag） | [已保存 pass 事件](native-gemini-loop.events.json.gz)。本地查询/路由夹具；空列表、平台分派、复合顺序，非推理验证。 |
| <a id="e14"></a>E14 Embeddings / Images / AlphaSearch | [TestEmbeddingsRetryReleasesBeforeFeedbackAndCompletesOnce](../../../backend/internal/gateway/media/embeddings_test.go)（无 build tag）<br>[TestImagesLoopPartialOutputCompletesAndStopsKeepalive](../../../backend/internal/gateway/media/generation_test.go)（无 build tag）<br>[TestAlphaSearchOutputFailureAndUnbillableResponse](../../../backend/internal/gateway/media/alpha_search_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-media-delivery-native-race.json.gz)。核心端口替身证明释放、已观测图片完成资格及失败输出；非供应商实际图片产出。 |
| <a id="e15"></a>E15 Grok 视频归属/完成认领 | [TestVideoTasksRedisOwnershipAndCompletion](../../../backend/internal/gateway/media/video_integration_test.go)（integration） | [已保存 pass 事件](parallel-evidence/s11-media-delivery-integration-race.json.gz)。隔离真实 Redis；供应商和资金效果替身，不等同真实视频生成或 PostgreSQL 资金验收。 |
| <a id="e16"></a>E16 媒体读取与 Voice/Realtime | [TestMediaContentResponsePreservesRangeAndCommit](../../../backend/internal/gateway/httpapi/media_response_test.go)（无 build tag）<br>[TestRealtimeAdmissionKeepsUpstreamBeforeAcceptAndReleaseOrder](../../../backend/internal/gateway/media/realtime_test.go)（无 build tag）<br>[TestVoiceRetryUsesOriginalFourAttemptBudget](../../../backend/internal/gateway/media/realtime_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-media-delivery-native-race.json.gz)。本地 HTTP/端口夹具；Range、升级/释放顺序和 Voice 预算，不证明真实音频输入输出。 |
| <a id="e17"></a>E17 Responses WS 每 turn/租约/断开排水 | [TestTurnQueueFreezesInputAndPreservesOrder](../../../backend/internal/gateway/ws/turn_test.go)（无 build tag）<br>[TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_KeepLeaseAcrossTurns](../../../backend/internal/service/openai_ws_forwarder_ingress_session_test.go)（无 build tag）<br>[TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_ClientDisconnectStillDrainsUpstream](../../../backend/internal/service/openai_ws_forwarder_ingress_session_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-ws-live-final-race.json.gz)。本地 WS/租约夹具。归档另有 other_event_type 子项跳过，不把父测试或集合视为无跳过。 |
| <a id="e18"></a>E18 Live sideband B06、零费用/observer | [TestSidebandCancelledAfterClaimReleasesWithoutTarget](../../../backend/internal/gateway/live/controller_test.go)（无 build tag）<br>[TestFinalizeClaimsOnceBeforeLeaseAndZeroUsage](../../../backend/internal/gateway/live/controller_test.go)（无 build tag）<br>[TestObserverStopCancelsAndWaitsForEnteredTask](../../../backend/internal/gateway/live/observers_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-native-live-ws-final.json.gz)。可控端口/任务夹具，取消后不取目标、零费用输入和停止等待；非真实远端 Live。 |
| <a id="e19"></a>E19 搜索工具与独立搜索 HTTP | [TestEmulatorSyntheticUsageAndEventOrder](../../../backend/internal/gateway/searchtools/emulator_test.go)（无 build tag）<br>[TestEmulatorWriteFailureStopsEventsWithoutInventingBillableUsage](../../../backend/internal/gateway/searchtools/emulator_test.go)（无 build tag）<br>[TestStandaloneSearchHTTPOrderAndResponse](../../../backend/internal/gateway/httpapi/search_handler_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-search-contract-final-race.json.gz)。本地事件/HTTP/端口夹具；合成量仍是合成量，不证明 Brave/Tavily 真实调用。 |
| <a id="e20"></a>E20 搜索额度预占/释放 | [TestS10SearchRedisReservations](../../../backend/internal/search/provider/redis_integration_test.go)（integration） | [已保存 pass 事件](parallel-evidence/s11-search-redis-retry-race.json.gz)。隔离真实 Redis，沿用 search 唯一预占实现；不代表供应商搜索完成。 |
| <a id="e21"></a>E21 完成 B01/B02、资金/日志失败分离 | [TestStopWaitsForSynchronousOverflow](../../../backend/internal/gateway/completion/lifecycle_test.go)（无 build tag）<br>[TestStartAfterStopDoesNotCreateWorker](../../../backend/internal/gateway/completion/lifecycle_test.go)（无 build tag）<br>[TestRecordSettlementFailureRetainsUnsettledFact](../../../backend/internal/gateway/completion/record_test.go)（无 build tag）<br>[TestRecordLogFailureDoesNotResettle](../../../backend/internal/gateway/completion/record_test.go)（无 build tag） | [已保存 pass 事件](completion-context-race.events.json.gz)。可控 worker/记录端口，证明等待、失败事实和不再次扣款的调用边界；不替代真实资金事务测试。 |
| <a id="e22"></a>E22 Compact 心跳/失败/单次恢复 | [TestOpenAICompactKeepaliveAdjustedWrittenSize_ExcludesHeartbeatBytes](../../../backend/internal/service/openai_compact_sse_keepalive_test.go)（无 build tag）<br>[TestWriteOpenAICompactSSEBridge_AfterKeepaliveCommitFailureEmitsFailedEvent](../../../backend/internal/service/openai_compact_sse_keepalive_test.go)（无 build tag）<br>[TestOpenAIPassthroughCompactFallbackSecondStreamFailureUsesStandardErrorPath](../../../backend/internal/service/openai_compact_fallback_test.go)（无 build tag） | [已保存 pass 事件](parallel-evidence/s11-compact-service-normal.json.gz)。本地 recorder/上游夹具；心跳扣除、已提交后流内失败及恢复耗尽后的原错误链。 |
| <a id="e23"></a>E23 固定 Qoder Execute 与真实资金/规则存储 | [TestS09QoderHTTPStorageChain](../../../backend/internal/app/qoder_gateway_integration_test.go)、[TestS11ErrorRulesStorageAndSubscription](../../../backend/internal/app/gateway_error_rules_integration_test.go)（integration） | [14 条 integration race 通过事件](fixed-execute-storage-race.events.json.gz)。本地 Qoder HTTP、真实 PostgreSQL/Redis；通过夹具固定运行时调用现有资金/事实能力，校验非流/SSE/部分失败/超时/断开尾部、等待和规则发布。不是生产供应商验证，也不把夹具完成回调等同完整生产装配。 |
| <a id="e24"></a>E24 standard/simple 真实进程关闭 | [TestS02ProcessModes](../../../backend/internal/app/process_integration_test.go) 的 standard-sigterm、simple-sigterm（integration） | [3 条父子通过事件](gateway-process-shutdown.events.json.gz)。真实进程与隔离数据库/Redis，核对 HTTP、网关请求/attempt、完成队列及共享资源停止顺序。 |

## 验证范围与待验边界

- **路由/HTTP**：E01、E02、E06、E13 证明具名注册、拒绝顺序、请求读取与展示断言；拒绝请求的成功测试不计供应商推理成功。Bedrock、Vertex、Service Account 及未列平台/协议组合不能从基础平台行推导覆盖。
- **本地输出与真实存储**：E03—E19、E21、E22 主要使用本地 HTTP/SSE/WS 或端口替身。E15、E20 明确使用隔离真实 Redis，但不因此获得真实供应商或全链 PostgreSQL 结算证明；E23 单独提供固定运行时与真实资金/事实存储组合证据，E24 提供真实生产进程关闭证据。Qoder 的 `qoder_gateway_test.go` 通过完成回调计数验证一次完成，本表已将其与真实扣款分开。
- **完成与取消资格**：部分失败完成、断开排水、重试窗口及模型/用量快照以测试实际入口为限；不将 Qoder 部分失败资格推广到其他平台、WS 或 Live。Live 仍验证零费用记录输入，完成队列的共同契约见 E21。
- **真实供应商与跳过**：普通全量 4 项跳过为 `TestRealAPI`、`TestReadLocalAuthFromDisk`、`TestEstimateOpenAIInputTokens_CompareWithOpenAIAPI`、`TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_PassthroughBridgeBoundaries/other_event_type`，见 [普通全量重跑事件](final-normal-recheck.events.json.gz)。这些不计通过；unit 额外跳过及真实供应商、硬件、外部 TLS/E2E 限制仍单列，本表没有新取得这些环境的验收证据。
- **迁移回归补验**：CN 六个子场景及父项的 [unit 原始失败](final-unit.events.json.gz)保留。完成输入已改为只读投影，新增引用隔离回归；[unit 全量重跑](final-unit-recheck.result.json) 19,773 条通过、8 项既有跳过，无失败。三组最终结果及其余收尾证据见[统一验收汇总](verification.md)。
