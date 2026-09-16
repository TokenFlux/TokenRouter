# S11 迁移与交接账本

本阶段实现及约定验收已完成，结果与限制见[验收汇总](verification.md)。本账本与[逐文件清单](final-migration-files.json.gz)、[入口矩阵](entry-contract-matrix.md)、[构建选择](build-selection-results.json)共同使用；类型引用、接口实现分别见 [normal](final-symbol-references-normal.json.gz)、[unit](final-symbol-references-unit.json.gz)、[integration](final-symbol-references-integration.json.gz)。三个类型集合均无加载诊断；反射、脚本和文档引用仍按本表和阶段文档人工核对。

## 生产实现与旧入口

| 原职责/入口 | 唯一实现与构造 | 已保留的契约 | 兼容/剩余消费者 |
| --- | --- | --- | --- |
| 通用/Google API Key 凭据与复合 Key 中间件 | apikey/httpapi + gateway/httpapi/authorization、api_key_composite、api_key_model_mapping；gateway/admission | 原凭据/错误顺序，普通门禁不读 body，复合映射与最终授权 | server/middleware 精确文件只投影旧 context；S15/S16 清理 |
| Messages、通用 Responses/Chat、Gemini 原生 | gateway/httpapi 与 gateway/text 固定执行器 | 每请求唯一循环；每 attempt 重建，部分结果资格、签名/摘要和 fallback 保持 | 旧 Handler 公开入口委托；单步执行依赖在构造时固定 |
| OpenAI Responses/Messages/Chat | OpenAITextHandler + ResponsesExecutor | 同账号重试、首输出、429、图片部分成功、逐 turn 快照 | service/handler 过渡 Adapter 只提供投影和已迁模块调用 |
| Qoder Chat | QoderChatHandler →固定 Execute；app/qoder_runtime | S09 部分失败结算、推理前取消、流式断开尾部用量 | 旧 app/legacybridge/qoder_chat.go 已删除；恢复同路径不能继承许可 |
| Qoder Messages/Responses | gateway/text/qoder_compatible + HTTP 对应入口 | 不添加等待后权益检查；任意字节输出关闭重试，Responses 额外粘性绑定 | 旧 handle 仅委托；保留与 Chat 的原差异 |
| Request/ExecutionResult 契约 | gateway/execution；旧 gateway 导出名为别名 | 显式请求值、同步 OutputSink、结果与错误可共存 | Qoder API 兼容；契约叶子禁止反向 import 实现 |
| 候选计划结果 | 在 Select 返回时读取已捕获 Account.resolvedCandidate | 不增加查询、不在执行结束后重算；PlanProvided 区分缺失 | 旧账号只提供只读投影，计划在新结果中独立复制 |
| 通用单次 Forward 与协议转换推进 | gateway/forward；HTTP 转换输出 Adapter | 原凭据/映射顺序、立即首事件、部分 usage、结束与错误 | 旧 service 方法委托；纯协议算法继续由 protocol/bridge 唯一提供 |
| OpenAI HTTP、passthrough、原生 Anthropic/raw Chat | gateway/provider/openaiforward + HTTP 输出 | 同账号恢复/端点条件、Header、未知字段、读写取消 | 旧 OpenAIGatewayService 方法投影；平台原语及池仍在 upstream |
| Grok Responses、Composer、观测 | gateway/provider/grokforward | 搜索/图片资格、协议事件、健康命令顺序 | 账号领域规则未迁入 provider，仍调用所属模块 |
| WS relay 与 HTTP-to-WS 恢复 | gateway/ws、ResponsesWSHandler、RunHTTPForward | 双向 relay、原重连预算/载荷、硬绑定、关闭帧 | 旧服务兼容方法；连接池唯一在 upstream/openai |
| Live/controller/observer | gateway/live 与 LiveHandler | B06、原 250ms、独立租约、仅零费用记录 | 旧服务持有唯一状态投影，app 管理停止；S15 聚合清理 |
| compact 状态、fallback、keepalive | gateway/compact + gateway/httpapi | 原条件、间隔、一次停止、输出前/后恢复边界 | 旧 compact 入口委托；不新增配置和恢复次数 |
| 摘要、隔离、previous/reasoning/密文状态 | gateway/session、rediscache | 原键/TTL/hash、一次性/归属语义，跨请求副本 | 调度粘性仍归 scheduler；授权 session 仍归 account |
| 客户端识别、提示替换、引导报文 | gateway/clientmeta、promptpolicy、requeststate | 版本/CLI 边界、原 TTL、JSON/XML、工具配对及原载荷 | 平台默认值/设置装配由旧入口窄投影；无第二份算法 |
| 模型替换缓存、图片意图与 thinking | requeststate、media.ImageIntentPolicy | 请求内缓存、Grok 显式/宽泛区别、模型与尺寸、GLM/签名差异 | 平台纯工具判定注入，核心不导入具体 upstream |
| count_tokens、本地估算、input_tokens | gateway/tokenestimate、text/count_tokens/single_count/input_tokens、HTTP | 无槽/原选择资源、独立预算、原 JSON；估算不结算 | 旧估算函数与 handler 委托；原外部供应商比较测试限制保留 |
| 模型列表与 Gemini list/get | ModelsHandler + routing 端口 | 空成功不恢复默认，稳定排序、资格与失败回退 | 旧资源入口委托；公开 usage 继续由 usage 提供 |
| 图片、Grok 视频、Voice/音频、资源下载 | gateway/media 与 HTTP | 实际产出、归属、pending/billed、完成认领、流关闭 | creative/batchimage 状态机及预占仍属 S13 |
| WebSearch/XSearch、工具模拟 | gateway/searchtools + SearchHandler | 原工具事件、搜索用量、合成估算不变成供应商费用 | Brave/Tavily 配额归 search；原生搜索归 upstream |
| 审核调用时机、Cyber 观测/补记 | gateway/moderationflow、HTTP、completion.RecordCyber | 普通与 Cyber 不同范围/计数；提交前快照；原后台预算 | moderation 唯一处置，ops 唯一队列，未新增生命周期拥有者 |
| 完成计算/结算/分析事实 | gateway/completion.Recorder | 原金额算法、时刻、用户与账号成本；日志失败不重扣 | app 先绑定唯一 Forward/OpenAI Recorder；旧 RecordUsage 仅投影 |
| 完成队列与 B01/B02 | gateway/completion worker | 同时等待排队/内联，停止不可逆，mandatory 与超时保持 | 旧类型别名；未新增持久队列或崩溃恢复保证 |
| 错误规则 B03—B05 | gateway/errorpolicy、postgres、rediscache、HTTP | 本地协调发布、深复制、可取消预热；原通知协议 | 只影响展示/原监控跳过，未改变健康/重试/资金规则 |
| 整图活动与停止 | app/gateway_activity + lifecycle.Operations | GatewayRequestsAndAttempts 与 HTTPRequests 同阶段等待，完成队列/依赖随后停止 | 不改变供应商取消；预算耗尽仍失败并报告未完成项 |

## 生产构造与 Wire

`app/gateway_http.go` 固定构造文本、计数、模型、媒体、Live、WS 与 Qoder 兼容入口；搜索由专用 app provider 构造。固定执行器创建前绑定同一 `GatewayCompletionRecorders`，避免使用兼容备用 facade。`gateway_completion_bindings.go` 继续完成原 Handler 兼容绑定并固定 Cyber HTTP 对象。

`app/wire.go` 是手写装配来源，`go generate ./cmd/server` 委托生成 app/wire_gen.go。只运行 Wire；没有生成 Ent。真实进程回归确认 standard/simple 中新的请求屏障、完成器和共享依赖按原有界预算停止。最终生成幂等、二进制及全量结果见阶段验证汇总。

## 兼容清理和后续阶段

- **S12**：支付、推广、返利、订单与退款完整业务闭合事务。网关完成器不接管这些保证。
- **S13**：creative/batchimage 的状态机、任务恢复、任务 UI、任务专属完成资格。只复用本阶段单次执行与已迁资金接口。
- **S14**：维护/升级、系统操作锁、重启与初始化恢复编排，不因网关迁移扩展作用域。
- **S15**：旧聚合 Handler/Service/Repository、provider 集合、兼容 context 投影与单步 Adapter 的最终装配清理。
- **S16**：消费者清零后的别名和测试转接删除、缓存旧格式兼容与约定外部环境补验。

[首轮私有包装清理](compatibility-pruning.json)按所有构建条件扫描标识符消费者：108 项无消费者删除，20 项仅 unit 使用移入对应测试文件；[第二轮](compatibility-pruning-second.json)继续删除 28 项并转移 1 项测试包装。后续少量成组常量/方法按实际引用处理；没有增加忽略规则或删除测试断言。

原始失败、迁移中间态和后续通过保留在独立日志；不能把早期编译/fixture 失败计为最终通过。已登记的 Gin 全局测试模式竞争按独立顶层运行补验，保留原内部并发、断言及失败日志，不修复清单外历史问题。
