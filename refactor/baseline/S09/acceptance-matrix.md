# S09 验收证据导航

每个 result 保存原命令、Go 工具链、退出码、父子测试事件数及脱敏日志位置；events 保存实际执行事件。集合之间存在重叠，不能相加为独立测试数量。规划复现的预期失败和过程中失败日志均保留，不能计为通过。

| 验证面 | 可追踪证据 | 实际边界 |
| --- | --- | --- |
| 首条 Qoder Chat 非流/SSE 完整链 | [先行门禁](qoder-first-chain-gate.json)、[完整链](qoder-final-first-chain.result.json)、[资金与记录失败](qoder-completion-failures.result.json)、[真实存储](qoder-final-storage-race.result.json) | 新 HTTP/gateway/upstream 贯通；前导、慢下游、取消、部分结果和一次完成处理 |
| Anthropic / Bedrock | [平台门禁](anthropic-bedrock-gate.json) | 本地签名、事件帧、流/非流、账号凭据与存储适配；无真实 AWS/Claude 账号调用 |
| Gemini / Code Assist | [平台门禁](gemini-gate.json) | 请求/流、媒体及本地 OAuth 与真实存储；批量任务状态机仍由原业务持有 |
| Vertex 与 B03 | [平台门禁](vertex-gate.json)、[定向消费者 race](vertex-final-consumer-race.result.json) | 200ms 等锁服从取消；保留锁与正常失败降级 |
| Antigravity | [平台门禁](antigravity-gate.json) | 原生包装、credits、五种单次 Execute、部分用量与资源关闭 |
| CN / Ollama / usageprovider | [平台门禁](usage-gate.json) | 模式、HTML/配额、账户身份复核及 CAS；手动查询没有新增健康/账单写入 |
| Grok | [平台门禁](grok-gate.json) | 媒体/Voice/Responses、CLI 403 回退、Redis 一次性会话与凭据事务 |
| OpenAI B04/T01 | [固定 race](openai-fixed-race.result.json)、[原多轮测试五轮](openai-fixed-original-ws-race.result.json) | 会话默认模型原子读写；各轮固化快照仍独立；设置替身保留原断言 |
| OpenAI 协议/请求与 WS | [最终消费者 race](openai-final-consumer-race.result.json)、[原生/池 race](openai-final-native-race.result.json)、[WS/计数](openai-ws-headers-unit.result.json) | Header、指纹、Raw Chat/Responses/Messages、尾部 usage、pool/relay；旧入站与资金完成留 S11 |
| OpenAI 真实存储及本地 OAuth | [PostgreSQL/Redis race](openai-token-postgres-redis-race.result.json)、[导入/outbox 回滚](openai-import-postgres-race.result.json) | 原 token key/TTL、管理员替换 CAS、同连接回滚；仅本地 TLS |
| 图片/辅助执行资源 | [Images TLS](openai-images-execute-tls-race.result.json)、[Embeddings TLS](openai-native-embeddings-tls-race.result.json)、[Alpha 原始 Body](openai-alpha-resource-fixed-race.result.json) | 真实本地网络与关闭顺序；不把失败变成成功，也不估算未观测用量 |
| 进程和生命周期 | [真实进程](openai-process-modes-order.result.json) | standard/simple、SIGTERM、监听失败、bootstrap 释放、setup/CLI/AUTO_SETUP、jwtgen、版本；原生/授权资源先于共享存储结束 |
| 历史例外与偶发观测 | [独立清单](incidental-observations.json) | X01/X02 测试夹具、X03 Alpha 关闭；I03 单次心跳失败归因尚未确认，未调整生产算法 |
| 依赖与文件选择 | [OpenAI 33 夹具](openai-depguard-fixtures.json)、[七个构建集合](openai-buildsets.json)、各平台门禁 | 预期拒绝是规则证据，不是测试失败；e2e/跨平台选择和仅编译不计行为通过 |
| 迁移、消费者及退出 | [迁移账本](migration-ledger.md)、[文件摘要](final-file-summary.json)、[消费者摘要](final-consumers-summary.json)、[交接](handoff.md) | Go 类型信息记录直接引用与接口候选；实际生产绑定由 Wire 与 app 引用定位 |

阶段全量测试、lint、构建及完成状态由阶段文件最后的完成记录统一给出。必要项目未执行前保持待验。供应商真实账号、外部 TLS、硬件 attestation 与未配置真实 E2E 的限制继续保留。
